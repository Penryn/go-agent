package autonomy

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
)

type Service struct {
	policy    *policysvc.Service
	gate      *gatesvc.Service
	randFloat func() float64
	now       func() time.Time
}

// Option customizes non-deterministic inputs for tests.
type Option func(*Service)

// WithRandFloat replaces the random source used by poke decisions.
func WithRandFloat(randFloat func() float64) Option {
	return func(s *Service) {
		if randFloat != nil {
			s.randFloat = randFloat
		}
	}
}

// WithClock replaces the clock used for state updates.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func New(policy *policysvc.Service, gate *gatesvc.Service, opts ...Option) *Service {
	service := &Service{policy: policy, gate: gate, randFloat: rand.Float64, now: time.Now}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *Service) Decide(ctx context.Context, snapshot conversationdomain.ContextSnapshot) (policydomain.AutonomyDecision, policydomain.RuntimeState, error) {
	now := time.Unix(snapshot.Event.TimestampUnix, 0)
	if now.IsZero() {
		now = time.Now()
	}

	groupPolicy := snapshot.GroupPolicy
	runtimeState := snapshot.RuntimeState
	if runtimeState.State == "" {
		runtimeState.State = policydomain.StateObserving
	}
	runtimeState.GroupID = snapshot.Event.GroupID

	decision := policydomain.AutonomyDecision{
		DecisionID:  fmt.Sprintf("decision-%d", now.UnixNano()),
		StateBefore: runtimeState.State,
		StateAfter:  runtimeState.State,
		Action:      policydomain.ActionSilent,
		Confidence:  0.9,
		Explain:     map[string]float64{},
	}

	if !groupPolicy.Enabled {
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateSuppressed, "group_disabled")
		return decision, runtimeState, nil
	}

	if s.policy.QuietHourActive(now, groupPolicy) {
		runtimeState.State = policydomain.StateSuppressed
		runtimeState.SuppressedUntil = now.Add(time.Hour)
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateSuppressed, "quiet_hour")
		return decision, runtimeState, nil
	}

	direct := snapshot.Event.MentionedBot || snapshot.Event.NamedBot || snapshot.Event.IsReplyToBot
	if direct {
		decision, runtimeState = reply(decision, runtimeState, policydomain.StateCooldown, "direct_triggered", 1, s.now())
		return decision, runtimeState, nil
	}

	if runtimeState.SuppressedUntil.After(now) {
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateSuppressed, "suppressed_active")
		return decision, runtimeState, nil
	}

	// EventPoke：被戳一戳。在 suppressed_active 之后处理，suppress 期间也静默。
	// 三路决策：戳回（ActionPokeBack）/ 对话回复（ActionPokeReply）/ 静默。
	// 不更新 LastDirectedAt / CooldownUntil，poke 是轻社交信号，不重置冷却。
	if snapshot.Event.Kind == conversationdomain.EventPoke {
		decision, runtimeState = decidePoke(decision, runtimeState, groupPolicy, s.randFloat, s.now())
		return decision, runtimeState, nil
	}

	if strings.TrimSpace(snapshot.Event.Text) == "" && len(snapshot.Event.Attachments) == 0 {
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateObserving, "low_signal")
		return decision, runtimeState, nil
	}

	if runtimeState.CooldownUntil.After(now) {
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateCooldown, "cooldown_active")
		return decision, runtimeState, nil
	}

	autonomyPolicy := s.policy.AutonomyPolicy()

	// 人类发言即重置连续 bot 发言计数；用本轮前的旧值做上限检查。
	// 进入 Decide 的消息均为人类消息（bot 自身消息在 normalizer 层已过滤）。
	prevBotTurns := runtimeState.ConsecutiveBotTurns
	runtimeState.ConsecutiveBotTurns = 0

	if prevBotTurns >= groupPolicy.MaxConsecutiveBot {
		suppressSec := autonomyPolicy.BotDominanceSuppressSec
		if suppressSec <= 0 {
			suppressSec = 60
		}
		runtimeState.State = policydomain.StateSuppressed
		runtimeState.SuppressedUntil = now.Add(time.Duration(suppressSec) * time.Second)
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateSuppressed, "recent_bot_dominance")
		return decision, runtimeState, nil
	}

	if len(snapshot.Event.Attachments) > 0 && groupPolicy.ReplyToImageChance >= autonomyPolicy.ProactiveScoreThreshold {
		runtimeState.State = policydomain.StateCooldown
		runtimeState.CooldownUntil = now.Add(time.Duration(autonomyPolicy.MinReplyIntervalSec) * time.Second)
		runtimeState.LastProactiveAt = now
		runtimeState.LastBotSpeakAt = now
		runtimeState.ConsecutiveBotTurns++
		decision.Action = policydomain.ActionReply
		decision.StateAfter = policydomain.StateCooldown
		decision.TriggerType = "proactive_candidate"
		decision.Score = groupPolicy.ReplyToImageChance
		decision.ReasonCodes = []string{"media_hook"}
		decision.Explain["media_hook"] = groupPolicy.ReplyToImageChance
		return decision, runtimeState, nil
	}

	if autonomyPolicy.LLMGateEnabled && s.gate != nil {
		gateDecision, err := s.gate.Evaluate(ctx, snapshot)
		if err == nil && (gateDecision.CueBot || gateDecision.NaturalHook) && gateDecision.Score >= autonomyPolicy.ProactiveScoreThreshold {
			runtimeState.State = policydomain.StateCooldown
			runtimeState.CooldownUntil = now.Add(time.Duration(autonomyPolicy.MinReplyIntervalSec) * time.Second)
			runtimeState.LastProactiveAt = now
			runtimeState.LastBotSpeakAt = now
			runtimeState.ConsecutiveBotTurns++
			decision.Action = policydomain.ActionReply
			decision.StateAfter = policydomain.StateCooldown
			decision.TriggerType = "llm_gate"
			decision.Score = gateDecision.Score
			decision.ReasonCodes = []string{"llm_gate"}
			decision.Explain["llm_gate"] = gateDecision.Score
			return decision, runtimeState, nil
		}
	}

	decision, runtimeState = silent(decision, runtimeState, policydomain.StateObserving, "semantic_relevance_low")
	return decision, runtimeState, nil
}

