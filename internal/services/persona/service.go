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
	if time.Since(current.UpdatedAt) < debounceWindow {
		return nil
	}

	mood := personadomain.Mood(current.Mood)
	energy := personadomain.Energy(current.Energy)

	// ---- Mood 更新规则 ----
	switch {
	case isDirectCue(snapshot) && replied:
		// 被直接 cue 且成功回复 → 互动感 → happy
		mood = personadomain.MoodHappy

	case isHighAffinityUser(snapshot) && replied:
		// 高好感用户触发的回复 → happy（不从 aggro 直接跳 happy）
		if mood != personadomain.MoodAggro {
			mood = personadomain.MoodHappy
		}

	case isFloodCue(snapshot, decision):
		// 短时间内被高频 cue 且都是 direct_triggered → 烦躁
		mood = personadomain.MoodAggro

	case decision.Action == policydomain.ActionSilent &&
		decision.TriggerType == "" &&
		mood == personadomain.MoodHappy:
		// 安静观察轮次，happy 自然衰减到 steady
		mood = personadomain.MoodSteady

	case isLowEngagement(snapshot):
		// 群里长时间无互动 → withdrawn
		if mood == personadomain.MoodSteady {
			mood = personadomain.MoodWithdrawn
		}
	}

	// ---- Energy 更新规则 ----
	switch {
	case replied && energy == personadomain.EnergyHigh:
		// 在 high 状态下发言 → 消耗资源 → normal
		energy = personadomain.EnergyNormal

	case replied && energy == personadomain.EnergyNormal:
		// 连续回复消耗精力，ConsecutiveBotTurns >= 3 时降为 low
		if snapshot.RuntimeState.ConsecutiveBotTurns >= 3 {
			energy = personadomain.EnergyLow
		}

	case replied && energy == personadomain.EnergyLow:
		// 在精力低时仍在继续回复 → tired
		energy = personadomain.EnergyTired

	case !replied && energy == personadomain.EnergyTired:
		// 沉默轮让 tired 恢复到 low（沉默 = 在休息）
		energy = personadomain.EnergyLow

	case isOffPeakQuiet(snapshot):
		// 群静止时段，精力慢慢恢复到 normal
		if energy == personadomain.EnergyLow {
			energy = personadomain.EnergyNormal
		}
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
	return decision.TriggerType == "direct_triggered" &&
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
