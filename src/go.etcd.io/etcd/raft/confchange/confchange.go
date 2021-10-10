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

package confchange

import (
	"errors"
	"fmt"
	"strings"

	"go.etcd.io/etcd/raft/quorum"
	pb "go.etcd.io/etcd/raft/raftpb"
	"go.etcd.io/etcd/raft/tracker"
)

// Changer facilitates configuration changes. It exposes methods to handle
// simple and joint consensus while performing the proper validation that allows
// refusing invalid configuration changes before they affect the active
// configuration.
// 配置变更器的封装
type Changer struct {
	Tracker   tracker.ProgressTracker
	LastIndex uint64
}

// EnterJoint verifies that the outgoing (=right) majority config of the joint
// config is empty and initializes it with a copy of the incoming (=left)
// majority config. That is, it transitions from
//
//     (1 2 3)&&()
// to
//     (1 2 3)&&(1 2 3).
//
// The supplied changes are then applied to the incoming majority config,
// resulting in a joint configuration that in terms of the Raft thesis[1]
// (Section 4.3) corresponds to `C_{new,old}`.
//
// [1]: https://github.com/ongardie/dissertation/blob/master/online-trim.pdf
func (c Changer) EnterJoint(autoLeave bool, ccs ...pb.ConfChangeSingle) (tracker.Config, tracker.ProgressMap, error) {
	// 先克隆并且校验配置
	cfg, prs, err := c.checkAndCopy()
	if err != nil {
		return c.err(err)
	}
	// 如果已经是 joint 配置模式，那么返回错误
	if joint(cfg) {
		err := errors.New("config is already joint")
		return c.err(err)
	}
	// 如果没有 C_old 配置，那么属于异常
	if len(incoming(cfg.Voters)) == 0 {
		// We allow adding nodes to an empty config for convenience (testing and
		// bootstrap), but you can't enter a joint state.
		err := errors.New("can't make a zero-voter config joint")
		return c.err(err)
	}
	// 清理 outgoing 配置
	// Clear the outgoing config.
	*outgoingPtr(&cfg.Voters) = quorum.MajorityConfig{}
	// Copy incoming to outgoing.
	// 拷贝 incoming 的配置到 outgoing
	for id := range incoming(cfg.Voters) {
		outgoing(cfg.Voters)[id] = struct{}{}
	}

	// 应用配置
	if err := c.apply(&cfg, prs, ccs...); err != nil {
		return c.err(err)
	}
	cfg.AutoLeave = autoLeave
	return checkAndReturn(cfg, prs)
}

// LeaveJoint transitions out of a joint configuration. It is an error to call
// this method if the configuration is not joint, i.e. if the outgoing majority
// config Voters[1] is empty.
//
// The outgoing majority config of the joint configuration will be removed,
// that is, the incoming config is promoted as the sole decision maker. In the
// notation of the Raft thesis[1] (Section 4.3), this method transitions from
// `C_{new,old}` into `C_new`.
//
// At the same time, any staged learners (LearnersNext) the addition of which
// was held back by an overlapping voter in the former outgoing config will be
// inserted into Learners.
//
// [1]: https://github.com/ongardie/dissertation/blob/master/online-trim.pdf
// 由 C_{old, new} 状态转移 到 C_new
func (c Changer) LeaveJoint() (tracker.Config, tracker.ProgressMap, error) {
	cfg, prs, err := c.checkAndCopy()
	if err != nil {
		return c.err(err)
	}
	// 如果不是 joint 状态，那么返错
	if !joint(cfg) {
		err := errors.New("can't leave a non-joint config")
		return c.err(err)
	}
	// 如果没有 outgoing 配置，那么返错
	if len(outgoing(cfg.Voters)) == 0 {
		err := fmt.Errorf("configuration is not joint: %v", cfg)
		return c.err(err)
	}
	// 在这个地方，把所有的 LearnersNext 列表里的节点转表成 Learner 角色
	// LearnerNext 是准备变成 learner 的 id 列表，在这个地方变成 learner 角色
	for id := range cfg.LearnersNext {
		nilAwareAdd(&cfg.Learners, id)
		prs[id].IsLearner = true
	}
	cfg.LearnersNext = nil

	for id := range outgoing(cfg.Voters) {
		// 判断这个 incoming （C_old） 的节点是否有
		_, isVoter := incoming(cfg.Voters)[id]
		// 判断这个 outgoing 的节点是否是 learner
		_, isLearner := cfg.Learners[id]

		// 如果不是 voter ，也不是 learner ，那么说明是一个完全被剔除的节点，就从进度器里删除
		if !isVoter && !isLearner {
			delete(prs, id)
		}
	}

	// 由于已经切换换了，所以这里 outgoing 就不需要了
	*outgoingPtr(&cfg.Voters) = nil
	cfg.AutoLeave = false

	return checkAndReturn(cfg, prs)
}

