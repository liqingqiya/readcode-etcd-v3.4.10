// Copyright 2019 The etcd Authors
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

package tracker

// 日志复制的状态，3 种，由 leader 管理
// StateType is the state of a tracked follower.
type StateType uint64

const (
	// StateProbe indicates a follower whose last index isn't known. Such a
	// follower is "probed" (i.e. an append sent periodically) to narrow down
	// its last index. In the ideal (and common) case, only one round of probing
	// is necessary as the follower will react with a hint. Followers that are
	// probed over extended periods of time are often offline.
	// 这种情况表明 follower 的 last index 不知道（日志缺失，中断的场景），所以需要探测这个 follower 的 last index，以便确定从哪个地方开始复制
	// leader 会不停的探测（通过心跳）到你日志开始复制的点
	StateProbe StateType = iota
	// StateReplicate is the state steady in which a follower eagerly receives
	// log entries to append to its log.
	// 表明处于一个正常复制日志的状态
	StateReplicate
	// StateSnapshot indicates a follower that needs log entries not available
	// from the leader's Raft log. Such a follower needs a full snapshot to
	// return to StateReplicate.
	// 表明 follower 复制日志有异常（非常非常落后，它最新的索引leader这里可能都丢了），所以需要得到一个完整的快照，从而恢复到 StateReplicate
	StateSnapshot
)

/*
Progress 存在于 3 个状态，StateProbe, StateReplicate, StateSnapshot。

StateReplicate <-> StateProbe <-> StateSnapshot

1. leader 收到拒绝的消息，会由 StateReplicate 切成 StateProbe
2. 收到恢复的消息之后，切成 StateReplicate。这两个状态可以相互转换；
3. 确认 follower 收到消息没希望了，直接发个完整的快照过去；
4. 收到消息之后，先切成 StateProbe 状态；
*/

var prstmap = [...]string{
	"StateProbe",
	"StateReplicate",
	"StateSnapshot",
}

func (st StateType) String() string { return prstmap[uint64(st)] }
