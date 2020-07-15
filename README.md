# etcd 代码学习

关于 etcd 注释说明：

1. fork 于 2020/6/29, master commit: e94dc39edcf902f68be8b37051b7ff7e7ea427c4
2. etcd官网：https://etcd.io

本项目包含详细的代码阅读注释，记录详细的中文 etcd 的代码阅读注释，配套梳理文档。

作者公众号：奇伢云存储

## 模块学习

### raftexample 模块

这个模块是使用 raft 实现的完整的一个 kv 程序。对于理解 etcd/raft 非常有参考意义。

- [contrib/raftexample/httpapi.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/contrib/raftexample/httpapi.go)
- [contrib/raftexample/kvstore.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/contrib/raftexample/kvstore.go)
- [contrib/raftexample/main.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/contrib/raftexample/main.go)
- [contrib/raftexample/raft.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/contrib/raftexample/raft.go)

### raft 模块

##### raftLog

- [raft/log.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/raft/log.go)

###### unstable log

- [raft/log_unstable.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/raft/log_unstable.go)

#### node ( raft StateMachine )

raft 状态机的实现核心。

- [raft/node.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/raft/node.go)
- [raft/raft.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/raft/raft.go)
- [raft/rawnode.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/raft/rawnode.go)

###### MemoryStorage

- [raft/storage.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/raft/storage.go)


### wal

- [wal/wal.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/wal/wal.go)
- [wal/file_pipeline.go](https://github.com/liqingqiya/readcode-etcd-master/blob/master/src/go.etcd.io/etcd/wal/file_pipeline.go)
