// Copyright 2017 The etcd Authors
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

package mvcc

import (
	"go.etcd.io/etcd/mvcc/mvccpb"
	"go.etcd.io/etcd/pkg/traceutil"
)

// watcher txn 和普通的 txn 的核心区别
// .End() 函数是唯一重写的，所以非常重要；
func (tw *watchableStoreTxnWrite) End() {
	// 获取变化
	changes := tw.Changes()
	if len(changes) == 0 {
		// 没有变动，则直接调用底层 Enc 退出
		tw.TxnWrite.End()
		return
	}
	// 获取版本号
	rev := tw.Rev() + 1
	// 构建 events
	evs := make([]mvccpb.Event, len(changes))
	// 遍历这个 changes 数组，构造 event 数组，构造完之后，使用 notify
	for i, change := range changes {
		evs[i].Kv = &changes[i]
		if change.CreateRevision == 0 {
			// 删除事件
			evs[i].Type = mvccpb.DELETE
			evs[i].Kv.ModRevision = rev
		} else {
			// 上传事件
			evs[i].Type = mvccpb.PUT
		}
	}

	// end write txn under watchable store lock so the updates are visible
	// when asynchronous event posting checks the current store revision
	tw.s.mu.Lock()
	// watch 写事务完了之后，通知 watcher
	tw.s.notify(rev, evs)
	// 调用底层的 End
	tw.TxnWrite.End()
	tw.s.mu.Unlock()
}

type watchableStoreTxnWrite struct {
	TxnWrite
	s *watchableStore
}

// 返回一个写事务对象
func (s *watchableStore) Write(trace *traceutil.Trace) TxnWrite {
	return &watchableStoreTxnWrite{s.store.Write(trace), s}
}
