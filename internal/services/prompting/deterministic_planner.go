package prompting

import (
	"context"
	"fmt"
	"strings"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type DeterministicPlanner struct {
	persona personadomain.PersonaConfig
}

func NewDeterministicPlanner(persona personadomain.PersonaConfig) *DeterministicPlanner {
	return &DeterministicPlanner{persona: persona}
}

func (p *DeterministicPlanner) Plan(_ context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	plan := replydomain.ReplyPlan{
		PlanID:           decision.DecisionID + "-plan",
		ReplyToMessageID: snapshot.Event.MessageID,
		SendMode:         "group",
		FallbackText:     "收到",
	}

	if decision.Action == policydomain.ActionSilent {
		plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionSilent}
		return plan, nil
	}

	plan.Intent = replydomain.ReplyIntent{
		Kind:            "chat",
		Goal:            "自然接话",
		TargetUserIDs:   []int64{snapshot.Event.UserID},
		PreferShortText: true,
		MaxChars:        p.persona.ReplyMaxChars,
	}
	plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionReply}

	switch {
	case snapshot.Event.MentionedBot || snapshot.Event.NamedBot:
		plan.Bubbles = []string{fmt.Sprintf("%s 在，啥事", p.persona.Name)}
	case snapshot.Event.IsReplyToBot:
		plan.Bubbles = []string{"我看到了，继续说"}
	case len(snapshot.Event.Attachments) > 0:
		plan.Bubbles = []string{"这图有点东西"}
	case strings.TrimSpace(snapshot.Event.Text) != "":
		plan.Bubbles = []string{"收到，我在看"}
	default:
		plan.Bubbles = []string{plan.FallbackText}
	}

	return plan, nil
}
