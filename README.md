# etcd 代码学习

关于 etcd 注释说明：

1. etcd 版本 tag: v3.4.10
2. etcd官网：https://etcd.io

本项目包含详细的代码阅读注释，记录详细的中文 etcd 的代码阅读注释，配套梳理文档。

## 作者公众号：奇伢云存储

## 源码博客

- [Go Etcd 源码学习【1】开篇.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%901%E3%80%91%E5%BC%80%E7%AF%87.md)
- [Go Etcd 源码学习【2】编译看看.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%902%E3%80%91%E7%BC%96%E8%AF%91%E7%9C%8B%E7%9C%8B.md)
- [Go Etcd 源码学习【3】raftexample 学习.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%903%E3%80%91raftexample%20%E5%AD%A6%E4%B9%A0.md)
- [Go Etcd 源码学习【4】raftNode 模块.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%904%E3%80%91raftNode%20%E6%A8%A1%E5%9D%97.md)
- [Go Etcd 源码学习【5】Storage 是什么.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%905%E3%80%91Storage%20%E6%98%AF%E4%BB%80%E4%B9%88.md)
- [Go Etcd 源码学习【6】Transport 网络层模块.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%906%E3%80%91Transport%20%E7%BD%91%E7%BB%9C%E5%B1%82%E6%A8%A1%E5%9D%97.md)
- [Go Etcd 源码学习【7】WAL 是什么.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%907%E3%80%91WAL%20%E6%98%AF%E4%BB%80%E4%B9%88.md)
- [Go Etcd 源码学习【8】raft.node.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%908%E3%80%91raft.node.md)
- [Go Etcd 源码学习【9】raft 状态机.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%909%E3%80%91raft%20%E7%8A%B6%E6%80%81%E6%9C%BA.md)
- [Go Etcd 源码学习【11】配置变更.md](https://github.com/liqingqiya/readcode-etcd-v3.4.10/blob/master/docs/Go%20Etcd%20%E6%BA%90%E7%A0%81%E5%AD%A6%E4%B9%A0%E3%80%9011%E3%80%91%E9%85%8D%E7%BD%AE%E5%8F%98%E6%9B%B4.md)


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
