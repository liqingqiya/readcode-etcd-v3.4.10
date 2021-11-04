[toc] 

前面简要的学习了下 Storage ，学习了下 WAL ，对于 raftexample 来说这两个加起来才形成了可靠的日志。

Storage 给 raft 状态机内部用的，都是持久化了的日志，检索日志的接口。

# raftNode

raftNode 则是组装这一切的业务实现。raftNode 是 raftexample 实现的承接业务，对接 raft 状态机的中间层。

raftNode 实现集中在 raftexample/raft.go 这个文件。

外部传入的两个 channel ：

1. proposeC
2. confChangeC

这两个都是外部投递消息的，给到底下去处理。并且也是外部初始化好传递给 raftNode 的。由上而下。

raftNode 内部也有两个给上层用的 channel：

1. commitC
2. errorC

这两个 channel 是 newRaftNode 的时候创建的，由下而上。

## newRaftNode

```go
func newRaftNode(id int, peers []string, join bool, getSnapshot func() ([]byte, error), proposeC <-chan string,
    confChangeC <-chan raftpb.ConfChange) (<-chan *string, <-chan error, <-chan *snap.Snapshotter) {

    rc := &raftNode{
        proposeC:    proposeC,
        confChangeC: confChangeC,
        commitC:     commitC,
        errorC:      errorC,
        id:          id,
        peers:       peers,
        // ...
    }
    // 开启 raftNode 内部处理协程
    go rc.startRaft()
    // 返回对外提供的 channel
    return commitC, errorC, rc.snapshotterReady
}
```

所以关键在于 rc.startRaft( ) 这个函数做了什么？

## startRaft

```go
func (rc *raftNode) startRaft() {

    // 恢复 wal 日志
    oldwal := wal.Exist(rc.waldir)
    rc.wal = rc.replayWAL()

    // 构造 raft 状态机配置。
    // 注意到，Storage 就是在这个地方被传入到 raft 状态机的
    c := &raft.Config{
        ID:                        uint64(rc.id),
        ElectionTick:              10,
        HeartbeatTick:             1,
        Storage:                   rc.raftStorage,
        MaxSizePerMsg:             1024 * 1024,
        MaxInflightMsgs:           256,
        MaxUncommittedEntriesSize: 1 << 30,
    }

    // 启动 raft 状态机
    if oldwal {
        rc.node = raft.RestartNode(c)
    } else {
        rc.node = raft.StartNode(c, startPeers)
    }

    // 创建被节点的 transport （网络传输层）
    // 注意：这里传入了 raftNode（因为要业务定制网络消息的处理 Process 接口）
    rc.transport = &rafthttp.Transport{
        Logger:      zap.NewExample(),
        ID:          types.ID(rc.id),
        ClusterID:   0x1000,
        Raft:        rc,
        ServerStats: stats.NewServerStats("", ""),
        LeaderStats: stats.NewLeaderStats(strconv.Itoa(rc.id)),
        ErrorC:      make(chan error),
    }
    rc.transport.Start()
    for i := range rc.peers {
        // 初始化和集群其他节点通信的通道（本节点不初始化）
        if i+1 != rc.id {
            rc.transport.AddPeer(types.ID(i+1), []string{rc.peers[i]})
        }
    }
    // 开启两个服务；
    // 一个服务是 tcp server 的服务，这个是网络通道，走内部 raft 数据流的
    go rc.serveRaft()
    // 另一个则是 channel 消息的处理，真正核心的定制，处理 proposeC，confChangeC，commitC，errorC 这四个通道
    go rc.serveChannels()
}
```

从上面来看 startRaft 方法内部做的主要逻辑是：

1. 加载 wal 持久化的日志信息；
2. 初始化 raft 状态机；
3. 创建网络通信层；
    1. 并传入 raftNode 作为网络消息的地址处理；
4. 开启两个内部处理子协程，一个是处理网络消息，一个是处理业务 channel ；

## raftNode.serveRaft

这个方法是偏底层的，主要是构建 raft 通信的数据通道。

