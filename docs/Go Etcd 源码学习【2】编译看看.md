克隆仓库下来：

```sh
git clone https://github.com/liqingqiya/readcode-etcd-v3.4.10.git
```

编译（可以把一些依赖包下载下来）：

```sh
root@ubuntu:~/code/github/readcode-etcd-v3.4.10/src/go.etcd.io/etcd# make

....
....
....

go.etcd.io/etcd/etcdctl
./bin/etcd --version
etcd Version: 3.4.10
Git SHA: b516720
Go Version: go1.14.4
Go OS/Arch: linux/amd64
./bin/etcdctl version
etcdctl version: 3.4.10
API version: 3.4

```

然后，我们就去编译 raftexample 这个程序。

```sh
root@ubuntu:~/code/github/readcode-etcd-v3.4.10/src/go.etcd.io/etcd/contrib/raftexample# go build -gcflags=all="-N -l"
```

目录下出现 `raftexample` 的二进制就代表你编译成功了，也就具有了学习的素材。

安装 README 的提示，我们用 goreman 来启动程序：

```sh
goreman start
```

`goreman` 读的是当前目录的 Procfile 文件，这个文件是这样写的：

```sh
root@ubuntu:~/code/github/readcode-etcd-v3.4.10/src/go.etcd.io/etcd/contrib/raftexample# cat Procfile 
# Use goreman to run `go get github.com/mattn/goreman`
raftexample1: ./raftexample --id 1 --cluster http://127.0.0.1:12379,http://127.0.0.1:22379,http://127.0.0.1:32379 --port 12380
raftexample2: ./raftexample --id 2 --cluster http://127.0.0.1:12379,http://127.0.0.1:22379,http://127.0.0.1:32379 --port 22380
raftexample3: ./raftexample --id 3 --cluster http://127.0.0.1:12379,http://127.0.0.1:22379,http://127.0.0.1:32379 --port 32380
```

也就是启动 3 个 raftexample 进程，分别使用了不同的端口，这样就省去了我们认为的键入，方便一些。

`goreman start` 执行，你就会发现拉起了 3 个进程，这组成了一个最简单的 raft 集群。

![d5cfda376d663262bb4fe0bc093a0cdc.png](evernotecid://EA6E7D5C-909C-487C-8DC8-4FCCDA32E1B8/appyinxiangcom/11768343/ENResource/p9932)

接下来，我们就调试+阅读这个程序的代码，来理解 raft 是怎么回事，敬请期待。