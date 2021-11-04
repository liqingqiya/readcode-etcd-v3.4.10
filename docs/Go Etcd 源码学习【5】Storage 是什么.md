[toc]

raft.Node 是纯状态机的实现。
raft.MemoryStorage 是存储的实现。
wal.WAL 是持久化 log 的实现。

这三个是业务 raftNode 组装功能的核心。

## Storage 

这是一个重要的 interface ，是抽象出来，用于检索 log entries 的存储器。

Storage 任何方法的报错，都会导致 raft 实例停服，并且拒绝参加选举。业务应用来负责清理和恢复这个场景。

```go
type Storage interface {
    // 返回保存的 hardstate 信息
    InitialState() (pb.HardState, pb.ConfState, error)
    // 返回指定区域的日志记录
    Entries(lo, hi, maxSize uint64) ([]pb.Entry, error)
    // 返回第 i 个条目的日志记录
    Term(i uint64) (uint64, error)
    // 最后一条日志的 idx
    LastIndex() (uint64, error)
    // 最前一条日志的 idx
    FirstIndex() (uint64, error)
    // 返回存储器的快照
    Snapshot() (pb.Snapshot, error)
}
```

而  raft.MemoryStorage 是 Storage interface 的一种实现而已。在 raftexmaple 中，直接使用了 raft.MemoryStorage 作为 Storage  存储器 ，当然 Storage 也可以由业务自己实现，只要实现对应的接口，从底层存储介质中取回日志对象（log entries）。

### raft.MemoryStorage

这个是 Storage 的具体实现，我们看下这个实现的方法，借以感受下这个的作用。

来看下 MemoryStorage 的结构：

```go
type MemoryStorage struct {
    sync.Mutex
    hardState pb.HardState
    snapshot  pb.Snapshot
    ents []pb.Entry
}
```

能看到，最关键的其实就是一个 Entry 数组。

#### InitialState

```go
// InitialState implements the Storage interface.
func (ms *MemoryStorage) InitialState() (pb.HardState, pb.ConfState, error) {
    return ms.hardState, ms.snapshot.Metadata.ConfState, nil
}
```

而这个 ms.hardState 则是在 raftexample/raft.go 文件，raftNode.replayWAL 函数中，调用 SetHardState 方法进行赋值。

```go
func (rc *raftNode) replayWAL() *wal.WAL {
    // 从 wal 恢复了 hardstate
    w := rc.openWAL(snapshot)
    _, st, ents, err := w.ReadAll()

    rc.raftStorage.SetHardState(st)
}
```

#### Entries

这个方法用于获取 [ lo, hi ] 这一个区间的日志记录。

```go
func (ms *MemoryStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
    // 获取到第一个日志的 index
    offset := ms.ents[0].Index
    if lo <= offset {
        // 这种情况，属于有部分的日志已经被截断了
        return nil, ErrCompacted
    }
    if hi > ms.lastIndex()+1 {
        // 越界了，panic
    }
    // 最开始的是一个 dummy entry，如果只有这一个，那么说明是空的；
    if len(ms.ents) == 1 {
        return nil, ErrUnavailable
    }
    // lo - offset ，hi - offset 是数组内偏移
    ents := ms.ents[lo-offset : hi-offset]
    return limitSize(ents, maxSize), nil
}
```

那么对于 MemoryStorage 来说，ms.ents 又是哪里来的呢？

对于 raftexample 来说，是在 raftNode.replayWAL 中，调用 Append 置入的。

```go
func (rc *raftNode) replayWAL() *wal.WAL {
    // 从 wal 恢复的日志，全部设置到 MemoryStorage 中
    rc.raftStorage.Append(ents)
}
```

#### Term

```go
func (ms *MemoryStorage) Term(i uint64) (uint64, error) {
    // 第一个 log 的 index
    offset := ms.ents[0].Index
    // 想要获取的是被截断的情况
    if i < offset {
        return 0, ErrCompacted
    }
    // 越界的情况
    if int(i-offset) >= len(ms.ents) {
        return 0, ErrUnavailable
    }
    // 获取到指定 index 的 log entry
    return ms.ents[i-offset].Term, nil
}
```

可以看到，这个就是获取到指定位置的 log entry ，需要处理截断和越界的两种情况。

#### LastIndex

返回最后一个 日志的 index 。

```go
func (ms *MemoryStorage) LastIndex() (uint64, error) {
    return ms.ents[0].Index + uint64(len(ms.ents)) - 1
}
```

思考：有这种计算方式，是不是代表 index 一定是连续的？

#### FirstIndex

返回第一个日志的 index 。

```go
func (ms *MemoryStorage) FirstIndex() (uint64, error) {
    return ms.ents[0].Index + 1
}
```

#### Snapshot

```go
func (ms *MemoryStorage) Snapshot() (pb.Snapshot, error) {
    return ms.snapshot, nil
}
```

这个主要是返回 MemoryStorage 的 snapshot ，而这个 snapshot 也是在 replayWAL 里面设置上的。

```go
func (rc *raftNode) replayWAL() *wal.WAL {
    // 从 wal 中恢复出来的 snapshot
    
    if snapshot != nil {
        rc.raftStorage.ApplySnapshot(*snapshot)
    }
}
```

#### 小结下 

小结下 MemoryStorage 的功能，这是一个日志全在内存里，实现了日志索引的功能的一个存储。

Storage 是给 raft 状态机用的，是 raft 状态机内部的 raftlog 。

我们知道 raft 算法中，log 是最重要的核心之一。raft 算法三大核心：

1. leader 选举；
2. 日志复制；
3. 选举的正确性保证；

这个日志在 etcd 的 raft 核心模块里则非常巧妙的抽象了出来。

etcd 把 raft 算法和 log 完全拆开，日志抽象成了一个 Storage 的接口，而算法则只对这几个特定语义的接口进行逻辑判断，合力工作则能保证数据的一致性。

这样保证了 raft 算法的最小化，有可以让业务自定义日志的具体实现，很灵活。

Ready 里面的 commit entries 将从这个里面出来。

对于  raft 状态机来讲，我们就可以认为 Storage 的数据是已经持久化了的（这个也是业务要保证的语义），那么在 raft 算法内部就可以完全按照一致性算法来判断。

同学们可能会疑惑？

Storage 如果是 MemoryStorage 的话，明明是个内存数据，怎么说是持久化了的呢？业务怎么来保证？

其实在 etcd 中实现很简单，所有 log entries 添加到 Storage 之前，一定要走 wal 先持久化。