```go
func (rc *raftNode) serveRaft() {
    // 解析出地址
    url, err := url.Parse(rc.peers[rc.id-1])
    if err != nil {
        log.Fatalf("raftexample: Failed parsing URL (%v)", err)
    }
    // 定制一个 listener
    ln, err := newStoppableListener(url.Host, rc.httpstopc)
    if err != nil {
        log.Fatalf("raftexample: Failed to listen rafthttp (%v)", err)
    }
    // 创建出一个 http server，用于走 raft 数据流
    err = (&http.Server{Handler: rc.transport.Handler()}).Serve(ln)
    select {
    case <-rc.httpstopc:
    default:
        log.Fatalf("raftexample: Failed to serve rafthttp (%v)", err)
    }
    close(rc.httpdonec)
}
```

先说 newStoppableListener 这个函数，这个函数定义在 raftexample/listener.go 这个文件。返回一个定制了 Accept 的监听对象 stoppableListener 。为什么要专门定制一下 Accept ？

为了简单支持两个功能：

1. 停服（外层可以控制停服，因为 stopc 是传进来的）；
2. 网络设置（比如一些保活的设置）


并且注意到，http server 在初始化的时候，还初始化了一个 Handler：

```go
http.Server{Handler: rc.transport.Handler()}
```

相当于定制了下 http 的入口处理函数。那这个 Handler 会是什么样子的呢？

这里相当于给 /raft ，/raft/probing ，/raft/stream ，/raft/snapshot 这四个路径分别注册了四个处理逻辑。

- /raft ：pipelineHandler ，发送的各种常规的 raft 消息，都是走这个路径，比如 MsgApp 类消息。
- /raft/probing ：probing.NewHandler() ，心跳路径
- /raft/stream ：streamHandler ，
- /raft/snapshot ：snapHandler ，快照传输路径

可以先知道下**网络层级关系**：

```sh
-> transport
    -> peer
        -> pipeline 
        -> stream 
            -> stream msgappv2
            -> stream message
```


完整描述一遍消息的传输:

**网络层初始化** :

1. 创建 peer 的时候, 就会调用 startPeer 创建好两个 streamReader ;
2. 这两个 streamReader 在后台调用 cr.dial( t ) 创建好长连接通道 ; 
    1. /raft/stream/msgapp 
    2. /raft/stream/message
3. 对端收到建连请求, 调用到 streamHandler.ServeHTTP 来处理这个请求 ;
    1. 找到对应的 peer 结构体
    2. 把这个 conn 添加到这个 peer 内部的 channel ( p.attachOutgoingConn( conn ) ) ; 
4. streamWriter.run 内部收到这个 conn 结构体 , 于是创建对应的 encoder ( newMsgAppV2Encoder ) , 之后走这个 encoder 就相当于把数据发走了 ; 
    1. run 方法内部则是循环处理消息和连接
5. 读端建连陈工之后, 就在 streamReader.decodeLoop 中循环处理收到的数据即可 ; 
    1. 传入 Reader 创建一个 decoder ,循环处理数据 ; 

这样长连接就由读端发起, 创建完毕 .

**发送过程**: 

1. raftexmaple 调用 rc.transport.Send( rc.Message ) 来发送消息 ;
2. 找到节点对应的 peer ,调用 p.send( m ) 发送消息 ;
3. 根据 m 类型, p.pick( m ) 选取通道 ;
    1. MsgSnap 类型 : pipeline 通道
    2. MsgApp 类型 : stream msgappv2 通道
    3. 其他类型 : stream message 通道
    4. 如果没有 stream 通道 , 则使用 pipeline 通道 
4. 假设是 MsgApp 类型, 那么把 message 投递到对应 channel 里面 ; 
    1. 拿到的是 stream msgappv2 对应的 streamWriter.msgc 这个 channel ; 
5. streamWriter.run 里从 msgc channel 中获取到 message ,调用 enc.encode( m ) 把数据编码并且写到网络, 这样数据就走了.

**接收过程** : 