// 应用配置, 初始化出 Progress
// Simple carries out a series of configuration changes that (in aggregate)
// mutates the incoming majority config Voters[0] by at most one. This method
// will return an error if that is not the case, if the resulting quorum is
// zero, or if the configuration is in a joint state (i.e. if there is an
// outgoing configuration).
func (c Changer) Simple(ccs ...pb.ConfChangeSingle) (tracker.Config, tracker.ProgressMap, error) {
	cfg, prs, err := c.checkAndCopy()
	if err != nil {
		return c.err(err)
	}
	if joint(cfg) {
		err := errors.New("can't apply simple config change in joint config")
		return c.err(err)
	}
	// 直接应用配置
	if err := c.apply(&cfg, prs, ccs...); err != nil {
		return c.err(err)
	}
	// 如果不一致超过一个，那么报错
	if n := symdiff(incoming(c.Tracker.Voters), incoming(cfg.Voters)); n > 1 {
		return tracker.Config{}, nil, errors.New("more than one voter changed without entering joint config")
	}
	if err := checkInvariants(cfg, prs); err != nil {
		return tracker.Config{}, tracker.ProgressMap{}, nil
	}

	return checkAndReturn(cfg, prs)
}

// 配置变更应用
// apply a change to the configuration. By convention, changes to voters are
// always made to the incoming majority config Voters[0]. Voters[1] is either
// empty or preserves the outgoing majority configuration while in a joint state.
func (c Changer) apply(cfg *tracker.Config, prs tracker.ProgressMap, ccs ...pb.ConfChangeSingle) error {
	// 批量节点的配置变更
	for _, cc := range ccs {
		// 一个节点的配置变更
		if cc.NodeID == 0 {
			// etcd replaces the NodeID with zero if it decides (downstream of
			// raft) to not apply a change, so we have to have explicit code
			// here to ignore these.
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
	if len(incoming(cfg.Voters)) == 0 {
		return errors.New("removed all voters")
	}
	return nil
}

// 添加 voter
// makeVoter adds or promotes the given ID to be a voter in the incoming
// majority config.
func (c Changer) makeVoter(cfg *tracker.Config, prs tracker.ProgressMap, id uint64) {
	pr := prs[id]
	if pr == nil {
		// 初始化赋值进度
		c.initProgress(cfg, prs, id, false /* isLearner */)
		return
	}

	// 如果 progress map 里面已经存在节点，那么修改配置，learner 置成 false，并且从 Learners，LearnersNext 列表里去除
	pr.IsLearner = false
	nilAwareDelete(&cfg.Learners, id)
	nilAwareDelete(&cfg.LearnersNext, id)
	// 添加集群配置，voter 投票人
	incoming(cfg.Voters)[id] = struct{}{}
	return
}

// makeLearner makes the given ID a learner or stages it to be a learner once
// an active joint configuration is exited.
//
// The former happens when the peer is not a part of the outgoing config, in
// which case we either add a new learner or demote a voter in the incoming
// config.
//
// The latter case occurs when the configuration is joint and the peer is a
// voter in the outgoing config. In that case, we do not want to add the peer
// as a learner because then we'd have to track a peer as a voter and learner
// simultaneously. Instead, we add the learner to LearnersNext, so that it will
// be added to Learners the moment the outgoing config is removed by
// LeaveJoint().
func (c Changer) makeLearner(cfg *tracker.Config, prs tracker.ProgressMap, id uint64) {
	pr := prs[id]
	if pr == nil {
		c.initProgress(cfg, prs, id, true /* isLearner */)
		return
	}

	// 如果已经是 learner ，那么直接退出
	if pr.IsLearner {
		return
	}

	// 完全新建的过程，那么
	// Remove any existing voter in the incoming config...
	c.remove(cfg, prs, id)
	// ... but save the Progress.
	prs[id] = pr
	// Use LearnersNext if we can't add the learner to Learners directly, i.e.
	// if the peer is still tracked as a voter in the outgoing config. It will
	// be turned into a learner in LeaveJoint().
	//
	// Otherwise, add a regular learner right away.
	if _, onRight := outgoing(cfg.Voters)[id]; onRight {
		// 如果已经在 Voter outgoing 的列表中，这种情况不能直接变成 learner，
		// 先加到 LearnersNext 列表中，等待 LeaveJoint 的时候，peer id 会变成 learner
		nilAwareAdd(&cfg.LearnersNext, id)
	} else {
		// 没有在 Voter outgoing 列表里，配置成 learner
		pr.IsLearner = true
		nilAwareAdd(&cfg.Learners, id)
	}
}

// remove this peer as a voter or learner from the incoming config.
func (c Changer) remove(cfg *tracker.Config, prs tracker.ProgressMap, id uint64) {
	if _, ok := prs[id]; !ok {
		return
	}

	delete(incoming(cfg.Voters), id)
	nilAwareDelete(&cfg.Learners, id)
	nilAwareDelete(&cfg.LearnersNext, id)

	// If the peer is still a voter in the outgoing config, keep the Progress.
	if _, onRight := outgoing(cfg.Voters)[id]; !onRight {
		delete(prs, id)
	}
}

// 初始化一个 Progress
// initProgress initializes a new progress for the given node or learner.
func (c Changer) initProgress(cfg *tracker.Config, prs tracker.ProgressMap, id uint64, isLearner bool) {
	// follower 可以参加选举和投票，learner 不可以；但是，无论是 follower 还是 learner 都会有一个 Progress
	if !isLearner {
		// 加入 voter 集群
		// 如果是非 leader 节点，那么就赋值 incoming
		incoming(cfg.Voters)[id] = struct{}{}
	} else {
		// 如果是 learner 角色，那么就在对应的 map 里添加一个节点
		nilAwareAdd(&cfg.Learners, id)
	}

	// 对应节点 Progress 的赋值
	// 初始化 Progress 需要给定 Next，Match，Inflights 容量以及是否是 learner
	// raft 的初始化时候 Match=0，Next=1
	prs[id] = &tracker.Progress{
		// Initializing the Progress with the last index means that the follower
		// can be probed (with the last index).
		//
		// TODO(tbg): seems awfully optimistic. Using the first index would be
		// better. The general expectation here is that the follower has no log
		// at all (and will thus likely need a snapshot), though the app may
		// have applied a snapshot out of band before adding the replica (thus
		// making the first index the better choice).
		// 初始进度：设置为最后的位置，从后往前试。
		Next:      c.LastIndex,
		Match:     0,
		Inflights: tracker.NewInflights(c.Tracker.MaxInflight),
		IsLearner: isLearner,
		// When a node is first added, we should mark it as recently active.
		// Otherwise, CheckQuorum may cause us to step down if it is invoked
		// before the added node has had a chance to communicate with us.
		RecentActive: true,
	}
}

// 校验配置的正确性, 检查 config 配置和进度是否是正确的。用于检查 Changer 初始化的内容
// checkInvariants makes sure that the config and progress are compatible with
// each other. This is used to check both what the Changer is initialized with,
// as well as what it returns.
func checkInvariants(cfg tracker.Config, prs tracker.ProgressMap) error {
	// NB: intentionally allow the empty config. In production we'll never see a
	// non-empty config (we prevent it from being created) but we will need to
	// be able to *create* an initial config, for example during bootstrap (or
	// during tests). Instead of having to hand-code this, we allow
	// transitioning from an empty config into any other legal and non-empty
	// config.
	for _, ids := range []map[uint64]struct{}{
		cfg.Voters.IDs(),
		cfg.Learners,
		cfg.LearnersNext,
	} {
		// 每个 ids 也是一个 map，遍历 map，查看这个 map 的 id 在 progress 表里有没有，没有就属于异常的；
		// ids map 的 key 是节点 id
		for id := range ids {
			if _, ok := prs[id]; !ok {
				return fmt.Errorf("no progress for %d", id)
			}
		}
	}

	// Any staged learner was staged because it could not be directly added due
	// to a conflicting voter in the outgoing config.
	for id := range cfg.LearnersNext {
		// 如果节点是 LearnersNext 里的一员，那么就一定要是 outgoing ( C_new ) 的一员；
		if _, ok := outgoing(cfg.Voters)[id]; !ok {
			return fmt.Errorf("%d is in LearnersNext, but not Voters[1]", id)
		}
		// LearnersNext 说明还不是 Learner 呢，如果已经是了，那么是有问题的。
		if prs[id].IsLearner {
			return fmt.Errorf("%d is in LearnersNext, but is already marked as learner", id)
		}
	}

	// Learner 的角色和 Voters 的角色不能有交集
	// Conversely Learners and Voters doesn't intersect at all.
	for id := range cfg.Learners {
		// 如果已经是 Learner 了，还在 C_new 的 Voter 里面，那么报错
		if _, ok := outgoing(cfg.Voters)[id]; ok {
			return fmt.Errorf("%d is in Learners and Voters[1]", id)
		}
		// 如果已经是 Learner 了，还在 C_old 的 Voter 里面，那么报错
		if _, ok := incoming(cfg.Voters)[id]; ok {
			return fmt.Errorf("%d is in Learners and Voters[0]", id)
		}
		// 如果在 Learner 的 map ，但是进度表没有标识 Learner ，那么报错
		if !prs[id].IsLearner {
			return fmt.Errorf("%d is in Learners, but is not marked as learner", id)
		}
	}

	// 如果不是 joint 配置，那么继续判断
	if !joint(cfg) {
		// We enforce that empty maps are nil instead of zero.
		// 如果没有 C_new ，map 直接为非 nil ，那么报错，
		if outgoing(cfg.Voters) != nil {
			return fmt.Errorf("Voters[1] must be nil when not joint")
		}
		// 如果没有 LearnersNext ，那么报错
		if cfg.LearnersNext != nil {
			return fmt.Errorf("LearnersNext must be nil when not joint")
		}
		// 非 joint 模式，需要确保 AutoLeave 为 false
		if cfg.AutoLeave {
			return fmt.Errorf("AutoLeave must be false when not joint")
		}
	}

	return nil
}

// checkAndCopy copies the tracker's config and progress map (deeply enough for
// the purposes of the Changer) and returns those copies. It returns an error
// if checkInvariants does.
func (c Changer) checkAndCopy() (tracker.Config, tracker.ProgressMap, error) {
	cfg := c.Tracker.Config.Clone()
	prs := tracker.ProgressMap{}

	// 拷贝 Progress 进度表
	for id, pr := range c.Tracker.Progress {
		// A shallow copy is enough because we only mutate the Learner field.
		ppr := *pr
		prs[id] = &ppr
	}
	return checkAndReturn(cfg, prs)
}

// 校验配置是否正确，如果校验错误，返回空，err 返回对应错误信息
// checkAndReturn calls checkInvariants on the input and returns either the
// resulting error or the input.
func checkAndReturn(cfg tracker.Config, prs tracker.ProgressMap) (tracker.Config, tracker.ProgressMap, error) {
	if err := checkInvariants(cfg, prs); err != nil {
		return tracker.Config{}, tracker.ProgressMap{}, err
	}
	return cfg, prs, nil
}

// err returns zero values and an error.
func (c Changer) err(err error) (tracker.Config, tracker.ProgressMap, error) {
	return tracker.Config{}, nil, err
}

// nilAwareAdd populates a map entry, creating the map if necessary.
func nilAwareAdd(m *map[uint64]struct{}, id uint64) {
	if *m == nil {
		*m = map[uint64]struct{}{}
	}
	(*m)[id] = struct{}{}
}

// nilAwareDelete deletes from a map, nil'ing the map itself if it is empty after.
func nilAwareDelete(m *map[uint64]struct{}, id uint64) {
	if *m == nil {
		return
	}
	delete(*m, id)
	if len(*m) == 0 {
		*m = nil
	}
}

// symdiff returns the count of the symmetric difference between the sets of
// uint64s, i.e. len( (l - r) \union (r - l)).
func symdiff(l, r map[uint64]struct{}) int {
	var n int
	pairs := [][2]quorum.MajorityConfig{
		{l, r}, // count elems in l but not in r
		{r, l}, // count elems in r but not in l
	}
	for _, p := range pairs {
		for id := range p[0] {
			if _, ok := p[1][id]; !ok {
				n++
			}
		}
	}
	return n
}

func joint(cfg tracker.Config) bool {
	return len(outgoing(cfg.Voters)) > 0
}

// 这里就体现了 JointConfig 为[2]数组的原因
// C_old 配置，当前配置
func incoming(voters quorum.JointConfig) quorum.MajorityConfig { return voters[0] }

// C_new 配置，配置变更
func outgoing(voters quorum.JointConfig) quorum.MajorityConfig      { return voters[1] }
func outgoingPtr(voters *quorum.JointConfig) *quorum.MajorityConfig { return &voters[1] }

// Describe prints the type and NodeID of the configuration changes as a
// space-delimited string.
func Describe(ccs ...pb.ConfChangeSingle) string {
	var buf strings.Builder
	for _, cc := range ccs {
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		fmt.Fprintf(&buf, "%s(%d)", cc.Type, cc.NodeID)
	}
	return buf.String()
}