func silent(decision policydomain.AutonomyDecision, state policydomain.RuntimeState, nextState policydomain.AutonomyState, reason string) (policydomain.AutonomyDecision, policydomain.RuntimeState) {
	decision.Action = policydomain.ActionSilent
	decision.StateAfter = nextState
	decision.ReasonCodes = []string{reason}
	decision.Explain[reason] = 1
	state.State = nextState
	return decision, state
}

func reply(decision policydomain.AutonomyDecision, state policydomain.RuntimeState, nextState policydomain.AutonomyState, trigger string, score float64, now time.Time) (policydomain.AutonomyDecision, policydomain.RuntimeState) {
	decision.Action = policydomain.ActionReply
	decision.StateAfter = nextState
	decision.TriggerType = trigger
	decision.Score = score
	decision.ReasonCodes = []string{trigger}
	decision.Explain[trigger] = score
	state.State = nextState
	state.LastDirectedAt = now
	state.LastBotSpeakAt = now
	state.CooldownUntil = now.Add(30 * time.Second)
	state.ConsecutiveBotTurns++
	return decision, state
}

// decidePoke 对 EventPoke 事件做三路决策：ActionPokeBack / ActionPokeReply / ActionSilent。
// 基础概率由 GroupPolicy.PokeBackChance 或 AllowPokeBack 推导，运行时 mood/energy 乘以修正系数。
// 不修改 CooldownUntil / LastDirectedAt，维持 poke 的轻社交信号语义。
func decidePoke(
	decision policydomain.AutonomyDecision,
	state policydomain.RuntimeState,
	groupPolicy policydomain.GroupPolicy,
	randFloat func() float64,
	now time.Time,
) (policydomain.AutonomyDecision, policydomain.RuntimeState) {
	// 推导基础戳回概率
	pokeBackBase := groupPolicy.PokeBackChance
	if pokeBackBase <= 0 {
		if groupPolicy.AllowPokeBack {
			pokeBackBase = 0.7 // 历史默认：AllowPokeBack=true 等价于 70% 概率戳回
		} else {
			pokeBackBase = 0.0
		}
	}

	// 状态乘数：mood/energy 影响实际概率
	pokeBackW := pokeBackBase * moodPokeBackMultiplier(state.CurrentMood, state.CurrentEnergy)
	replyW := 0.4 * moodReplyMultiplier(state.CurrentMood, state.CurrentEnergy) // 对话回复基础概率 40%
	if pokeBackW < 0 {
		pokeBackW = 0
	}
	if replyW < 0 {
		replyW = 0
	}
	// 归一化：两者之和超过 1 时等比压缩
	if total := pokeBackW + replyW; total > 1.0 {
		pokeBackW = pokeBackW / total
		replyW = replyW / total
	}

	r := randFloat()
	switch {
	case r < pokeBackW:
		decision.Action = policydomain.ActionPokeBack
		decision.TriggerType = "poke_back"
		decision.Score = pokeBackW
		decision.ReasonCodes = []string{"poke_back"}
		decision.Explain["poke_back"] = pokeBackW
	case r < pokeBackW+replyW:
		decision, state = pokeReply(decision, state, replyW, now)
	default:
		decision, state = silent(decision, state, state.State, "poke_silent")
	}
	return decision, state
}

// pokeReply 设置 ActionPokeReply 决策。
// 与 reply() 不同，不设置 CooldownUntil / LastDirectedAt，维持轻社交信号语义。
func pokeReply(
	decision policydomain.AutonomyDecision,
	state policydomain.RuntimeState,
	score float64,
	now time.Time,
) (policydomain.AutonomyDecision, policydomain.RuntimeState) {
	decision.Action = policydomain.ActionPokeReply
	decision.StateAfter = state.State // 不跳 StateCooldown
	decision.TriggerType = "poke_reply"
	decision.Score = score
	decision.ReasonCodes = []string{"poke_reply"}
	decision.Explain["poke_reply"] = score
	state.LastBotSpeakAt = now
	state.ConsecutiveBotTurns++
	return decision, state
}

// moodReplyMultiplier 根据心情/能量返回对话回复概率的乘数。
func moodReplyMultiplier(mood, energy string) float64 {
	switch mood {
	case "happy", "excited", "playful":
		return 1.5
	case "irritated", "angry":
		return 0.2
	case "withdrawn", "tired", "sad":
		return 0.3
	}
	switch energy {
	case "low":
		return 0.4
	case "high":
		return 1.2
	}
	return 1.0
}

// moodPokeBackMultiplier 根据心情/能量返回戳回概率的乘数。
func moodPokeBackMultiplier(mood, energy string) float64 {
	switch mood {
	case "happy", "excited", "playful":
		return 1.2
	case "irritated", "angry":
		return 0.3 // 烦躁时懒得理
	case "withdrawn", "tired":
		return 0.3
	}
	switch energy {
	case "low":
		return 0.3
	case "high":
		return 1.1
	}
	return 1.0
}
