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

package backend

import (
	"bytes"
	"math"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

type BatchTx interface {
	// BatchTx 为读写事务，所以肯定包含了 ReadTx，直接复用代码即可 ;
	ReadTx
	// Unsafe 的意思是必须串行执行，在锁内互斥执行；
	UnsafeCreateBucket(name []byte)
	UnsafePut(bucketName []byte, key []byte, value []byte)
	UnsafeSeqPut(bucketName []byte, key []byte, value []byte)
	UnsafeDelete(bucketName []byte, key []byte)
	// Commit commits a previous tx and begins a new writable one.
	Commit()
	// CommitAndStop commits the previous tx and does not create a new one.
	CommitAndStop()
}

type batchTx struct {
	sync.Mutex          // 互斥保护
	tx         *bolt.Tx // blot 的事务对象
	backend    *backend // 底层封装( 数据真正所在位置 )
	pending    int      // 标识 batchTx 里的 pending 操作
}

func (t *batchTx) Lock() {
	t.Mutex.Lock()
}

func (t *batchTx) Unlock() {
	if t.pending >= t.backend.batchLimit {
		t.commit(false)
	}
	t.Mutex.Unlock()
}

// BatchTx interface embeds ReadTx interface. But RLock() and RUnlock() do not
// have appropriate semantics in BatchTx interface. Therefore should not be called.
// TODO: might want to decouple ReadTx and BatchTx

// 内嵌了 ReadTx ，但是又不想被使用到 RLock, RUnlock 的方法；
func (t *batchTx) RLock() {
	panic("unexpected RLock")
}

func (t *batchTx) RUnlock() {
	panic("unexpected RUnlock")
}

// 创建一个 bucket （必须在锁内，非并发安全）
func (t *batchTx) UnsafeCreateBucket(name []byte) {
	// 调用 bolt 的 CreateBucket
	_, err := t.tx.CreateBucket(name)
	if err != nil && err != bolt.ErrBucketExists {
		if t.backend.lg != nil {
			t.backend.lg.Fatal(
				"failed to create a bucket",
				zap.String("bucket-name", string(name)),
				zap.Error(err),
			)
		} else {
			plog.Fatalf("cannot create bucket %s (%v)", name, err)
		}
	}
	t.pending++
}

// Put 一个 k/v 到 bolt db 里去；
// UnsafePut must be called holding the lock on the tx.
func (t *batchTx) UnsafePut(bucketName []byte, key []byte, value []byte) {
	t.unsafePut(bucketName, key, value, false)
}

// UnsafeSeqPut must be called holding the lock on the tx.
func (t *batchTx) UnsafeSeqPut(bucketName []byte, key []byte, value []byte) {
	t.unsafePut(bucketName, key, value, true)
}

func (t *batchTx) unsafePut(bucketName []byte, key []byte, value []byte, seq bool) {
	// 获取到 bucket
	bucket := t.tx.Bucket(bucketName)
	if bucket == nil {
		if t.backend.lg != nil {
			t.backend.lg.Fatal(
				"failed to find a bucket",
				zap.String("bucket-name", string(bucketName)),
			)
		} else {
			plog.Fatalf("bucket %s does not exist", bucketName)
		}
	}
	if seq {
		// it is useful to increase fill percent when the workloads are mostly append-only.
		// this can delay the page split and reduce space usage.
		bucket.FillPercent = 0.9
	}
	// k/v 数据保存到 bucket 里
	if err := bucket.Put(key, value); err != nil {
		if t.backend.lg != nil {
			t.backend.lg.Fatal(
				"failed to write to a bucket",
				zap.String("bucket-name", string(bucketName)),
				zap.Error(err),
			)
		} else {
			plog.Fatalf("cannot put key into bucket (%v)", err)
		}
	}
	t.pending++
}

// 读数据，range 读
// UnsafeRange must be called holding the lock on the tx.
func (t *batchTx) UnsafeRange(bucketName, key, endKey []byte, limit int64) ([][]byte, [][]byte) {
	bucket := t.tx.Bucket(bucketName)
	if bucket == nil {
		if t.backend.lg != nil {
			t.backend.lg.Fatal(
				"failed to find a bucket",
				zap.String("bucket-name", string(bucketName)),
			)
		} else {
			plog.Fatalf("bucket %s does not exist", bucketName)
		}
	}
	return unsafeRange(bucket.Cursor(), key, endKey, limit)
}

// 调用 bolt range 一个 bucket 里面的 kv
func unsafeRange(c *bolt.Cursor, key, endKey []byte, limit int64) (keys [][]byte, vs [][]byte) {
	if limit <= 0 {
		limit = math.MaxInt64
	}
	var isMatch func(b []byte) bool
	if len(endKey) > 0 {
		isMatch = func(b []byte) bool { return bytes.Compare(b, endKey) < 0 }
	} else {
		isMatch = func(b []byte) bool { return bytes.Equal(b, key) }
		limit = 1
	}

	for ck, cv := c.Seek(key); ck != nil && isMatch(ck); ck, cv = c.Next() {
		vs = append(vs, cv)
		keys = append(keys, ck)
		if limit == int64(len(keys)) {
			break
		}
	}
	return keys, vs
}

// 删除一个 k/v
// UnsafeDelete must be called holding the lock on the tx.
func (t *batchTx) UnsafeDelete(bucketName []byte, key []byte) {
	bucket := t.tx.Bucket(bucketName)
	if bucket == nil {
		if t.backend.lg != nil {
			t.backend.lg.Fatal(
				"failed to find a bucket",
				zap.String("bucket-name", string(bucketName)),
			)
		} else {
			plog.Fatalf("bucket %s does not exist", bucketName)
		}
	}
	err := bucket.Delete(key)
	if err != nil {
		if t.backend.lg != nil {
			t.backend.lg.Fatal(
				"failed to delete a key",
				zap.String("bucket-name", string(bucketName)),
				zap.Error(err),
			)
		} else {
			plog.Fatalf("cannot delete key from bucket (%v)", err)
		}
	}
	t.pending++
}

// 遍历一个 bucket ，bucket 里面存储数据是有序的；
// UnsafeForEach must be called holding the lock on the tx.
func (t *batchTx) UnsafeForEach(bucketName []byte, visitor func(k, v []byte) error) error {
	return unsafeForEach(t.tx, bucketName, visitor)
}

func unsafeForEach(tx *bolt.Tx, bucket []byte, visitor func(k, v []byte) error) error {
	if b := tx.Bucket(bucket); b != nil {
		return b.ForEach(visitor)
	}
	return nil
}

// Commit commits a previous tx and begins a new writable one.
func (t *batchTx) Commit() {
	t.Lock()
	t.commit(false)
	t.Unlock()
}

// CommitAndStop commits the previous tx and does not create a new one.
func (t *batchTx) CommitAndStop() {
	t.Lock()
	t.commit(true)
	t.Unlock()
}

func (t *batchTx) safePending() int {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	return t.pending
}

// 递交一个事务
func (t *batchTx) commit(stop bool) {
	// commit the last tx
	if t.tx != nil {
		if t.pending == 0 && !stop {
			return
		}

		start := time.Now()
		// 递交刷盘
		// gofail: var beforeCommit struct{}
		err := t.tx.Commit()
		// gofail: var afterCommit struct{}

		rebalanceSec.Observe(t.tx.Stats().RebalanceTime.Seconds())
		spillSec.Observe(t.tx.Stats().SpillTime.Seconds())
		writeSec.Observe(t.tx.Stats().WriteTime.Seconds())
		commitSec.Observe(time.Since(start).Seconds())
		// 累计计数 commit 的总次数
		atomic.AddInt64(&t.backend.commits, 1)

		t.pending = 0
		if err != nil {
			if t.backend.lg != nil {
				t.backend.lg.Fatal("failed to commit tx", zap.Error(err))
			} else {
				plog.Fatalf("cannot commit tx (%s)", err)
			}
		}
	}
	// 如果 stop 为 true，那么说明不需要再开启一个新事务了；
	if !stop {
		// 获取一个新的事务对象； 开始的时候就会调用到这里来；
		// 调用栈：
		// New -> newBackend
		// -> newBatchTxBuffered
		// -> tx.Commit
		// -> t.commit(false)
		// -> t.unsafeCommit(false)
		// -> t.batchTx.commit(false)
		// -> t.tx 赋值
		t.tx = t.backend.begin(true)
	}
}

type batchTxBuffered struct {
	// 底层的封装
	batchTx
	buf txWriteBuffer
}

// 创建一个读写事务
func newBatchTxBuffered(backend *backend) *batchTxBuffered {
	tx := &batchTxBuffered{
		batchTx: batchTx{backend: backend},
		buf: txWriteBuffer{
			txBuffer: txBuffer{make(map[string]*bucketBuffer)},
			seq:      true,
		},
	}
	tx.Commit()
	return tx
}

func (t *batchTxBuffered) Unlock() {
	if t.pending != 0 {
		t.backend.readTx.Lock() // blocks txReadBuffer for writing.
		t.buf.writeback(&t.backend.readTx.buf)
		t.backend.readTx.Unlock()
		if t.pending >= t.backend.batchLimit {
			t.commit(false)
		}
	}
	t.batchTx.Unlock()
}

func (t *batchTxBuffered) Commit() {
	t.Lock()
	t.commit(false)
	t.Unlock()
}

func (t *batchTxBuffered) CommitAndStop() {
	t.Lock()
	t.commit(true)
	t.Unlock()
}

//
func (t *batchTxBuffered) commit(stop bool) {
	// all read txs must be closed to acquire boltdb commit rwlock
	t.backend.readTx.Lock()
	t.unsafeCommit(stop)
	t.backend.readTx.Unlock()
}

func (t *batchTxBuffered) unsafeCommit(stop bool) {
	// 如果有读事务
	if t.backend.readTx.tx != nil {
		// wait all store read transactions using the current boltdb tx to finish,
		// then close the boltdb tx
		go func(tx *bolt.Tx, wg *sync.WaitGroup) {
			// 等待所有的并发读事务完成
			wg.Wait()
			if err := tx.Rollback(); err != nil {
				if t.backend.lg != nil {
					t.backend.lg.Fatal("failed to rollback tx", zap.Error(err))
				} else {
					plog.Fatalf("cannot rollback tx (%s)", err)
				}
			}
		}(t.backend.readTx.tx, t.backend.readTx.txWg)
		t.backend.readTx.reset()
	}

	// 递交事务，并赋值新的 batchTx.tx
	t.batchTx.commit(stop)

	if !stop {
		// 赋值新的 ReadTx.tx
		t.backend.readTx.tx = t.backend.begin(false)
	}
}

// batchTxBuffered 本身就是对原始的 batchTx 的一层封装，保存一份数据到 buffer
func (t *batchTxBuffered) UnsafePut(bucketName []byte, key []byte, value []byte) {
	// 数据保存到 bolt db
	t.batchTx.UnsafePut(bucketName, key, value)
	// 数据保存到 buffer
	t.buf.put(bucketName, key, value)
}

func (t *batchTxBuffered) UnsafeSeqPut(bucketName []byte, key []byte, value []byte) {
	t.batchTx.UnsafeSeqPut(bucketName, key, value)
	t.buf.putSeq(bucketName, key, value)
}
