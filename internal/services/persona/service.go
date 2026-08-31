package persona

import (
	"context"
	"log/slog"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	"github.com/phlin/go-agent/internal/runtime/scheduler"
)

const (
	moodStateTTL   = 2 * time.Hour
	debounceWindow = 30 * time.Second // 同一群 30s 内只更新一次情绪，防止被刷
	decayInterval  = 20 * time.Minute // Scheduler 每 20min 衰减一次
)

// Service 负责读写 PersonaState，驱动情绪状态的动态变化。
type Service struct {
	store     ports.RuntimeStateStore
	personaID string
}

// New 创建 PersonaService 实例。
func New(store ports.RuntimeStateStore, personaID string) *Service {
	return &Service{store: store, personaID: personaID}
}

// RegisterJobs 向 Scheduler 注册情绪衰减定时任务。
// groupIDs 来自 cfg.QQ.GroupWhitelist，用于遍历所有已知群。
func (s *Service) RegisterJobs(sched *scheduler.Scheduler, groupIDs []int64) {
	sched.Register("persona_mood_decay", decayInterval, s.decayAllGroups(groupIDs))
}

// UpdateAfterTurn 在 processor.go 的 fireAndForget 中调用。
// 根据本轮 decision 和 snapshot 计算情绪变化方向，写入 Redis。
// replied=true 表示实际发出了内容（receipt.Sent==true 或 guard_silenced 的情况不计沉默）。
func (s *Service) UpdateAfterTurn(
	ctx context.Context,
	snapshot conversationdomain.ContextSnapshot,
	decision policydomain.AutonomyDecision,
	replied bool,
) error {
	groupID := snapshot.Event.GroupID
	current, err := s.store.GetPersonaState(ctx, s.personaID, groupID)
	if err != nil {
		return err
	}

	// 防抖：30s 内已更新过则跳过
	if !current.UpdatedAt.IsZero() && time.Since(current.UpdatedAt) < debounceWindow {
		return nil
	}

	mood, energy, talkBias := transitionState(current, snapshot, decision, replied)

	next := personadomain.PersonaState{
		PersonaID: s.personaID,
		GroupID:   groupID,
		Mood:      string(mood),
		Energy:    string(energy),
		TalkBias:  talkBias,
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(moodStateTTL),
	}

	if err := s.store.SavePersonaState(ctx, next); err != nil {
		return err
	}
	slog.Debug("persona mood updated",
		"group_id", groupID,
		"mood", next.Mood,
		"energy", next.Energy,
		"trigger", decision.TriggerType,
	)
	return nil
}

func transitionState(current personadomain.PersonaState, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision, replied bool) (personadomain.Mood, personadomain.Energy, float64) {
	mood := personadomain.Mood(current.Mood)
	if mood == "" {
		mood = personadomain.MoodSteady
	}
	energy := personadomain.Energy(current.Energy)
	if energy == "" {
		energy = personadomain.EnergyNormal
	}
	talkBias := current.TalkBias

	switch {
	case isFloodCue(snapshot, decision):
		mood = personadomain.MoodAggro
		talkBias -= 0.2
	case replied && (decision.TriggerType == "gratitude" || decision.TriggerType == "banter"):
		mood = personadomain.MoodHappy
		talkBias += 0.08
	case isHighAffinityUser(snapshot) && replied && mood != personadomain.MoodAggro:
		mood = personadomain.MoodHappy
		talkBias += 0.05
	case isDirectCue(snapshot) && replied && mood == personadomain.MoodWithdrawn:
		mood = personadomain.MoodSteady
		talkBias += 0.03
	case decision.Action == policydomain.ActionSilent && decision.TriggerType == "" && mood == personadomain.MoodHappy:
		mood = personadomain.MoodSteady
		talkBias -= 0.03
	case isLowEngagement(snapshot) && mood == personadomain.MoodSteady:
		mood = personadomain.MoodWithdrawn
		talkBias -= 0.05
	}

	switch {
	case replied && energy == personadomain.EnergyHigh:
		energy = personadomain.EnergyNormal
	case replied && energy == personadomain.EnergyNormal && snapshot.RuntimeState.ConsecutiveBotTurns >= 3:
		energy = personadomain.EnergyLow
	case replied && energy == personadomain.EnergyLow:
		energy = personadomain.EnergyTired
	case !replied && energy == personadomain.EnergyTired:
		energy = personadomain.EnergyLow
	case isOffPeakQuiet(snapshot) && energy == personadomain.EnergyLow:
		energy = personadomain.EnergyNormal
	}

	return mood, energy, min(max(talkBias, -0.5), 0.5)
}

// decayAllGroups 返回一个 Scheduler JobFunc，对所有已知群执行情绪衰减。
func (s *Service) decayAllGroups(groupIDs []int64) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		for _, gid := range groupIDs {
			if err := s.decayGroup(ctx, gid); err != nil {
				slog.Warn("persona decay failed", "group_id", gid, "err", err)
				// 单群失败不中断整体
			}
		}
		return nil
	}
}

// decayGroup 将指定群的情绪状态向基线衰减一步。
func (s *Service) decayGroup(ctx context.Context, groupID int64) error {
	current, err := s.store.GetPersonaState(ctx, s.personaID, groupID)
	if err != nil {
		return err
	}

	mood := personadomain.Mood(current.Mood)
	energy := personadomain.Energy(current.Energy)

	// 情绪向 steady 衰减
	switch mood {
	case personadomain.MoodHappy, personadomain.MoodWithdrawn, personadomain.MoodAggro:
		mood = personadomain.MoodSteady
	}

	// 精力向 normal 衰减一步
	switch energy {
	case personadomain.EnergyHigh:
		energy = personadomain.EnergyNormal
	case personadomain.EnergyTired:
		energy = personadomain.EnergyLow
	case personadomain.EnergyLow:
		energy = personadomain.EnergyNormal
	}

	next := personadomain.PersonaState{
		PersonaID: s.personaID,
		GroupID:   groupID,
		Mood:      string(mood),
		Energy:    string(energy),
		TalkBias:  current.TalkBias,
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(moodStateTTL),
	}
	return s.store.SavePersonaState(ctx, next)
}

// ---- 辅助判断函数 ----

func isDirectCue(snapshot conversationdomain.ContextSnapshot) bool {
	return snapshot.Event.MentionedBot ||
		snapshot.Event.NamedBot ||
		snapshot.Event.IsReplyToBot
}

func isHighAffinityUser(snapshot conversationdomain.ContextSnapshot) bool {
	return snapshot.RelationshipState.Affinity >= 0.6
}

func isFloodCue(snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) bool {
	return isDirectCue(snapshot) && decision.Action != policydomain.ActionSilent &&
		snapshot.RuntimeState.RepliesLast10Min >= 5
}

func isLowEngagement(snapshot conversationdomain.ContextSnapshot) bool {
	if snapshot.RuntimeState.LastBotSpeakAt.IsZero() {
		return false
	}
	return time.Since(snapshot.RuntimeState.LastBotSpeakAt) > 2*time.Hour
}

func isOffPeakQuiet(snapshot conversationdomain.ContextSnapshot) bool {
	if snapshot.RuntimeState.LastDirectedAt.IsZero() {
		return true
	}
	return time.Since(snapshot.RuntimeState.LastDirectedAt) > time.Hour
}
