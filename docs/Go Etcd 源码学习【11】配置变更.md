[toc]

# api 层递交

```go
func (h *httpKVAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	case r.Method == "POST":
       // 把请求包读出来
		url, err := ioutil.ReadAll(r.Body)
		if err != nil {
			// ...
		}
       // 解析出 id
		nodeId, err := strconv.ParseUint(key[1:], 0, 64)
		if err != nil {
			// ...
		}
       // 构造一个 confchange 的消息
		cc := raftpb.ConfChange{
			Type:    raftpb.ConfChangeAddNode,
			NodeID:  nodeId,
			Context: url,
		}
       // 投递 channal 处理，将由 raftNode 异步处理
		h.confChangeC <- cc
} 
```

消息类型为 ConfChange，初始化为：

```go
cc := raftpb.ConfChange{
    Type:    raftpb.ConfChangeAddNode,
    NodeID:  nodeId,
    Context: url,
}
```

# raftNode 异步处理

```go
func (rc *raftNode) serveChannels() {

	go func() {
		for rc.proposeC != nil && rc.confChangeC != nil {
			select {
            // ...
			case cc, ok := <-rc.confChangeC:
				if !ok {
					rc.confChangeC = nil
				} else {
					// 递交配置变更信息，消息转接到 node
					rc.node.ProposeConfChange(context.TODO(), cc)
				}
			}
		}
	}()
    
}
```

# node 递交状态机

在 ProposeConfChange 方法中，把数据封装成 message 格式。

```go
func (n *node) ProposeConfChange(ctx context.Context, cc pb.ConfChangeI) error {
	msg, err := confChangeToMsg(cc)
	if err != nil {
		return err
	}
	return n.Step(ctx, msg)
}

// 构造 message
func confChangeToMsg(c pb.ConfChangeI) (pb.Message, error) {
	typ, data, err := pb.MarshalConfChange(c)
	if err != nil {
		return pb.Message{}, err
	}
	return pb.Message{Type: pb.MsgProp, Entries: []pb.Entry{{Type: typ, Data: data}}}, nil
}

// 配置结构体的序列化
func MarshalConfChange(c ConfChangeI) (EntryType, []byte, error) {
	var typ EntryType
	var ccdata []byte
	var err error
	if ccv1, ok := c.AsV1(); ok {
		typ = EntryConfChange
		ccdata, err = ccv1.Marshal()
	} else {
		ccv2 := c.AsV2()
		typ = EntryConfChangeV2
		ccdata, err = ccv2.Marshal()
	}
	return typ, ccdata, err
}
```

message 的消息类型为 MsgProp ，所以可想而知 commit 的流程跟“写”请求的递交流程没啥两样。commit 完之后，apply 的流程大有不同。

c 的类型为 ConfChange ，这个是定制了 AsV1，AsV2 两种方法的：

```go
func (c ConfChange) AsV2() ConfChangeV2 {
	return ConfChangeV2{
		Changes: []ConfChangeSingle{{
			Type:   c.Type,
			NodeID: c.NodeID,
		}},
		Context: c.Context,
	}
}

func (c ConfChange) AsV1() (ConfChange, bool) {
	return c, true
}
```

对于 ConfChange 只要调用了 AsV1 方法，那就是返回本身，布尔值为 true ，所以序列化 confChangeToMsg 结果自然是：

```go
pb.Message{Type: pb.MsgProp, Entries: []pb.Entry{{Type: EntryConfChange, Data: data}}}
```

接下来的流程和写流程类似，只有在 stepLeader 里面有稍微的处理：

