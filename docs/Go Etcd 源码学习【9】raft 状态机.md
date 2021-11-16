[toc]

# raft

定义在 etcd/raft/raft.go 。

```go
type raft struct {
    // 节点 id 编号
    id uint64
    // 任期编号
    Term uint64
    // 选举了谁当 leader（投票给了谁）
    Vote uint64
    readStates []ReadState
    // the log
    // 日志（未持久化 + 持久化都有）
    raftLog *raftLog
    maxMsgSize         uint64
    maxUncommittedSize uint64
    // 其他 follower 的复制进度表
    prs tracker.ProgressTracker
    state StateType
    isLearner bool
    msgs []pb.Message
    // the leader id
    lead uint64
    leadTransferee uint64
    pendingConfIndex uint64
    // 标识还没有 commit 的一个量，计量数据
    uncommittedSize uint64
    readOnly *readOnly
    // 如果这个数值超过一个值，那么就说明和 leader 失联了，就可以去净增选举了
    // 只要由 leader 消息过来（心跳，或者复制），这个值就会被清零；
    electionElapsed int
    heartbeatElapsed int
    checkQuorum bool
    preVote     bool
    // leader 与 follower 之间的心跳超时时间
    heartbeatTimeout int
    electionTimeout  int
    randomizedElectionTimeout int
    disableProposalForwarding bool
    tick func()
    step stepFunc
    logger Logger
}
```

这个是最核心的 raft 算法所在地。

## newRaft

创建这个状态机，每个创建出来的 raft 实例都是从 follower 角色开始。

```go
func newRaft(c *Config) *raft {

    r := &raft{
        id:                        c.ID,
        lead:                      None,
        isLearner:                 false,
        raftLog:                   raftlog,
        //
    }
    
    // 恢复集群配置
    cfg, prs, err := confchange.Restore(confchange.Changer{
        Tracker:   r.prs,
        LastIndex: raftlog.lastIndex(),
    }, cs)
    
    // 从 follower 角色开始
    r.becomeFollower(r.Term, None)
}
```

## becomFollower

```go

func (r *raft) becomeFollower(term uint64, lead uint64) {
    // 这个很重要
    r.step = stepFollower
    r.reset(term)
    // 这个很重要
    r.tick = r.tickElection
    r.lead = lead
    r.state = StateFollower
}
```

成为 follower 角色，tick 定时器是 r.tickElection ，对 leader 角色虎视眈眈。只要超时立马参与竞选，只有 leader 不断地告知它，才能不断的打消它的想法。

那么刚开始所有人都是 follower 的时候，就看谁是第一个超时并发起选举的人。

其实，除了 leader 之外，其他所有角色的 tick 都是 tickElection 。

## becomeLeader

选举成功则称为 leader 角色，称为 leader ，leader 的处理逻辑则和 follower 大不相同。

```go
func (r *raft) becomeLeader() {
	if r.state == StateFollower {
		panic("invalid transition [follower -> leader]")
	}
    // 关键操作：leader 的状态机处理逻辑是 stepLeader
	r.step = stepLeader
	r.reset(r.Term)
    // 关键操作：设置 leader 的定时操作，就是不断的发心跳消息给 follower ，提醒他们不要叛变；
	r.tick = r.tickHeartbeat
	r.lead = r.id
	r.state = StateLeader
    // 关键操作：leader 节点对应的 progress 状态变为 replicate 
	r.prs.Progress[r.id].BecomeReplicate()

    // pendingConfIndex 取最后一个 index
	r.pendingConfIndex = r.raftLog.lastIndex()

    // 添加一条空白消息，会消耗一个 index
	emptyEnt := pb.Entry{Data: nil}
	if !r.appendEntry(emptyEnt) {
		// This won't happen because we just called reset() above.
		r.logger.Panic("empty entry was dropped")
	}
}
```

以上有几个关键操作：

1. 设置 step 逻辑为 stepLeader ；
    1. 这个相比 stepFollower 要复杂的多；
2. 设置 tick 为 tickHeartbeat ；
    1. leader 的 tick 操作最关键的是给其他节点发送心跳
3. 日志复制状态变为 replicate ；

## becomeCandidate

这个是超时选举之后，变成候选者。

1. 设置 step 为 stepCandidate
2. 设置 tick 为 tickElection
3. 任期加 1

## becomePreCandidate

1. 设置 step 为 stepCandidate
2. 设置 tick 为 tickElection
3. 任期不变

## stepLeader

这个是 leader 角色的处理逻辑，也是最复杂的逻辑，既要向其他节点发送消息（比如日志复制），又要处理各种 response 消息。

## stepCandidate

候选者收到消息的处理逻辑：

1. prevote 和 vote 两个消息的逻辑处理都在这
2. 收到 MsgProp 消息直接丢掉（如果是 follower 角色，那么跟根据配置判断，是否转发 leader 处理）
3. 收到 leader 发过来的消息，那么说明大局已定，要退觉 follower ；
    1. 收到 MsgApp 消息会导致重新变成 follower 角色；
    2. 收到 MsgHeartbeat 消息，说明 leader 在发送心跳；
    3. 收到 MsgSnap 消息，说明 leader 在发送 snap 消息；
