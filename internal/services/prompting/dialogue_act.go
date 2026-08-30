package prompting

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
