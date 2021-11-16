[toc]

今天稍微看下 raft.node ，也就是最核心的状态机部分。

# Node 接口

```go
type Node interface {
    // 步进时钟（由业务触发）
    Tick()
    // 使自身状态变成候选人，然后竞争 leader
    Campaign(ctx context.Context) error
    // 提案递交；提案可能丢失，需要业务自己保证重试
    Propose(ctx context.Context, data []byte) error
    // 配置变更的提案
    ProposeConfChange(ctx context.Context, cc pb.ConfChangeI) error
    // 当节点收到其他节点发过来的 message ，主动调用驱动 Raft
    Step(ctx context.Context, msg pb.Message) error
    // Ready 并不单一指持久化了，而且存在各种 ready 待处理状态的；
    Ready() <-chan Ready
    // 告诉 Raft 已经处理完了 ready，开始状态机的推进
    Advance()
    // 应用配置变更
    ApplyConfChange(cc pb.ConfChangeI) *pb.ConfState
    // 自愿转让 leader 身份
    TransferLeadership(ctx context.Context, lead, transferee uint64)
    // 线性一致性读
    ReadIndex(ctx context.Context, rctx []byte) error
    // 当前状态机的状态
    Status() Status
    ReportUnreachable(id uint64)
    ReportSnapshot(id uint64, status SnapshotStatus)
    Stop()
}
```

## ReadIndex

请求一个 read state 。这个 read state 将被设置到 ready 结构体，read state 有一个 read index 。一旦业务应用**步进**到 read index 之后，前面的都是可以安全的读取的。read state 会被赋值为相同的 rctx 。

线性一致性读，只要是写返回成功的，那么读的时候一定要是最新的数据，不能是旧的。

当收到读请求的时候，先发出一条 readindex 消息（记录当前的 index ），确认自己是否是 leader ，并且确认业务 apply index 是否超过当前 index 。只要满足这一点，那么就一定能读到一致性的数据。

除非同 key 有读写并发，但是并发这种场景本来就是未定义的。

不一致怎么理解？

1. leader 节点和 follower 节点存在状态差，不能保证任何时刻，二者的日志完全相同，follower 完全由可能落后于 leader 。
2. 如果限制只能从 leader 节点读其实也有问题，因为网络分区之后，可能选举出新的 leader ，而旧的 leader 可能还不自知，如果从旧 leader 上返回了数据，那么可能就是脏数据；

解决一致性读 readindex 做的两个事情：

1. leader 需要和集群中多数节点通信，以确保自己仍然是合法的 leader ；
2. 等待状态机应用 readindex 记录之前的所有日志。这个也是必须的，因为虽然所有的日志保持了一致性，但是不同的节点 apply 应用进度可能是不一样的，只要保证了这一点，那么再去读数据，那么时序上一定是一致的；

## Propose

## Step

## Advance

# node 实现

node 是 Node 接口的具体实现。

在上一篇网络篇提到，网络中来的数据会调用 raftNode.Process( m ) 处理，而这函数里面则是直接调用 node.Step( ctx, m ) 来处理。

也就是说，大写的 node.Step 就是网络入口进来的， 这个里面只处理网络消息。

```go
func (n *node) Step(ctx context.Context, m pb.Message) error {
    // 非网络消息，则直接过滤
    if IsLocalMsg(m.Type) {
        // TODO: return an error?
        return nil
    }
    return n.step(ctx, m)
}
```

接下来看 node.step 这个接口，这个接口其实是对 node.stepWithWaitOption 的封装，网络过来的走异步化。

另外有一个 node.stepWait 方法，这个方法也是 node.stepWithWaitOption 的封装，只不过是同步的。而调用 node.stepWait 的只有 node.Propose ，可以理解，Propose 肯定要状态机接收才能返回嘛。


## stepWithWaitOption

## run

这个是 node 实例封装的最重要的方法。是对 raft 实例的一层封装。



# 思考问题

## 什么是线性一致性读？

光是数据的一致性还不够，如果分区脑裂了，这个时候如果请求发给了老的 leader ，而他也不自知，那么就可能读到脏数据。这个时候就要保证这个一致性了。

怎么保证？

etcd 默认使用 ReadIndex 的机制来实现线性一致性读。其原理就是 etcd 在处理读请求的时候，那么它必须先要向 leader 查询 commit index ，此时少数派联系不上 leader，所以必然会失败。