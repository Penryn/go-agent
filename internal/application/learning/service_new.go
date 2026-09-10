package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// LLMProvider LLM 调用接口
type LLMProvider interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// NewService 创建新的学习服务（窗口提炼）
type NewService struct {
	store         ports.MemoryStore
	memoryService memorydomain.Service
	outbox        ports.TaskSubmitter
	llm           LLMProvider
	promptBuilder *ExtractionPromptBuilder

	// 窗口配置
	maxWindowSize      int           // 最多 60 条消息
	maxWindowDuration  time.Duration // 15 分钟邻近上下文
	idleWindowDuration time.Duration // 2 分钟空闲闭合
	extractorVersion   string        // 提炼器版本
}

// NewServiceConfig 配置
type NewServiceConfig struct {
	MaxWindowSize      int
	MaxWindowDuration  time.Duration
	IdleWindowDuration time.Duration
	ExtractorVersion   string
}

// NewLearningService 创建学习服务实例
func NewLearningService(
	store ports.MemoryStore,
	memService memorydomain.Service,
	outbox ports.TaskSubmitter,
	llm LLMProvider,
	config NewServiceConfig,
) *NewService {
	if config.MaxWindowSize == 0 {
		config.MaxWindowSize = 60
	}
	if config.MaxWindowDuration == 0 {
		config.MaxWindowDuration = 15 * time.Minute
	}
	if config.IdleWindowDuration == 0 {
		config.IdleWindowDuration = 2 * time.Minute
	}
	if config.ExtractorVersion == "" {
		config.ExtractorVersion = "v1"
	}

	return &NewService{
		store:              store,
		memoryService:      memService,
		outbox:             outbox,
		llm:                llm,
		promptBuilder:      NewExtractionPromptBuilder(config.ExtractorVersion),
		maxWindowSize:      config.MaxWindowSize,
		maxWindowDuration:  config.MaxWindowDuration,
		idleWindowDuration: config.IdleWindowDuration,
		extractorVersion:   config.ExtractorVersion,
	}
}

// Window 表示一个学习窗口
type Window struct {
	GroupID          int64
	Events           []conversation.ConversationEvent
	StartTime        time.Time
	EndTime          time.Time
	ContextEventIDs  []string // 上下文引用
}

// ProcessBatch 处理一批未处理的事件
func (s *NewService) ProcessBatch(ctx context.Context, groupID int64, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}

	// 1. 加载事件
	events, err := s.loadEvents(ctx, eventIDs)
	if err != nil {
		return fmt.Errorf("failed to load events: %w", err)
	}

	if len(events) == 0 {
		// 标记为已处理但无内容
		return s.markEventsProcessed(ctx, groupID, eventIDs, "skipped", "no_events", 0)
	}

	// 2. 构建窗口
	window := s.buildWindow(groupID, events)

	// 3. 提炼候选
	candidates, err := s.extractFromWindow(ctx, window)
	if err != nil {
		return fmt.Errorf("failed to extract candidates: %w", err)
	}

	// 4. 应用候选
	successCount := 0
	if len(candidates) > 0 {
		results, err := s.memoryService.ApplyCandidates(ctx, candidates)
		if err != nil {
			return fmt.Errorf("failed to apply candidates: %w", err)
		}

		for _, r := range results {
			if r.Success {
				successCount++
			}
		}
	}

	// 5. 标记每个事件为已处理
	return s.markEventsProcessed(ctx, groupID, eventIDs, "completed", "", successCount)
}

// markEventsProcessed 标记事件为已处理
func (s *NewService) markEventsProcessed(ctx context.Context, groupID int64, eventIDs []string, outcome, skipReason string, memoryCount int) error {
	memStore, ok := s.store.(memorydomain.Store)
	if !ok {
		return fmt.Errorf("store does not support memory interface")
	}

	for _, eventID := range eventIDs {
		progress := &memorydomain.LearningEventProgress{
			EventID:          eventID,
			ExtractorVersion: s.extractorVersion,
			GroupID:          groupID,
			ProcessedAt:      time.Now(),
			Outcome:          outcome,
			SkipReason:       skipReason,
			MemoryCount:      memoryCount,
			CreatedAt:        time.Now(),
		}

		if err := memStore.MarkProgress(ctx, progress); err != nil {
			return fmt.Errorf("failed to mark progress for %s: %w", eventID, err)
		}
	}

	return nil
}

// loadEvents 从数据库加载事件
func (s *NewService) loadEvents(ctx context.Context, eventIDs []string) ([]conversation.ConversationEvent, error) {
	// TODO: 实际实现需要从 messages 表加载
	// 当前返回空切片
	return []conversation.ConversationEvent{}, nil
}

// buildWindow 构建学习窗口
func (s *NewService) buildWindow(groupID int64, events []conversation.ConversationEvent) *Window {
	if len(events) == 0 {
		return &Window{GroupID: groupID}
	}

	return &Window{
		GroupID:   groupID,
		Events:    events,
		StartTime: time.Unix(events[0].TimestampUnix, 0),
		EndTime:   time.Unix(events[len(events)-1].TimestampUnix, 0),
	}
}

// extractFromWindow 从窗口提炼记忆候选
func (s *NewService) extractFromWindow(ctx context.Context, window *Window) ([]*memorydomain.MemoryCandidate, error) {
	if len(window.Events) == 0 {
		return nil, nil
	}

	// 1. 构建提炼 Prompt
	prompt := s.promptBuilder.BuildPrompt(window)

	// 2. 调用 LLM
	response, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	// 3. 解析响应
	candidates, err := s.promptBuilder.ParseExtractionResponse(response, window)
	if err != nil {
		return nil, fmt.Errorf("failed to parse extraction response: %w", err)
	}

	return candidates, nil
}

// ScanAndSchedule 扫描未处理事件并投递任务
func (s *NewService) ScanAndSchedule(ctx context.Context, groupID int64) error {
	if s.outbox == nil {
		return fmt.Errorf("outbox not configured")
	}

	// 获取未处理的事件
	memStore, ok := s.store.(memorydomain.Store)
	if !ok {
		return fmt.Errorf("store does not support memory interface")
	}

	eventIDs, err := memStore.ListUnprocessedEvents(ctx, groupID, s.extractorVersion, s.maxWindowSize)
	if err != nil {
		return fmt.Errorf("failed to list unprocessed events: %w", err)
	}

	if len(eventIDs) == 0 {
		return nil // 没有待处理事件
	}

	// 投递批量学习任务
	taskPayload := map[string]interface{}{
		"group_id":  groupID,
		"event_ids": eventIDs,
		"version":   s.extractorVersion,
	}

	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal task payload: %w", err)
	}

	idempotencyKey := fmt.Sprintf("learning-batch-%d-%s", groupID, eventIDs[0])

	if err := s.outbox.Enqueue(ctx, "learning_extract", idempotencyKey, payloadBytes); err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	return nil
}

// ListUnprocessedEvents 列出未处理的事件（用于管理后台）
func (s *NewService) ListUnprocessedEvents(ctx context.Context, groupID int64, limit int) ([]string, error) {
	memStore, ok := s.store.(memorydomain.Store)
	if !ok {
		return nil, fmt.Errorf("store does not support memory interface")
	}

	return memStore.ListUnprocessedEvents(ctx, groupID, s.extractorVersion, limit)
}