```go
func stepLeader(r *raft, m pb.Message) error {
	switch m.Type {
	case pb.MsgProp:
		for i := range m.Entries {
			e := &m.Entries[i]
			var cc pb.ConfChangeI
			// 配置变更
			if e.Type == pb.EntryConfChange {
				var ccc pb.ConfChange
				if err := ccc.Unmarshal(e.Data); err != nil {
				}
				cc = ccc
			} else if e.Type == pb.EntryConfChangeV2 {
				var ccc pb.ConfChangeV2
				if err := ccc.Unmarshal(e.Data); err != nil {
				}
				cc = ccc
			}
			if cc != nil {
               // 判断是否异常
				alreadyPending := r.pendingConfIndex > r.raftLog.applied
				alreadyJoint := len(r.prs.Config.Voters[1]) > 0
				wantsLeaveJoint := len(cc.AsV2().Changes) == 0

				var refused string
                // refuse 原因
				if alreadyPending {
					refused = fmt.Sprintf("possible unapplied conf change at index %d (applied to %d)", r.pendingConfIndex, r.raftLog.applied)
				} else if alreadyJoint && !wantsLeaveJoint {
					refused = "must transition out of joint config first"
				} else if !alreadyJoint && wantsLeaveJoint {
					refused = "not in joint state; refusing empty conf change"
				}

				if refused != "" {
					r.logger.Infof("%x ignoring conf change %v at config %s: %s", r.id, cc, r.prs.Config, refused)
					m.Entries[i] = pb.Entry{Type: pb.EntryNormal}
				} else {
                   // 关键操作：记录 conf pending 的位置
					r.pendingConfIndex = r.raftLog.lastIndex() + uint64(i) + 1
				}
			}
		}
```

所以，其实 stepLeader 这里只做了两件小事：

1. 做一些异常判断；
2. 记录当前 conf pending 的位置；


# Ready 消息应用

## raftNode.publishEntries

这条 conf 消息被所有节点都 commit 之后，将会走到 Ready 结构体，这个时候才是重头戏。

```go
func (rc *raftNode) publishEntries(ents []raftpb.Entry) bool {

	for i := range ents {
		switch ents[i].Type {
		case raftpb.EntryNormal:
            // 
		case raftpb.EntryConfChange:
			// 配置变更消息
			var cc raftpb.ConfChange
			cc.Unmarshal(ents[i].Data)
			// 应用配置变更信息
			rc.confState = *rc.node.ApplyConfChange(cc)
            // 网络层节点对应变动
			switch cc.Type {
			case raftpb.ConfChangeAddNode:
				if len(cc.Context) > 0 {
					rc.transport.AddPeer(types.ID(cc.NodeID), []string{string(cc.Context)})
				}
			case raftpb.ConfChangeRemoveNode:
				if cc.NodeID == uint64(rc.id) {
					log.Println("I've been removed from the cluster! Shutting down.")
					return false
				}
				rc.transport.RemovePeer(types.ID(cc.NodeID))
			}
```

node.ApplyConfChange 其实是走异步流水处理。

## node.ApplyConfChange

```go
func (n *node) ApplyConfChange(cc pb.ConfChangeI) *pb.ConfState {
	var cs pb.ConfState
	select {
	// 消息投队列，切流程处理，下一个处理是在 node.run 状态机的运转里；
	case n.confc <- cc.AsV2():
		n.rn.raft.logger.Infof("ApplyConfChange enqueue ......")
	case <-n.done:
	}
	select {
	case cs = <-n.confstatec:
	case <-n.done:
	}
	return &cs
}
```

只做两件事：

1. 把消息转为 ConfChangeV2 类型，投递到 n.confc ，等待 node.run 去异步处理；
2. 等待 n.confstatec 的状态返回；

## node.run

```go
func (n *node) run() {
    // 
	for {


		select {
        // 
		case cc := <-n.confc:
            // 取出对应当前节点的进度
			_, okBefore := r.prs.Progress[r.id]
            // 状态机内部应用配置修改
			cs := r.applyConfChange(cc)
			if _, okAfter := r.prs.Progress[r.id]; okBefore && !okAfter {
				var found bool
				for _, sl := range [][]uint64{cs.Voters, cs.VotersOutgoing} {
					for _, id := range sl {
						if id == r.id {
							found = true
						}
					}
				}
				if !found {
					propc = nil
				}
			}
            // 投递状态，因为对面等着呢；
			select {
			case n.confstatec <- cs:
			case <-n.done:
			}
		}
	}
}
```

# raft

## raft.applyConfChange

