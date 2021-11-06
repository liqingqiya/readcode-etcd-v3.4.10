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

package backend

import (
	"bytes"
	"sort"
)

// txBuffer handles functionality shared between txWriteBuffer and txReadBuffer.
type txBuffer struct {
	buckets map[string]*bucketBuffer
}

// 清空掉 buffer 里面的 kv
func (txb *txBuffer) reset() {
	for k, v := range txb.buckets {
		if v.used == 0 {
			// demote
			delete(txb.buckets, k)
		}
		v.used = 0
	}
}

// 对写请求的 buffer 封装
// txWriteBuffer buffers writes of pending updates that have not yet committed.
type txWriteBuffer struct {
	txBuffer
	seq bool
}

func (txw *txWriteBuffer) put(bucket, k, v []byte) {
	txw.seq = false
	txw.putSeq(bucket, k, v)
}

func (txw *txWriteBuffer) putSeq(bucket, k, v []byte) {
	b, ok := txw.buckets[string(bucket)]
	if !ok {
		// 没有找到这个 bucket 对应的 buffer 结构，那么就 new 一个；
		b = newBucketBuffer()
		txw.buckets[string(bucket)] = b
	}
	// 把 kv 添加进 buffer
	b.add(k, v)
}

// 把 txWriteBuffer 回写到 txReadBuffer 里面
func (txw *txWriteBuffer) writeback(txr *txReadBuffer) {
	for k, wb := range txw.buckets {
		rb, ok := txr.buckets[k]
		if !ok {
			delete(txw.buckets, k)
			txr.buckets[k] = wb
			continue
		}
		if !txw.seq && wb.used > 1 {
			// assume no duplicate keys
			sort.Sort(wb)
		}
		rb.merge(wb)
	}
	// 清空 txw
	txw.reset()
}

// 访问的 buffer 更新
// txReadBuffer accesses buffered updates.
type txReadBuffer struct{ txBuffer }

// 查找 bucket 里面从 [ key, endKey ]的 kv
func (txr *txReadBuffer) Range(bucketName, key, endKey []byte, limit int64) ([][]byte, [][]byte) {
	if b := txr.buckets[string(bucketName)]; b != nil {
		return b.Range(key, endKey, limit)
	}
	return nil, nil
}

// 遍历执行 bucket 里面所有的 kv
func (txr *txReadBuffer) ForEach(bucketName []byte, visitor func(k, v []byte) error) error {
	if b := txr.buckets[string(bucketName)]; b != nil {
		return b.ForEach(visitor)
	}
	return nil
}

// 返回 txReadBuffer 的副本
// unsafeCopy returns a copy of txReadBuffer, caller should acquire backend.readTx.RLock()
func (txr *txReadBuffer) unsafeCopy() txReadBuffer {
	txrCopy := txReadBuffer{
		txBuffer: txBuffer{
			buckets: make(map[string]*bucketBuffer, len(txr.txBuffer.buckets)),
		},
	}
	for bucketName, bucket := range txr.txBuffer.buckets {
		txrCopy.txBuffer.buckets[bucketName] = bucket.Copy()
	}
	return txrCopy
}

type kv struct {
	key []byte
	val []byte
}

// bucketBuffer buffers key-value pairs that are pending commit.
type bucketBuffer struct {
	// buffer 的 kv
	buf []kv
	// used tracks number of elements in use so buf can be reused without reallocation.
	// 使用的位置游标
	used int
}

// 新建一个 bucketBuffer 结构，默认数组大小 512 长度
func newBucketBuffer() *bucketBuffer {
	return &bucketBuffer{buf: make([]kv, 512), used: 0}
}

// 获取一段有序范围内的 kv
// key 指明开始的位置，endKey 指明结束的位置，limit 指明总个数
func (bb *bucketBuffer) Range(key, endKey []byte, limit int64) (keys [][]byte, vals [][]byte) {
	f := func(i int) bool { return bytes.Compare(bb.buf[i].key, key) >= 0 }
	idx := sort.Search(bb.used, f)
	if idx < 0 {
		return nil, nil
	}
	if len(endKey) == 0 {
		if bytes.Equal(key, bb.buf[idx].key) {
			keys = append(keys, bb.buf[idx].key)
			vals = append(vals, bb.buf[idx].val)
		}
		return keys, vals
	}
	if bytes.Compare(endKey, bb.buf[idx].key) <= 0 {
		return nil, nil
	}
	for i := idx; i < bb.used && int64(len(keys)) < limit; i++ {
		if bytes.Compare(endKey, bb.buf[i].key) <= 0 {
			break
		}
		keys = append(keys, bb.buf[i].key)
		vals = append(vals, bb.buf[i].val)
	}
	return keys, vals
}

// 遍历 bucketBuffer 里面存储的 kv
func (bb *bucketBuffer) ForEach(visitor func(k, v []byte) error) error {
	for i := 0; i < bb.used; i++ {
		if err := visitor(bb.buf[i].key, bb.buf[i].val); err != nil {
			return err
		}
	}
	return nil
}

func (bb *bucketBuffer) add(k, v []byte) {
	bb.buf[bb.used].key, bb.buf[bb.used].val = k, v
	bb.used++
	if bb.used == len(bb.buf) {
		// 如果满了，那就扩容
		buf := make([]kv, (3*len(bb.buf))/2)
		copy(buf, bb.buf)
		bb.buf = buf
	}
}

// 合并两个 bucketBuffer
// merge merges data from bb into bbsrc.
func (bb *bucketBuffer) merge(bbsrc *bucketBuffer) {
	for i := 0; i < bbsrc.used; i++ {
		bb.add(bbsrc.buf[i].key, bbsrc.buf[i].val)
	}
	if bb.used == bbsrc.used {
		return
	}
	if bytes.Compare(bb.buf[(bb.used-bbsrc.used)-1].key, bbsrc.buf[0].key) < 0 {
		return
	}

	sort.Stable(bb)

	// remove duplicates, using only newest update
	widx := 0
	for ridx := 1; ridx < bb.used; ridx++ {
		if !bytes.Equal(bb.buf[ridx].key, bb.buf[widx].key) {
			widx++
		}
		bb.buf[widx] = bb.buf[ridx]
	}
	bb.used = widx + 1
}

// 长度以 used 为准
func (bb *bucketBuffer) Len() int { return bb.used }

// 比较以 key 为准
func (bb *bucketBuffer) Less(i, j int) bool {
	return bytes.Compare(bb.buf[i].key, bb.buf[j].key) < 0
}
func (bb *bucketBuffer) Swap(i, j int) { bb.buf[i], bb.buf[j] = bb.buf[j], bb.buf[i] }

func (bb *bucketBuffer) Copy() *bucketBuffer {
	bbCopy := bucketBuffer{
		buf:  make([]kv, len(bb.buf)),
		used: bb.used,
	}
	copy(bbCopy.buf, bb.buf)
	return &bbCopy
}
