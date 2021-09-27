// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"strings"

	"go.etcd.io/etcd/raft/raftpb"
)

func main() {
	// 集群配置（ raft 集群配置）
	cluster := flag.String("cluster", "http://127.0.0.1:9021", "comma separated cluster peers")
	// 集群的第几个节点
	id := flag.Int("id", 1, "node ID")
	// 业务端口
	kvport := flag.Int("port", 9121, "key-value server port")
	join := flag.Bool("join", false, "join an existing cluster")
	flag.Parse()

	// proposeC 这个 channel 是用来和 raft 交互的管道，用来给业务传递输入进 raft 状态机，所以是 业务侧来 new，然后传递给 raft 内部；
	proposeC := make(chan string)
	defer close(proposeC)
	confChangeC := make(chan raftpb.ConfChange)
	defer close(confChangeC)

	// raft provides a commit stream for the proposals from the http api
	var kvs *kvstore
	getSnapshot := func() ([]byte, error) { return kvs.getSnapshot() }
	// commitC 是用来给 raft 状态机传递数据出来用的，是输出，是已经被 raft 状态机 commit 过的数据，业务侧拿来用；
	// snapshotterReady 也是传入输出用的，所以类似；
	commitC, errorC, snapshotterReady := newRaftNode(*id, strings.Split(*cluster, ","), *join, getSnapshot, proposeC, confChangeC)

	kvs = newKVStore(<-snapshotterReady, proposeC, commitC, errorC)

	// the key-value http handler will propose updates to raft
	serveHttpKVAPI(kvs, *kvport, confChangeC, errorC)
}
