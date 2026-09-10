package scene

import (
	"strings"
	"testing"
	"time"
)

func TestTopicExtractor_BasicExtraction(t *testing.T) {
	extractor := NewTopicExtractor()
	now := time.Now()

	messages := []Message{
		{Text: "今天我们讨论一下游戏攻略", Timestamp: now.Add(-2 * time.Minute)},
		{Text: "游戏攻略确实很重要", Timestamp: now.Add(-1 * time.Minute)},
		{Text: "攻略可以提高游戏效率", Timestamp: now},
	}

	topic := extractor.ExtractTopic(messages, now)

	// 应该包含"游戏"或"攻略"（都出现至少2次）
	if !strings.Contains(topic, "游戏") && !strings.Contains(topic, "攻略") {
		t.Errorf("话题应包含关键词，实际: %s", topic)
	}

	// 不应该是默认话题
	if topic == "闲聊" {
		t.Errorf("有明确关键词时不应返回'闲聊'")
	}
}

func TestTopicExtractor_FilterShortMessages(t *testing.T) {
	extractor := NewTopicExtractor()
	now := time.Now()

	messages := []Message{
		{Text: "哈哈", Timestamp: now},
		{Text: "好", Timestamp: now},
		{Text: "👍", Timestamp: now},
	}

	topic := extractor.ExtractTopic(messages, now)

	// 应该返回默认话题
	if topic != "闲聊" {
		t.Errorf("短消息应返回'闲聊'，实际: %s", topic)
	}
}

func TestTopicExtractor_TimeWindow(t *testing.T) {
	extractor := NewTopicExtractor()
	now := time.Now()

	messages := []Message{
		{Text: "很久之前讨论的话题", Timestamp: now.Add(-10 * time.Minute)},
		{Text: "现在讨论周末计划", Timestamp: now},
	}

	topic := extractor.ExtractTopic(messages, now)

	// 应该只包含最近的话题
	if strings.Contains(topic, "很久") {
		t.Errorf("不应包含时间窗口外的话题，实际: %s", topic)
	}
}

func TestTopicExtractor_EmptyMessages(t *testing.T) {
	extractor := NewTopicExtractor()
	now := time.Now()

	topic := extractor.ExtractTopic([]Message{}, now)

	if topic != "闲聊" {
		t.Errorf("空消息应返回'闲聊'，实际: %s", topic)
	}
}

func TestTopicExtractor_RepeatedKeywords(t *testing.T) {
	extractor := NewTopicExtractor()
	now := time.Now()

	messages := []Message{
		{Text: "周末计划去爬山", Timestamp: now.Add(-2 * time.Minute)},
		{Text: "爬山需要准备什么", Timestamp: now.Add(-1 * time.Minute)},
		{Text: "周末爬山约起来", Timestamp: now},
	}

	topic := extractor.ExtractTopic(messages, now)

	// 应该包含高频词"爬山"或"周末"
	if !strings.Contains(topic, "爬山") && !strings.Contains(topic, "周末") {
		t.Errorf("话题应包含高频关键词，实际: %s", topic)
	}
}

func TestIsOnlyEmoji(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"👍", true},
		{"😂😂😂", true},
		{"哈哈", false},
		{"good", false},
		{"👍不错", false},
	}

	for _, tt := range tests {
		result := isOnlyEmoji(tt.text)
		if result != tt.expected {
			t.Errorf("isOnlyEmoji(%q) = %v, 期望 %v", tt.text, result, tt.expected)
		}
	}
}

func TestIsOnlyPunctuation(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"...", true},
		{"？？？", true},
		{"！！！", true},
		{"好的", false},
		{"？是吗", false},
	}

	for _, tt := range tests {
		result := isOnlyPunctuation(tt.text)
		if result != tt.expected {
			t.Errorf("isOnlyPunctuation(%q) = %v, 期望 %v", tt.text, result, tt.expected)
		}
	}
}

func TestTopicExtractor_MultipleTopics(t *testing.T) {
	extractor := NewTopicExtractor()
	now := time.Now()

	messages := []Message{
		{Text: "今天天气不错啊", Timestamp: now.Add(-3 * time.Minute)},
		{Text: "是啊，适合出门", Timestamp: now.Add(-2 * time.Minute)},
		{Text: "天气好可以去公园", Timestamp: now.Add(-1 * time.Minute)},
		{Text: "公园里人应该很多", Timestamp: now},
	}

	topic := extractor.ExtractTopic(messages, now)

	// 应该提取多个关键词
	keywordCount := strings.Count(topic, "、") + 1
	if keywordCount < 2 {
		t.Errorf("应提取多个关键词，实际: %s", topic)
	}
}

func TestTopicExtractor_ChineseSegmentation(t *testing.T) {
	extractor := NewTopicExtractor()

	words := extractor.splitWords("今天天气真好，适合出门玩")

	// 应该分割出至少2个词
	if len(words) < 2 {
		t.Errorf("应分割出至少2个词，实际: %d (%v)", len(words), words)
	}
}
