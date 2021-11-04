[toc]

## api

从入口 api 层说起，入口文件在 contrib/raftexample/httpapi.go

核心结构：

```go
type httpKVAPI struct {
    store       *kvstore
    confChangeC chan<- raftpb.ConfChange
}
```

它既然是 api 接口，那么肯定对接外面的请求，协议是 http 协议，那么必定实现 ServeHTTP 的接口：

```go
func (h *httpKVAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    key := r.RequestURI

    switch {
    case r.Method == "PUT":
        // key 写入请求
    case r.Method == "GET":
        // key 读取请求
    case r.Method == "POST":
        // 集群节点扩容请求
    case r.Method == "DELETE":
        // 集群节点下线请求
```

- 写入是需要走 raft 状态机，这里是异步的，递交了没等返回；
- 读这里是直接通过 service 提供的接口进行读，读内存即可（这里没有满足线性一致性）；
- 扩容和下线自然也是要走 raft 状态机的，这里是异步的；

这就是 api 层的全部内容了，api 层把请求交给了 service 的模块，service 业务层的模块也即是 kvstore 的封装，这个是 kv 业务的实现。

## service

下面看业务层的实现，业务层实现的是一个 kv 存储。在 contrib/raftexample/kvstore.go 文件。核心结构是 kvstore ：

```go
type kvstore struct {
    proposeC    chan<- string     // channel for proposing updates
    mu          sync.RWMutex      //
    kvStore     map[string]string // current committed key-value pairs
    snapshotter *snap.Snapshotter
}
```

不出意料，核心就是一个 map ，还有一个定制 snap 的函数。对于 kv 主要是读写两个接口：

- Lookup ：从 map 中查询 key/value；
- Propose ：把写请求递交给底层，走 raft 状态机，达成分布式一致；

另外业务层对于 snap 定制了两个函数:

- getSnapshot ：创建一个节点的 snap；
- recoverFromSnapshot ：从 snap 中读取，并恢复节点数据；

另外还有个关键函数：

- readCommits ：处理哪些已经 commit 过，但是还未 apply 的信息。而在这个简单的例子中，apply 就是对 kv map 进行值得写入而已；

所以，我们看到业务层究竟做了哪些事情：

1. 实现自己业务的语义，比如这里就是一个 kv；
2. 使用 Propose 递交消息到底层；
3. 读取从底层抛上来的已经 commit 的消息，进行 apply （业务的设置）；
4. 实现一个创建快照，恢复快照的方法；

换句话说，业务只理解业务，跟底层打交道的另有其人。业务层只往下丢（propose channel），也只处理上面抛上来的（commit channel）。

那么跟业务层打交道的是谁？

关键要看谁去取  propose channel，谁去写 commit channel ？上下两个模块用这两个 channel 解耦了。

在 contrib/raftexample/main.go 里：

```go
    commitC, errorC, snapshotterReady := newRaftNode(*id, strings.Split(*cluster, ","), *join, getSnapshot, proposeC, confChangeC)
    
    kvs = newKVStore(<-snapshotterReady, proposeC, commitC, errorC)
```

所以答案就是：raftNode 这个由 raftexample 实现的模块，作为承上启下的作用。承接 service 模块，启开最底层的 raft 状态机模块。

## raftNode

raftNode 由 raftexample 实现，和 raft 状态机解耦开（ etcd 也实现了自己的 raftNode ）。

承上：读 proposeC，写 commitC ；
启下：怎么把请求合理的经过 raft 状态机，这个是重点工作；

来看一下核心结构：

```go
type raftNode struct {
    proposeC    <-chan string            // 这个给上层丢请求下来的 channel；
    confChangeC <-chan raftpb.ConfChange // 这个是上层丢集群变动的请求的 channel；
    commitC     chan<- *string           // 已经经过状态机处理，给上层返回的已经 commit 过的消息，都在这个 channel；
    errorC      chan<- error             // 错误处理，跟业务层打交道的；
    id          int                    // 集群 ID
    peers       []string               // raft 集群配置
    join        bool                   // 
    waldir      string                 // WAL 目录
    snapdir     string                 // 快照目录
    getSnapshot func() ([]byte, error) // 业务层定义的，打快照的方法；
    lastIndex   uint64                 // index of log at start
    confState     raftpb.ConfState //
    snapshotIndex uint64           // 最近一次快照记录的日志的 index，快照之前的是可以被安全删除的。这个赋值只有两种情况，1）触发一次快照 2）从持久化中加载
    appliedIndex  uint64           // 这个之前的一定是 apply 过的日志, 这个是日志索引
    // 最重要：raft 状态机；
    node        raft.Node
    raftStorage *raft.MemoryStorage
    wal         *wal.WAL
    snapshotter      *snap.Snapshotter
    snapshotterReady chan *snap.Snapshotter // signals when snapshotter is ready
    snapCount uint64
    transport *rafthttp.Transport
    // ...
}
```

这个结构体字段很多，raftNode 本来就是一个中间的组合协调的角色。大概可以划分下功能：

- 其中 proposeC，confChangeC，commitC，errorC 这几个 channel 是和业务层通信打交道的；
- raft.Node 这个字段是最核心的 raft 状态机的实现；
- 另外还组装了 wal.WAL，raft.MemoryStorage 这两个核心实现；


raftNode 实例由函数 newRaftNode 创建，结构体初始化之后，调用 rc.startRaft( ) 进行初始化。

```go
func (rc *raftNode) startRaft() {
    // 读取 wal 日志
    rc.wal = rc.replayWAL()
    // 启动网络层
    rc.transport.Start()
    // 启动后台服务
    go rc.serveRaft()
    go rc.serveChannels()
```


## raft machine

raft.Node 模块，实现都在 raft/node.go 文件中。另外不得不提的是 raft.MemoryStorage 模块还有 wal.WAL 模块，这两个是和状态机配合使用的。