// Package feedback 提供情绪分析实现
package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// LLMCaller 调用 LLM 的接口
type LLMCaller interface {
	CallSimple(ctx context.Context, prompt string) (string, error)
}

// LLMSentimentAnalyzer 使用 LLM 进行情绪分析
type LLMSentimentAnalyzer struct {
	llm LLMCaller
}

// NewLLMSentimentAnalyzer 创建基于 LLM 的情绪分析器
func NewLLMSentimentAnalyzer(llm LLMCaller) *LLMSentimentAnalyzer {
	return &LLMSentimentAnalyzer{llm: llm}
}

// SentimentResult LLM 返回的情绪分析结果
type SentimentResult struct {
	Sentiment float64 `json:"sentiment"` // -1.0 到 1.0
	Reasoning string  `json:"reasoning"` // 分析理由
}

// AnalyzeSentiment 使用 LLM 分析消息的情绪倾向
func (a *LLMSentimentAnalyzer) AnalyzeSentiment(ctx context.Context, messages []string) (float64, error) {
	if len(messages) == 0 {
		return 0, nil
	}

	prompt := buildSentimentPrompt(messages)
	response, err := a.llm.CallSimple(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("llm call failed: %w", err)
	}

	// 尝试解析 JSON 响应
	var result SentimentResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// 如果不是 JSON，尝试从文本中提取分数
		sentiment := extractSentimentFromText(response)
		return sentiment, nil
	}

	// 限制在 [-1, 1] 范围
	if result.Sentiment > 1 {
		result.Sentiment = 1
	} else if result.Sentiment < -1 {
		result.Sentiment = -1
	}

	return result.Sentiment, nil
}

func buildSentimentPrompt(messages []string) string {
	var sb strings.Builder
	sb.WriteString("分析以下用户反馈消息的整体情绪倾向。\n\n")
	sb.WriteString("背景：这些消息是用户在收到机器人回复后30秒内发送的反馈。\n\n")
	sb.WriteString("消息列表：\n")
	for i, msg := range messages {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
	}
	sb.WriteString("\n")
	sb.WriteString("请分析这些消息的情绪倾向，返回一个 -1.0 到 1.0 之间的分数：\n")
	sb.WriteString("- 1.0 = 非常正面（感谢、赞赏、满意）\n")
	sb.WriteString("- 0.5 = 中等正面（认可、接受）\n")
	sb.WriteString("- 0.0 = 中性（继续对话但无明显情绪）\n")
	sb.WriteString("- -0.5 = 中等负面（质疑、不满）\n")
	sb.WriteString("- -1.0 = 非常负面（愤怒、拒绝、批评）\n\n")
	sb.WriteString("请返回 JSON 格式：{\"sentiment\": 0.5, \"reasoning\": \"用户表示感谢...\"}\n")
	return sb.String()
}

func extractSentimentFromText(text string) float64 {
	text = strings.ToLower(text)

	// 尝试找到数字
	if strings.Contains(text, "1.0") || strings.Contains(text, "1.00") {
		return 1.0
	}
	if strings.Contains(text, "0.5") {
		return 0.5
	}
	if strings.Contains(text, "-0.5") {
		return -0.5
	}
	if strings.Contains(text, "-1.0") || strings.Contains(text, "-1.00") {
		return -1.0
	}

	// 根据关键词猜测
	if strings.Contains(text, "positive") || strings.Contains(text, "正面") {
		return 0.5
	}
	if strings.Contains(text, "negative") || strings.Contains(text, "负面") {
		return -0.5
	}

	return 0.0
}
