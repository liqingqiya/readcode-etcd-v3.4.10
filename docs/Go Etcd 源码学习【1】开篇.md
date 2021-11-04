[toc]

## 代码仓库

奇伢拉了个仓库出来，用于阅读 etcd 的代码，版本是 v3.4.10 。

https://github.com/liqingqiya/readcode-etcd-v3.4.10

我会不断的在这个仓库里加注释。

## etcd

etcd 说白了就是是 kv 存储，只不过这个 kv 存储是分布式的，数据是多副本冗余的，kv 是有多版本功能的。

既然是分布式那就要考虑一致性，etcd 使用了 raft 算法解决了数据多节点的一致性。

所以我们第一步就是去撸一遍 raft 相关的代码，etcd 的业务从抽象模块来讲运行在 raft 模块，只要把 raft 相关模块理解了，etcd 就算理解 50 %。

## raft

理解 raft ，我们从 etcd/contrib/raftexample/ 这个目录开始。这个目录是一个完整的 package，实现了一个极简的 kv 存储，就是为了专门理解 raft 的。

```
➜  raftexample git:(master) ✗ tree -L 1
.
├── httpapi.go      # 最上层的模块，对外提供 http api，我们命名为 api 层；
├── kvstore.go      # kv 存储的实现，业务层的实现，我们命名为 service 层；
├── raft.go         # 承上启下的模块，对上对接 service 模块，对下对接 raft 状态机模块；
├── listener.go     # 和 raftNode 是一起的；
├── main.go         # 入口文件

0 directories, 10 files
```

具体模块划分：

```
api
service
raftNode
raft(状态机)
```

下一步，开始。。。