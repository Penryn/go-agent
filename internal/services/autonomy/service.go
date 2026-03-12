package autonomy

import (
	"context"
	"fmt"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
)

type Service struct {
	policy *policysvc.Service
	gate   *gatesvc.Service
}

func New(policy *policysvc.Service, gate *gatesvc.Service) *Service {
	return &Service{policy: policy, gate: gate}
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

	if runtimeState.SuppressedUntil.After(now) {
		decision, runtimeState = silent(decision, runtimeState, policydomain.StateSuppressed, "suppressed_active")
		return decision, runtimeState, nil
	}

	direct := snapshot.Event.MentionedBot || snapshot.Event.NamedBot || snapshot.Event.IsReplyToBot
	if direct {
		decision, runtimeState = reply(decision, runtimeState, policydomain.StateCooldown, "direct_triggered", 1)
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
	if runtimeState.ConsecutiveBotTurns >= groupPolicy.MaxConsecutiveBot {
		runtimeState.State = policydomain.StateSuppressed
		runtimeState.SuppressedUntil = now.Add(5 * time.Minute)
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

func reply(decision policydomain.AutonomyDecision, state policydomain.RuntimeState, nextState policydomain.AutonomyState, trigger string, score float64) (policydomain.AutonomyDecision, policydomain.RuntimeState) {
	decision.Action = policydomain.ActionReply
	decision.StateAfter = nextState
	decision.TriggerType = trigger
	decision.Score = score
	decision.ReasonCodes = []string{trigger}
	decision.Explain[trigger] = score
	state.State = nextState
	now := time.Now()
	state.LastDirectedAt = now
	state.LastBotSpeakAt = now
	state.CooldownUntil = now.Add(30 * time.Second)
	state.ConsecutiveBotTurns++
	return decision, state
}
