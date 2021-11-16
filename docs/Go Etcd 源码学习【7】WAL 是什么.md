[toc]

wal 是 Write Ahead Log 的缩写。顾名思义，更新之前先写日志。本质是保证数据操作原子性和持久化的一种手段。

etcd 把这个 wal 抽离出来成一个独立的模块，位于 etcd/wal/ 目录下。

主要有几个文件：

- decoder.go
- encoder.go

这两个文件是解决数据编码和解码的，数据存储在底层介质，必须是有特定格式的。有了这层封装，上层的业务就不用关心底层的数据格式了。

- file_pipeline.go 

抽象出一个分配器，这个分配器能够分配指定大小的空间（用户不用关心内部细节，只需要知道数据能存下去，能读出来即可）。

初始化这个流式分配器只需要传入一个目录，一个定长的大小即可。这样就方便 wal 写空间了。

- metrics.go 

是上报的指标，标名当前 wal 日志的一些情况。

- repair.go


- util.go

这里面主要是一些工具函数。

比如 Exist 判断 wal 目录下是否存在日志文件。searchIndex 则是返回最后一个大于一个 index 的文件。

readWALNames  返回合法的 wal 文件列表。

parseWALName 则是分析 wal 文件名字的函数，因为名字里包含了信息，wal 文件创建之初就是 "${seq}-${index}.wal" ，seq 是第几个文件的意思（文件号编码） ，index 是 最后一条 entry 的 index 编码。


- wal.go 

这里面则是 wal 的实现，最关键的一个就是 Save 方法，这个方法在处理 Ready 结构数据调用。

```go
// raftexample/raft.go
func (rc *raftNode) serveChannels() {

    // 获取到 Ready 数据
    case rd := <-rc.node.Ready():
        // 还在内存未持久化的需要通过 wal 模块持久化到磁盘
        rc.wal.Save(rd.HardState, rd.Entries)

        // 把这些已经持久化的数据添加到 Storage 存储，以便 raft 状态机进行日志的判断；
        rc.raftStorage.Append(rd.Entries)
}
```

在 raftexample 中，这个地方是最关键的应用场景。