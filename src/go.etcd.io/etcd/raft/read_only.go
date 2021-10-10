// Copyright 2016 The etcd Authors
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

package raft

import pb "go.etcd.io/etcd/raft/raftpb"

// ReadState provides state for read only query.
// It's caller's responsibility to call ReadIndex first before getting
// this state from ready, it's also caller's duty to differentiate if this
// state is what it requests through RequestCtx, eg. given a unique id as
// RequestCtx
type ReadState struct {
	Index      uint64
	RequestCtx []byte
}

type readIndexStatus struct {
	req   pb.Message
	index uint64
	// NB: this never records 'false', but it's more convenient to use this
	// instead of a map[uint64]struct{} due to the API of quorum.VoteResult. If
	// this becomes performance sensitive enough (doubtful), quorum.VoteResult
	// can change to an API that is closer to that of CommittedIndex.
	acks map[uint64]bool
}

type readOnly struct {
	option           ReadOnlyOption
	pendingReadIndex map[string]*readIndexStatus
	readIndexQueue   []string
}

// 配合 readindex 请求使用的结构体
func newReadOnly(option ReadOnlyOption) *readOnly {
	return &readOnly{
		option:           option,
		pendingReadIndex: make(map[string]*readIndexStatus),
	}
}

// 添加一个 read only 请求，收到 MsgReadIndex 请求的时候调用。
// addRequest adds a read only reuqest into readonly struct.
// `index` is the commit index of the raft state machine when it received
// the read only request.
// `m` is the original read only request message from the local or remote node.
func (ro *readOnly) addRequest(index uint64, m pb.Message) {
	s := string(m.Entries[0].Data)
	if _, ok := ro.pendingReadIndex[s]; ok {
		return
	}
	// ctx 和一个 status 加入 map
	ro.pendingReadIndex[s] = &readIndexStatus{index: index, req: m, acks: make(map[uint64]bool)}
	// ctx 加入数组
	ro.readIndexQueue = append(ro.readIndexQueue, s)
}

// 收到 节点：$id 的回应，则添加到对应 map 中，并且返回这张表。
// 这个在两个场景会调用到：
// 1. 本节点收到 readindex ，可以把本节点直接设置成 ack；
// 2. follower 节点发过来的 heartbeat resp，可以把对应 follower 节点设置成 ack；
// recvAck notifies the readonly struct that the raft state machine received
// an acknowledgment of the heartbeat that attached with the read only request
// context.
func (ro *readOnly) recvAck(id uint64, context []byte) map[uint64]bool {
	// 根据 ctx 找到这个 status
	rs, ok := ro.pendingReadIndex[string(context)]
	if !ok {
		return nil
	}
	// 添加对应的节点 ack 到这个 status
	rs.acks[id] = true
	return rs.acks
}

// 只在 heartbeat resp 里调用
// advance advances the read only request queue kept by the readonly struct.
// It dequeues the requests until it finds the read only request that has
// the same context as the given `m`.
func (ro *readOnly) advance(m pb.Message) []*readIndexStatus {
	var (
		i     int
		found bool
	)

	ctx := string(m.Context)
	rss := []*readIndexStatus{}

	// 遍历 ctx 数组
	for _, okctx := range ro.readIndexQueue {
		i++
		rs, ok := ro.pendingReadIndex[okctx]
		if !ok {
			panic("cannot find corresponding read state from pending map")
		}
		// 找到指定之前的全部添加到 rss 数组
		rss = append(rss, rs)
		// 找到指定的 ctx
		if okctx == ctx {
			found = true
			break
		}
	}

	if found {
		// ro.readIndexQueue 找到指定的位置，前面的截断
		ro.readIndexQueue = ro.readIndexQueue[i:]
		for _, rs := range rss {
			// 清理掉 map 里对应的 ctx（ 全都是目标前面的 ）
			delete(ro.pendingReadIndex, string(rs.req.Entries[0].Data))
		}
		return rss
	}

	return nil
}

// lastPendingRequestCtx returns the context of the last pending read only
// request in readonly struct.
func (ro *readOnly) lastPendingRequestCtx() string {
	if len(ro.readIndexQueue) == 0 {
		return ""
	}
	// 最新的一个 readindex 请求
	return ro.readIndexQueue[len(ro.readIndexQueue)-1]
}
