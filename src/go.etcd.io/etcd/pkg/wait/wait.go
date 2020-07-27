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

// Package wait provides utility functions for polling, listening using Go
// channel.
package wait

import (
	"log"
	"sync"
)

// Wait is an interface that provides the ability to wait and trigger events that
// are associated with IDs.
type Wait interface {
	// Register waits returns a chan that waits on the given ID.
	// The chan will be triggered when Trigger is called with
	// the same ID.
	// 注册请求等待的路径，一般在请求处理的开始的地方
	Register(id uint64) <-chan interface{}
	// Trigger triggers the waiting chans with the given ID.
	// 触发唤醒，一般调用在请求结果确认的地方
	Trigger(id uint64, x interface{})
	// 判断是否注册了，如果注册了说明某个地方会等待这个 key 的结果返回
	IsRegistered(id uint64) bool
}

type list struct {
	l sync.RWMutex
	m map[uint64]chan interface{}
}

// 创建要给同步对象
// New creates a Wait.
func New() Wait {
	return &list{m: make(map[uint64]chan interface{})}
}

// 注册一个 key 进去，返回一个用于同步的 channel
func (w *list) Register(id uint64) <-chan interface{} {
	w.l.Lock()
	defer w.l.Unlock()
	ch := w.m[id]
	if ch == nil {
		ch = make(chan interface{}, 1)
		w.m[id] = ch
	} else {
		log.Panicf("dup id %x", id)
	}
	return ch
}

// 触发信号
func (w *list) Trigger(id uint64, x interface{}) {
	w.l.Lock()
	ch := w.m[id]
	delete(w.m, id)
	w.l.Unlock()
	if ch != nil {
		ch <- x
		close(ch)
	}
}

// 判断一个 key 是否注册
func (w *list) IsRegistered(id uint64) bool {
	w.l.RLock()
	defer w.l.RUnlock()
	_, ok := w.m[id]
	return ok
}

type waitWithResponse struct {
	ch <-chan interface{}
}

func NewWithResponse(ch <-chan interface{}) Wait {
	return &waitWithResponse{ch: ch}
}

func (w *waitWithResponse) Register(id uint64) <-chan interface{} {
	return w.ch
}
func (w *waitWithResponse) Trigger(id uint64, x interface{}) {}
func (w *waitWithResponse) IsRegistered(id uint64) bool {
	panic("waitWithResponse.IsRegistered() shouldn't be called")
}