4. 收到 prevote，vote 的结果消息，那么就看唱票的结果；
    1. 如果是 prevote 成功，那么说明自己具备称为 leader 的资格，可以增加自己的任期正是发起选举了；
    2. 如果是 vote 成功，那么直接就变成 leader 角色；


```go
func stepCandidate(r *raft, m pb.Message) error {
    // prevote 和 vote 两个消息的逻辑处理都在这
	var myVoteRespType pb.MessageType
	if r.state == StatePreCandidate {
		myVoteRespType = pb.MsgPreVoteResp
	} else {
		myVoteRespType = pb.MsgVoteResp
	}
    
	switch m.Type {
    // 直接丢弃
	case pb.MsgProp:
		return ErrProposalDropped
	// 日志复制消息，则会强迫角色变更，接受日志的数据
    case pb.MsgApp:
		r.becomeFollower(m.Term, m.From) // always m.Term == r.Term
		r.handleAppendEntries(m)
    // 收到 leader 发过来的心跳警告，那么也会导致它老实
	case pb.MsgHeartbeat:
		r.becomeFollower(m.Term, m.From) // always m.Term == r.Term
		r.handleHeartbeat(m)
    // 快照消息，也是 leader 发过来的
	case pb.MsgSnap:
		r.becomeFollower(m.Term, m.From) // always m.Term == r.Term
		r.handleSnapshot(m)
	// 投票的结果回来了（ vote 或者 prevote ）
	case myVoteRespType:
        // 唱票
		gr, rj, res := r.poll(m.From, m.Type, !m.Reject)
		switch res {
		case quorum.VoteWon:
			// 获得了大多数的认可，觉得自己能赢，那么才会开启选举
			if r.state == StatePreCandidate {
				// 如果是预投的情况，那么现在基本就稳了，就可以正式开始拉票选举
				r.campaign(campaignElection)
			} else {
				// 如果是投票的场景，那么这就成功了，变成 leader
				r.becomeLeader()
				r.bcastAppend()
			}
		case quorum.VoteLost:
           // 事实证明得不到多数人的支持，那么就退居 follower
			r.becomeFollower(r.Term, None)
		}
	case pb.MsgTimeoutNow:
	}
	return nil
}
```

## stepFollower

follower 角色处理消息的逻辑：

```go
func stepFollower(r *raft, m pb.Message) error {
	switch m.Type {
    // 这类消息只有两种处理：要么丢弃，要么转发给 leader
	case pb.MsgProp:
		if r.lead == None {
			// 如果当前还没有选主出来，那么就 drop 掉这个请求，并返回对应错误码
			return ErrProposalDropped
		} else if r.disableProposalForwarding {
            // 如果没有开启转发，那么就丢弃
			return ErrProposalDropped
		}
		// 转发给 leader，消息类型不变，还是 MsgProp
		m.To = r.lead
		// 投入队列中，这个队列就是已经准备好可以发往 peer 的消息，之后通过 Ready 输出，由状态机外层发出；
		r.send(m)
	case pb.MsgApp:
        // 收到 leader 的消息了，就不要想着再去竞争选举了。发 MsgApp 的一定是 leader
        // ...
	case pb.MsgHeartbeat:
		// 收到 leader 的消息了，就不要想着再去竞争选举了
		// ...
	case pb.MsgSnap:
		// 收到 leader 的消息了，就不要想着再去竞争选举了
		// ...
	case pb.MsgTransferLeader:
		if r.lead == None {
			return nil
		}
		m.To = r.lead
		r.send(m)
	case pb.MsgTimeoutNow:
		if r.promotable() {
			r.campaign(campaignTransfer)
		} else {
		}
	case pb.MsgReadIndex:
		if r.lead == None {
			return nil
		}
		m.To = r.lead
		r.send(m)
	case pb.MsgReadIndexResp:
		if len(m.Entries) != 1 {
			return nil
		}
	}
	return nil
}
```

# 思考问题

程序刚启动的时候，ConfChangeAddNode 事件是哪里来的？好像是本地发起的？不是网络传递过来的消息。

好像就是从 wal 日志里读出来的，bootstrap 添加了这个配置变更。

搞懂了，就是在 confchange 日志就是在 wal 里，因为根本就没有持久化 applyindex 呀。所以全部要应用一把。

对于那些已经 commit 但是还没有 apply 的日志，就会在系统起来的时候 apply 一把。

所以一般业务会负责记录下 applyindex 的值，不然初始化加载的速度就很慢。因为要解析完所有的日志。比如你可以把它存到本地文件，或者 mongodb 之类的。

其实这个也不是必须要这样，只是 raftexample 偷了个懒。如果这个日志被 compact 掉了，其实也不会有这个。

疑问：但是 etcd server 好像没有持久化这个？


