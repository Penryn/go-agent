package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/phlin/go-agent/internal/application/textutil"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// ParseTerminalPlan 解析终端工具的调用结果为 ReplyPlan
// 终端工具会结束 agent 循环并产生最终的回复计划
func ParseTerminalPlan(decisionID string, toolName string, raw string, session replydomain.ToolContext) (replydomain.ReplyPlan, bool, error) {
	switch toolName {
	case "speak_text":
		var result speakTextResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode speak_text result: %w", err)
		}
		cleanedText := textutil.StripThinkBlocks(result.Text)
		cleanedBubbles := make([]string, 0, len(result.Bubbles))
		for _, b := range result.Bubbles {
			cleanedBubbles = append(cleanedBubbles, textutil.StripThinkBlocks(b))
		}
		bubbles := cleanedBubbles
		if len(bubbles) == 0 && strings.TrimSpace(cleanedText) != "" {
			bubbles = textutil.SplitNaturalBubbles(cleanedText, 2)
		}
		return replydomain.ReplyPlan{
			PlanID:               decisionID + "-plan",
			Intent:               session.Intent,
			ReplyToMessageID:     result.ReplyToMessageID,
			Bubbles:              bubbles,
			PlannedActions:       []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:             "group",
			FallbackText:         cleanedText,
			ProposedPersonaFacts: append([]replydomain.PersonaFactCandidate(nil), result.SelfFacts...),
		}, true, nil
	case "stay_silent":
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent},
			SendMode:       "none",
		}, true, nil
	case "react_emoji":
		var result reactEmojiResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode react_emoji result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionReact},
			ActionParams: map[string]any{
				"emoji_id":   result.EmojiID,
				"message_id": result.MessageID,
			},
			SendMode: "group",
		}, true, nil
	case "send_meme":
		var result sendMemeResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode send_meme result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionMemeOnly},
			ActionParams: map[string]any{
				"meme_id": result.MemeID,
				"caption": result.Caption,
			},
			SendMode:     "group",
			FallbackText: result.Caption,
		}, true, nil
	case "quote_reply":
		var result quoteReplyResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode quote_reply result: %w", err)
		}
		cleanedText := textutil.StripThinkBlocks(result.Text)
		cleanedBubbles := make([]string, 0, len(result.Bubbles))
		for _, b := range result.Bubbles {
			cleanedBubbles = append(cleanedBubbles, textutil.StripThinkBlocks(b))
		}
		bubbles := cleanedBubbles
		if len(bubbles) == 0 && strings.TrimSpace(cleanedText) != "" {
			bubbles = textutil.SplitNaturalBubbles(cleanedText, 2)
		}
		return replydomain.ReplyPlan{
			PlanID:               decisionID + "-plan",
			Intent:               session.Intent,
			ReplyToMessageID:     result.ReplyToMessageID,
			Bubbles:              bubbles,
			PlannedActions:       []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:             "group",
			FallbackText:         cleanedText,
			ProposedPersonaFacts: append([]replydomain.PersonaFactCandidate(nil), result.SelfFacts...),
		}, true, nil
	case "repair_message":
		var result repairMessageResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode repair_message result: %w", err)
		}
		correctedText := textutil.StripThinkBlocks(result.CorrectedText)
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionRepair},
			ActionParams: map[string]any{
				"message_id":          result.MessageID,
				"corrected_text":      correctedText,
				"reply_to_message_id": result.ReplyToMessageID,
			},
			SendMode:     "group",
			FallbackText: correctedText,
		}, true, nil
	case "poke_member":
		var result pokeMemberResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode poke_member result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionPokeBack},
			ActionParams: map[string]any{
				"user_id": result.UserID,
			},
			SendMode: "group",
		}, true, nil
	default:
		return replydomain.ReplyPlan{}, false, nil
	}
}

// compactPersonaFacts 压缩人格事实，移除重复
func compactPersonaFacts(facts []personadomain.PersonaFact) []personadomain.PersonaFact {
	seen := make(map[string]bool, len(facts))
	result := make([]personadomain.PersonaFact, 0, len(facts))
	for _, f := range facts {
		key := f.Key + "\x00" + f.Value
		if !seen[key] {
			seen[key] = true
			result = append(result, f)
		}
	}
	return result
}
