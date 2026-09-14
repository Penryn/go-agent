package feedback

import (
	"testing"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

func TestResponseAttributionRequiresExplicitLink(t *testing.T) {
	m := NewWindowManager(nil)
	sentAt := time.Now()
	if m.isResponseToBotMessage(conversationdomain.ConversationEvent{EventID: "unrelated", Text: "群里继续聊天"}, "bot-msg", sentAt) {
		t.Fatal("unrelated group traffic must not be feedback")
	}
	if !m.isResponseToBotMessage(conversationdomain.ConversationEvent{EventID: "reply", ReplyToMessageID: "bot-msg"}, "bot-msg", sentAt) {
		t.Fatal("direct reply must be feedback")
	}
	if m.isResponseToBotMessage(conversationdomain.ConversationEvent{EventID: "out", Origin: string(presencedomain.OriginOutbound), ReplyToMessageID: "bot-msg"}, "bot-msg", sentAt) {
		t.Fatal("outbound event must not be feedback")
	}
}

func TestRelevanceFilter_DirectReply(t *testing.T) {
	filter := NewRelevanceFilter(100, "bot_msg_123")

	events := []conversationdomain.ConversationEvent{
		{
			MessageID:        "evt_1",
			ReplyToMessageID: "bot_msg_123",
			Text:             "不对",
		},
	}

	relevant := filter.FilterExplicitFeedback(events)

	if len(relevant) != 1 {
		t.Errorf("期望 1 条相关消息，实际 %d 条", len(relevant))
	}
}

func TestRelevanceFilter_Mention(t *testing.T) {
	filter := NewRelevanceFilter(100, "bot_msg_123")

	events := []conversationdomain.ConversationEvent{
		{
			MessageID:    "evt_1",
			Text:         "我觉得不对",
			MentionedBot: true,
		},
	}

	relevant := filter.FilterExplicitFeedback(events)

	if len(relevant) != 1 {
		t.Errorf("期望 1 条相关消息，实际 %d 条", len(relevant))
	}
}

func TestRelevanceFilter_ExplicitSignal(t *testing.T) {
	filter := NewRelevanceFilter(100, "bot_msg_123")

	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"纠正信号", "不对，应该是这样", true},
		{"赞同信号", "没错，说得对", true},
		{"拒绝信号", "别说了", true},
		{"无关消息", "今天天气真好", false},
		{"短消息", "哈哈", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []conversationdomain.ConversationEvent{
				{MessageID: "evt_1", Text: tt.text},
			}

			relevant := filter.FilterExplicitFeedback(events)

			if tt.expected && len(relevant) == 0 {
				t.Errorf("期望过滤出消息，但被过滤掉了")
			}
			if !tt.expected && len(relevant) > 0 {
				t.Errorf("期望过滤掉消息，但被保留了")
			}
		})
	}
}

func TestRelevanceFilter_NoRelevant(t *testing.T) {
	filter := NewRelevanceFilter(100, "bot_msg_123")

	events := []conversationdomain.ConversationEvent{
		{MessageID: "evt_1", Text: "你好啊"},
		{MessageID: "evt_2", Text: "今天吃什么"},
		{MessageID: "evt_3", Text: "👍"},
	}

	relevant := filter.FilterExplicitFeedback(events)

	if len(relevant) != 0 {
		t.Errorf("期望 0 条相关消息，实际 %d 条", len(relevant))
	}
}

func TestContainsExplicitSignal(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		// 纠正信号
		{"不对，应该这样", true},
		{"错了", true},
		{"不是这个意思", true},

		// 赞同信号
		{"没错", true},
		{"说得对", true},

		// 拒绝信号
		{"别说了", true},
		{"闭嘴", true},

		// 无信号
		{"好的", false},
		{"哈哈", false},
		{"今天天气不错", false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := containsExplicitSignal(tt.text)
			if result != tt.expected {
				t.Errorf("text=%q, 期望=%v, 实际=%v", tt.text, tt.expected, result)
			}
		})
	}
}
