package prompting

import (
	"strings"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

func dialogueGoal(trigger string) string {
	switch trigger {
	case "request_help":
		return "理解并处理对方的实际请求；缺少必要信息时只追问关键条件"
	case "support":
		return "先接住对方的情绪，再给简短、具体且不过度说教的回应"
	case "gratitude":
		return "自然回应感谢，不把简单互动扩写成客套总结"
	case "banter":
		return "接住轻松语气或笑点，简短接梗"
	case "question":
		return "直接回答问题；不确定时说明不确定，并仅在确有必要时查证"
	case "answer":
		return "回应对方直接发给你的内容，优先解决其真实问题"
	case "react":
		return "针对媒体内容做具体反应，不泛泛表示已看到"
	case "follow_up":
		return "延续尚未完成的话题，不重复已经说过的内容"
	default:
		return "结合上下文自然接话"
	}
}

func replyBudget(persona personadomain.PersonaConfig, snapshot conversationdomain.ContextSnapshot, trigger string) (int, int) {
	chars := persona.ReplyMaxChars
	sentences := persona.ReplyMaxSentences
	if chars <= 0 {
		chars = 110
	}
	if sentences <= 0 {
		sentences = 2
	}
	switch trigger {
	case "request_help", "question":
		chars = chars * 3 / 2
		sentences++
	case "banter", "gratitude":
		chars = chars * 3 / 4
	}
	if snapshot.PersonaState.Energy == "tired" || snapshot.PersonaState.TalkBias <= -0.2 {
		chars = chars * 3 / 4
		sentences = min(sentences, 2)
	}
	return max(chars, 40), max(sentences, 1)
}

func fallbackExpression(persona personadomain.PersonaConfig, trigger string) string {
	if configured := persona.Speech.Fallbacks[trigger]; len(configured) > 0 {
		for _, response := range configured {
			if response = strings.TrimSpace(response); response != "" {
				return response
			}
		}
	}
	switch trigger {
	case "request_help":
		return "把具体需求发来，我先看看。"
	case "support":
		return "我在。愿意的话就慢慢说。"
	case "gratitude":
		return "不用这么客气。"
	case "banter":
		return "看来确实挺有意思。"
	case "question":
		return "把具体情况说清楚些，我来看看。"
	default:
		if persona.Name != "" {
			return persona.Name + "在。你继续说。"
		}
		return "我在。你继续说。"
	}
}
