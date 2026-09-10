// Package reflection 实现发送后的反馈收集、分类和状态更新。
//
// Deprecated: 该包中的 FeedbackCollector 和 FeedbackClassifierImpl 已废弃。
// 使用 internal/application/presence/feedback/llm_sentiment.go 中的
// LLMSentimentAnalyzer 代替，它提供更准确的反馈分析。
//
// 计划移除时间: v0.4.0
package reflection

import (
	"context"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	feedbackdomain "github.com/phlin/go-agent/internal/domain/feedback"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

// FeedbackCollector 反馈收集器
type FeedbackCollector struct {
	eventStore EventStore
	classifier feedbackdomain.FeedbackClassifier
}

// NewFeedbackCollector 创建反馈收集器
func NewFeedbackCollector(
	eventStore EventStore,
	classifier feedbackdomain.FeedbackClassifier,
) *FeedbackCollector {
	return &FeedbackCollector{
		eventStore: eventStore,
		classifier: classifier,
	}
}

// StartFeedbackWindow 创建反馈观察窗口
func (c *FeedbackCollector) StartFeedbackWindow(
	ctx context.Context,
	decisionID, actionID string,
	groupID int64,
	sentAt time.Time,
) (*presencedomain.FeedbackWindow, error) {
	window := &presencedomain.FeedbackWindow{
		WindowID:        generateWindowID(),
		DecisionID:      decisionID,
		ActionID:        actionID,
		GroupID:         groupID,
		SentAt:          sentAt,
		ObserveDuration: 30 * time.Second, // 观察 30 秒
		MaxEvents:       10,                // 最多观察 10 个事件
		Status:          "observing",
		ObservedEventIDs: []string{},
	}

	return window, nil
}

// CollectFeedback 收集反馈窗口内的事件并分类
func (c *FeedbackCollector) CollectFeedback(
	ctx context.Context,
	window *presencedomain.FeedbackWindow,
) (*feedbackdomain.ActionFeedback, error) {
	// 查询窗口期内的事件
	events, err := c.eventStore.ListEventsSince(
		ctx,
		window.GroupID,
		window.SentAt,
		window.ObserveDuration,
		window.MaxEvents,
	)
	if err != nil {
		return nil, err
	}

	// 提取事件 ID
	eventIDs := make([]string, len(events))
	for i, e := range events {
		eventIDs[i] = e.EventID
	}

	// 更新窗口
	window.ObservedEventIDs = eventIDs
	window.Status = "completed"
	window.ClosedAt = time.Now()

	// 分类反馈
	feedback, err := c.classifier.ClassifyFeedback(window.ActionID, eventIDs)
	if err != nil {
		return nil, err
	}

	// 填充反馈类型到窗口
	window.FeedbackType = string(feedback.Type)
	window.FeedbackNote = feedback.SummaryNote

	return feedback, nil
}

// FeedbackClassifierImpl 反馈分类器实现
type FeedbackClassifierImpl struct {
	eventStore EventStore
}

// NewFeedbackClassifier 创建反馈分类器
func NewFeedbackClassifier(eventStore EventStore) *FeedbackClassifierImpl {
	return &FeedbackClassifierImpl{
		eventStore: eventStore,
	}
}

// ClassifyFeedback 分析事件并分类反馈
func (impl *FeedbackClassifierImpl) ClassifyFeedback(
	actionID string,
	observedEventIDs []string,
) (*feedbackdomain.ActionFeedback, error) {
	if len(observedEventIDs) == 0 {
		// 无回应
		return &feedbackdomain.ActionFeedback{
			FeedbackID:       generateFeedbackID(),
			ActionID:         actionID,
			CollectedAt:      time.Now(),
			ObservedEventIDs: observedEventIDs,
			Type:             feedbackdomain.TypeIgnored,
			Signals: []feedbackdomain.FeedbackSignal{
				{SignalType: feedbackdomain.SignalNoResponse, Intensity: 1.0},
			},
			OverallSentiment: 0.0,
			EngagementLevel:  0.0,
			SummaryNote:      "无人回应",
		}, nil
	}

	// 获取事件详情进行分析
	// TODO: 从 eventStore 获取事件详情
	// 目前基于事件数量和简单启发式规则分类

	signals := []feedbackdomain.FeedbackSignal{}
	var sentiment float64 = 0.0
	var engagement float64 = 0.0

	eventCount := len(observedEventIDs)

	// 根据事件数量判断参与度
	if eventCount >= 5 {
		// 多人参与或持续对话
		signals = append(signals, feedbackdomain.FeedbackSignal{
			SignalType: feedbackdomain.SignalContinued,
			Intensity:  0.8,
		})
		engagement = 0.8
		sentiment = 0.5 // 假设为正向
	} else if eventCount >= 2 {
		// 有回应但不热烈
		signals = append(signals, feedbackdomain.FeedbackSignal{
			SignalType: feedbackdomain.SignalBriefResponse,
			Intensity:  0.5,
		})
		engagement = 0.5
		sentiment = 0.0 // 中性
	} else {
		// 仅一条回应
		signals = append(signals, feedbackdomain.FeedbackSignal{
			SignalType: feedbackdomain.SignalBriefResponse,
			Intensity:  0.3,
		})
		engagement = 0.3
		sentiment = 0.0
	}

	// 根据情感判断反馈类型
	var feedbackType feedbackdomain.FeedbackType
	var summaryNote string

	if sentiment > 0.3 {
		feedbackType = feedbackdomain.TypePositive
		summaryNote = "获得正面回应"
	} else if sentiment < -0.3 {
		feedbackType = feedbackdomain.TypeNegative
		summaryNote = "遭遇负面反馈"
	} else if engagement > 0.4 {
		feedbackType = feedbackdomain.TypeNeutral
		summaryNote = "有正常互动"
	} else {
		feedbackType = feedbackdomain.TypeNeutral
		summaryNote = "有简短回应"
	}

	return &feedbackdomain.ActionFeedback{
		FeedbackID:       generateFeedbackID(),
		ActionID:         actionID,
		CollectedAt:      time.Now(),
		ObservedEventIDs: observedEventIDs,
		Type:             feedbackType,
		Signals:          signals,
		OverallSentiment: sentiment,
		EngagementLevel:  engagement,
		SummaryNote:      summaryNote,
	}, nil
}

// EventStore 事件存储接口
type EventStore interface {
	ListEventsSince(
		ctx context.Context,
		groupID int64,
		since time.Time,
		duration time.Duration,
		limit int,
	) ([]conversationdomain.ConversationEvent, error)
}

// 辅助函数
func generateWindowID() string {
	return "win_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

func generateFeedbackID() string {
	return "fb_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