1. 对端从  streamReader.decodeLoop 中的 dec.decode( ) 位置唤醒 , 读取到一个 mesage ; 
2. 消息类型是 MsgApp , 所以使用的是 cr.recvc , 而这个 channel 是 peer 传进来的, 其实是 peer.recvc ;
3. peer 则是在 startPeer 的时候开启了一个处理 p.recvc 的子协程, 调用 r.Process( ctx, mm ) 来处理这个消息, 于是消息就走到 raftNode 的 Process 方法 ; 
4. 而在 raftexample 的 raftNode.Process 中是直接调用的 raft 状态机的 Step 方法 ( rc.node.Step( ctx, m ) ) , 这样消息就来到了 raft 状态机内部了. 

再往后处理就是到业务了. 网络层就这么多了，下面看下业务。

## raftNode.serveChannels

这个方法是业务必须要定制的逻辑，主要是上层传递下来，底层抛上来的消息，是承上启下的关键之一。

```go
func (rc *raftNode) serveChannels() {
    // 子协程：处理上层业务递送的消息
    go func() {
        for rc.proposeC != nil && rc.confChangeC != nil {
            select {
            case prop, ok := <-rc.proposeC:
                // 消息递交，等待 raft 状态机接收
                rc.node.Propose(context.TODO(), []byte(prop))
            case cc, ok := <-rc.confChangeC:
                // 配置变更信息
                rc.node.ProposeConfChange(context.TODO(), cc)
            }
        }
    }()

    // 子协程：处理底层状态机抛上来的消息
    for {
        select {
        case <-ticker.C:
            rc.node.Tick()
        // 重要！！！
        case rd := <-rc.node.Ready():
            // 还在内存未持久化的需要通过 wal 模块持久化到磁盘
            rc.wal.Save(rd.HardState, rd.Entries)
            // 添加到内存存储
            rc.raftStorage.Append(rd.Entries)
            // 发送到远端节点，发网络这个也是抽离出来的，message 里面标识了节点去向
            rc.transport.Send(rd.Messages)
            // rd.CommittedEntries 作为消息状态机的输出，输出完之后，会传递给最外层的 commitC channel
            if ok := rc.publishEntries(rc.entriesToApply(rd.CommittedEntries)); !ok {
            }

            // 前进吧，状态机
            rc.node.Advance()
        }
    }
}
```

raftNode 作为中间节点，本质上做好两件事就好：

1. 上层递交的消息要及时的递送到 raft 状态机；
2. raft 状态机抛上来已经处理好的消息，要及时处理；

上面这两个子协程就是专门干这两件事的。分别对应了几个 channel 的处理：

1. proposeC，confChangeC 是上层递送消息的通道；
2. rc.node.Ready() 是底层抛上来的数据，业务处理完成之后，调用 rc.node.Advance() 推进下 raft 状态机进度；
3. rc.node.Tick() 是和底层的时钟步进协调；

我们看到，ready 数据来的时候，做了三件事：

1. 把 rd.HardState ，rd.Entries 持久化，然后 append 放到状态机的持久化日志中（ Storage ）；
2. 把 rd.Messages 发送到网络；
3. 把 rd.CommittedEntries 已经 commit ，但是还未 apply 的 apply 掉；

apply 消息则是调用 raftNode.publishEntries 方法来做这个事。

## raftNode.publishEntries

这个是 raftNode 把待 apply 的消息全部递送出去，通过 commitC 。

```go
func (rc *raftNode) publishEntries(ents []raftpb.Entry) bool {
	for i := range ents {
		switch ents[i].Type {
		case raftpb.EntryNormal:
			select {
            // 递送 commitC
			case rc.commitC <- &s:
			}
		case raftpb.EntryConfChange:
            // 处理配置变更
		}
        
        // 更新 appliedIndex
		rc.appliedIndex = ents[i].Index
	}
	return true
}
```

主要做了几件事：

1. 把待 apply 的消息投递到 commitC，等待外层业务去处理；
    1. 如果是配置变更，那么就要对应处理；
2. 更新 appliedIndex ；
