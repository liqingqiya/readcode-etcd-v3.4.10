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

package schedule

import (
	"context"
	"sync"
)

type Job func(context.Context)

// Scheduler can schedule jobs.
type Scheduler interface {
	// Schedule asks the scheduler to schedule a job defined by the given func.
	// Schedule to a stopped scheduler might panic.
	Schedule(j Job)

	// Pending returns number of pending jobs
	Pending() int

	// Scheduled returns the number of scheduled jobs (excluding pending jobs)
	Scheduled() int

	// Finished returns the number of finished jobs
	Finished() int

	// WaitFinish waits until at least n job are finished and all pending jobs are finished.
	WaitFinish(n int)

	// Stop stops the scheduler.
	Stop()
}

type fifo struct {
	mu sync.Mutex

	resume    chan struct{}
	scheduled int
	finished  int
	pendings  []Job

	ctx    context.Context
	cancel context.CancelFunc

	finishCond *sync.Cond
	donec      chan struct{}
}

// NewFIFOScheduler returns a Scheduler that schedules jobs in FIFO
// order sequentially
func NewFIFOScheduler() Scheduler {
	f := &fifo{
		resume: make(chan struct{}, 1),
		donec:  make(chan struct{}, 1),
	}
	f.finishCond = sync.NewCond(&f.mu)
	f.ctx, f.cancel = context.WithCancel(context.Background())

	// 后台协程任务调度
	go f.run()
	return f
}

// Schedule schedules a job that will be ran in FIFO order sequentially.
func (f *fifo) Schedule(j Job) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cancel == nil {
		panic("schedule: schedule to stopped scheduler")
	}

	if len(f.pendings) == 0 {
		select {
		// 如果之前没有在运行，那么唤醒；
		case f.resume <- struct{}{}:
		default:
		}
	}
	// 添加到任务队列
	f.pendings = append(f.pendings, j)
}

func (f *fifo) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pendings)
}

func (f *fifo) Scheduled() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scheduled
}

func (f *fifo) Finished() int {
	f.finishCond.L.Lock()
	defer f.finishCond.L.Unlock()
	return f.finished
}

// 当前并没有人用到这个接口
func (f *fifo) WaitFinish(n int) {
	f.finishCond.L.Lock()
	for f.finished < n || len(f.pendings) != 0 {
		// 等待请求完成
		f.finishCond.Wait()
	}
	f.finishCond.L.Unlock()
}

// Stop stops the scheduler and cancels all pending jobs.
func (f *fifo) Stop() {
	f.mu.Lock()
	f.cancel()
	f.cancel = nil
	f.mu.Unlock()
	<-f.donec
}

/*
从实现来看，这个仅仅是一个最简单的线性任务执行的 goroutine ，一个一个取，一个一个任务执行；
纯串行执行
*/
func (f *fifo) run() {
	// TODO: recover from job panic?
	defer func() {
		close(f.donec)
		close(f.resume)
	}()

	for {
		var todo Job
		f.mu.Lock()
		if len(f.pendings) != 0 {
			// 调度次数加一
			f.scheduled++
			// 从任务队列中取一个任务出来
			todo = f.pendings[0]
		}
		f.mu.Unlock()
		if todo == nil {
			select {
			// 如果没有任务就等待
			case <-f.resume:
			// 任务结束
			case <-f.ctx.Done():
				f.mu.Lock()
				pendings := f.pendings
				f.pendings = nil
				f.mu.Unlock()
				// clean up pending jobs
				for _, todo := range pendings {
					todo(f.ctx)
				}
				return
			}
		} else {
			// 执行任务
			todo(f.ctx)
			f.finishCond.L.Lock()
			// 执行完了，加 finished 计数
			f.finished++
			// 删除执行过的任务
			f.pendings = f.pendings[1:]
			// 知会等待的节点唤醒
			f.finishCond.Broadcast()
			f.finishCond.L.Unlock()
		}
	}
}