```go
func (r *raft) applyConfChange(cc pb.ConfChangeV2) pb.ConfState {
	cfg, prs, err := func() (tracker.Config, tracker.ProgressMap, error) {
		changer := confchange.Changer{
			Tracker:   r.prs,
			LastIndex: r.raftLog.lastIndex(),
		}
		if cc.LeaveJoint() {
			return changer.LeaveJoint()
		} else if autoLeave, ok := cc.EnterJoint(); ok {
            // 走这个分支出去
			return changer.EnterJoint(autoLeave, cc.Changes...)
		}
		return changer.Simple(cc.Changes...)
	}()
}
```

正是进入 joint 模式。

```
(1,2,3)
变成
(1,2,3), (1,2,3,4)
```

并且设置

```go
	cfg.AutoLeave = true
```

展开如下：

```go
func (c Changer) EnterJoint(autoLeave bool, ccs ...pb.ConfChangeSingle) (tracker.Config, tracker.ProgressMap, error) {
    // 左边拷贝到右边
	for id := range incoming(cfg.Voters) {
		outgoing(cfg.Voters)[id] = struct{}{}
	}
    // 创建进度表，创建角色
	if err := c.apply(&cfg, prs, ccs...); err != nil {
		return c.err(err)
	}
    // 设置 autoleave
	cfg.AutoLeave = autoLeave
}

func (c Changer) apply(cfg *tracker.Config, prs tracker.ProgressMap, ccs ...pb.ConfChangeSingle) error {
	for _, cc := range ccs {
		if cc.NodeID == 0 {
			continue
		}
		switch cc.Type {
		case pb.ConfChangeAddNode:
			// 添加正常的选举节点
			c.makeVoter(cfg, prs, cc.NodeID)
		case pb.ConfChangeAddLearnerNode:
			// 添加 Learner 节点
			c.makeLearner(cfg, prs, cc.NodeID)
		case pb.ConfChangeRemoveNode:
			// 删除对应节点
			c.remove(cfg, prs, cc.NodeID)
		case pb.ConfChangeUpdateNode:
		default:
			return fmt.Errorf("unexpected conf type %d", cc.Type)
		}
	}
	return nil
}
```

## Changer.makeVoter 

c.makeVoter 对于新节点做最重要的是，为新节点添加 map 进去：

```go
	incoming(cfg.Voters)[id] = struct{}{}
```

## ProgressTracker.Confstate

构造出 conf state 配置。

```go
func (p *ProgressTracker) ConfState() pb.ConfState {
	// 返回配置状态
	return pb.ConfState{
		Voters:         p.Voters[0].Slice(),
		VotersOutgoing: p.Voters[1].Slice(),
		Learners:       quorum.MajorityConfig(p.Learners).Slice(),
		LearnersNext:   quorum.MajorityConfig(p.LearnersNext).Slice(),
		AutoLeave:      p.AutoLeave,
	}
}
```

这样就可以把 confstate 投递到 n.confstatec 通道。这样 node.ApplyConfChange 就可以唤醒退出了。


# raft

## raft.advance

该方法是配合 ready 使用的，rd 已经被 apply 了，这个由业务自行保证。

```go
func (r *raft) advance(rd Ready) {
    // 拿到 apply 到位置
	if index := rd.appliedCursor(); index > 0 {
		r.raftLog.appliedTo(index)
        // 如果还处于 joint 的状态，并且 pendingConfIndex 已经被应用了，那么该走第二阶段了；
		if r.prs.Config.AutoLeave && index >= r.pendingConfIndex && r.state == StateLeader {
			ccdata, err := (&pb.ConfChangeV2{}).Marshal()
			if err != nil {
			}
			ent := pb.Entry{
				Type: pb.EntryConfChangeV2,
				Data: ccdata,
			}
            // 加入一个空的 entry ，类型为 EntryConfChangeV2
			if !r.appendEntry(ent) {
				r.pendingConfIndex = r.raftLog.lastIndex()
			} else {
				r.logger.Infof("initiating automatic transition out of joint configuration %s", r.prs.Config)
			}
		}
	}
	r.reduceUncommittedSize(rd.CommittedEntries)

	// 清理掉 unstable 里已经持久化到 wal / storage 的日志
	if len(rd.Entries) > 0 {
		// 取出来 Ready 里面最后的一个 entry，这个 entry 之前的都可以被清理掉
		e := rd.Entries[len(rd.Entries)-1]
		r.raftLog.stableTo(e.Index, e.Term)
	}
	// 已经持久化的快照可以直接置空
	if !IsEmptySnap(rd.Snapshot) {
		r.raftLog.stableSnapTo(rd.Snapshot.Metadata.Index)
	}
}
```

如果确认 pendingConfIndex 的位置已经被 apply ，那么就可以走下一阶段了，投递一个 EntryConfChangeV2 类型的空消息到日志里。

这个消息最后走到 ready ，然后由业务发起 apply 。在状态机 raft.applyConfChange 里会对应的处理。

```go
func (r *raft) applyConfChange(cc pb.ConfChangeV2) pb.ConfState {
	cfg, prs, err := func() (tracker.Config, tracker.ProgressMap, error) {
		changer := confchange.Changer{
			Tracker:   r.prs,
			LastIndex: r.raftLog.lastIndex(),
		}
		if cc.LeaveJoint() {
            // 这次走这个路径出去
			return changer.LeaveJoint()
		} else if autoLeave, ok := cc.EnterJoint(); ok {
			return changer.EnterJoint(autoLeave, cc.Changes...)
		}
		return changer.Simple(cc.Changes...)
	}()

	if err != nil {
		panic(err)
	}

	return r.switchToConfig(cfg, prs)
}
```

在 applyConfChange 这个方法就是先走 LeaveJoint ，稳定配置，然后调用 switchToConfig 来切换配置。

# Joint Consensus

以上的理论基础都是 raft Joint Consensus 算法。

第一次的是进入 joint 的命令，第二次的是退出 joint 的命令。

在 joint 状态下，不仅要保证旧集群的 quorum ，也要保证新配置下的 quorum 。


# 单节点变更

单节点变更不会出问题，所以不用走 joint consensus 的算法。而是在 raft.applyConfChange 中使用 Simple 方式来应用配置：

```go
func (r *raft) applyConfChange(cc pb.ConfChangeV2) pb.ConfState {
	cfg, prs, err := func() (tracker.Config, tracker.ProgressMap, error) {
		// 创建 changer 对象
		changer := confchange.Changer{
			Tracker:   r.prs,
			LastIndex: r.raftLog.lastIndex(),
		}
		if cc.LeaveJoint() {
			return changer.LeaveJoint()
		} else if autoLeave, ok := cc.EnterJoint(); ok {
			return changer.EnterJoint(autoLeave, cc.Changes...)
		}
		// 简单应用配置，这种一般是单节点的配置变更，不用走 joint consensus 变更算法
		// 这里 cc.Changes 为 1 的数组
		return changer.Simple(cc.Changes...)
	}()

	if err != nil {
		// TODO(tbg): return the error to the caller.
		panic(err)
	}

	// 切换使用新的配置
	return r.switchToConfig(cfg, prs)
}
```

单节点变更配置不至于出现任何问题，所以都是直接变更，而不是走两阶段变更。

# 集群初始化

## 常规

集群初始化配置是怎么样的流程？

主要在 newRaft 函数（ raft/raft.go ）。

1. 首先 c.Storage.InitialState() 获取到持久化了的集群配置；
2. 然后 confchange.Restore 恢复出 progress，cfg 等结构体；
3. 最后调用 r.switchToConfig 来应用配置;
    1. 在这个里面就会尝试着 probe 日志的复制位置；


## raftexample 

raftexample 第一次的时候，走了 bootstrap 流程，这个流程里添加了三条配置变更的日志，并且直接应用到 raft 状态机了。

后面等这三条日志 commit 之后，还会 apply 一次。

```go
func (rn *RawNode) Bootstrap(peers []Peer) error {
    
    // commited 为 3，apply 为 0 ，这就会导致这三条日志稍后会进到 Ready 的 committed entries 里，从而被 apply 掉；
    rn.raft.raftLog.committed = uint64(len(ents))

}
```

选举之后，会有一条 empty 消息，这样 index 就变成 4 了。