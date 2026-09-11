package learning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	memsvc "github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/runtime/scheduler"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

const (
	defaultExtractorVersion = "window-v1"
	defaultWindowSize       = 60
)

type LLMProvider interface {
	Generate(context.Context, string) (string, error)
}

type learningProgressStore interface {
	EventsByIDs(context.Context, int64, []string) ([]conversationdomain.ConversationEvent, error)
	ListUnprocessedEvents(context.Context, int64, string, int) ([]string, error)
	MarkLearningProgress(context.Context, memorydomain.LearningEventProgress) error
}

type BatchTask struct {
	GroupID          int64    `json:"group_id"`
	EventIDs         []string `json:"event_ids"`
	ExtractorVersion string   `json:"extractor_version"`
}

type Window struct {
	GroupID   int64
	Events    []conversationdomain.ConversationEvent
	StartTime time.Time
	EndTime   time.Time
}

type Service struct {
	progress         learningProgressStore
	mem              *memsvc.Service
	outbox           ports.TaskSubmitter
	llm              LLMProvider
	promptBuilder    *ExtractionPromptBuilder
	extractorVersion string
	maxWindowSize    int
}

type Option func(*Service)

func WithOutbox(runtime ports.TaskSubmitter) Option {
	return func(s *Service) { s.outbox = runtime }
}

func WithLLM(llm LLMProvider) Option {
	return func(s *Service) { s.llm = llm }
}

func New(_ context.Context, store ports.MemoryStore, state ports.LearningEventStore, mem *memsvc.Service, opts ...Option) (*Service, error) {
	progress, ok := state.(learningProgressStore)
	if !ok {
		progress, ok = store.(learningProgressStore)
	}
	if !ok {
		return nil, fmt.Errorf("learning: progress store is not configured")
	}
	service := &Service{
		progress:         progress,
		mem:              mem,
		extractorVersion: defaultExtractorVersion,
		maxWindowSize:    defaultWindowSize,
	}
	for _, opt := range opts {
		opt(service)
	}
	service.promptBuilder = NewExtractionPromptBuilder(service.extractorVersion)
	return service, nil
}

func (s *Service) RegisterJobs(sched *scheduler.Scheduler, groupIDs []int64) {
	sched.Register("learning_scan", 2*time.Minute, func(ctx context.Context) error {
		for _, groupID := range groupIDs {
			if err := s.ScanAndSchedule(ctx, groupID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) ScanAndSchedule(ctx context.Context, groupID int64) error {
	if s.outbox == nil {
		return fmt.Errorf("learning: outbox is not configured")
	}
	eventIDs, err := s.progress.ListUnprocessedEvents(ctx, groupID, s.extractorVersion, s.maxWindowSize)
	if err != nil || len(eventIDs) == 0 {
		return err
	}
	task := BatchTask{GroupID: groupID, EventIDs: eventIDs, ExtractorVersion: s.extractorVersion}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	keyHash := sha256.Sum256([]byte(strings.Join(eventIDs, "\x00")))
	return s.outbox.Enqueue(ctx, "learning_extract", fmt.Sprintf("%d-%s-%x", groupID, s.extractorVersion, keyHash[:8]), payload)
}

func (s *Service) ProcessTask(ctx context.Context, payload []byte) error {
	var task BatchTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return fmt.Errorf("decode learning task: %w", err)
	}
	if task.ExtractorVersion != s.extractorVersion {
		return fmt.Errorf("learning: extractor version %q is not active", task.ExtractorVersion)
	}
	return s.ProcessBatch(ctx, task.GroupID, task.EventIDs)
}

func (s *Service) ProcessBatch(ctx context.Context, groupID int64, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	events, err := s.progress.EventsByIDs(ctx, groupID, eventIDs)
	if err != nil {
		return fmt.Errorf("load learning events: %w", err)
	}
	if len(events) != len(eventIDs) {
		return fmt.Errorf("learning: loaded %d of %d events", len(events), len(eventIDs))
	}

	inbound := make([]conversationdomain.ConversationEvent, 0, len(events))
	for _, event := range events {
		if event.Origin == "outbound" {
			if err := s.markProgress(ctx, groupID, event.EventID, "skipped", "policy_excluded", 0); err != nil {
				return err
			}
			continue
		}
		inbound = append(inbound, event)
	}
	if len(inbound) == 0 {
		return nil
	}
	if s.llm == nil {
		return fmt.Errorf("learning: llm is not configured")
	}

	window := buildWindow(groupID, inbound)
	response, err := s.llm.Generate(ctx, s.promptBuilder.BuildPrompt(window))
	if err != nil {
		return fmt.Errorf("extract learning window: %w", err)
	}
	candidates, err := s.promptBuilder.ParseExtractionResponse(response, window)
	if err != nil {
		return err
	}

	eventsByID := make(map[string]conversationdomain.ConversationEvent, len(inbound))
	for _, event := range inbound {
		eventsByID[event.EventID] = event
	}
	successCount := 0
	for _, candidate := range candidates {
		if err := validateCandidateEvidence(candidate, groupID, eventsByID); err != nil {
			continue
		}
		if s.mem == nil {
			return fmt.Errorf("learning: memory service is not configured")
		}
		if _, err := s.mem.MarkIntent(ctx, candidateIntent(candidate)); err != nil {
			return err
		}
		successCount++
	}
	for _, event := range inbound {
		if err := s.markProgress(ctx, groupID, event.EventID, "completed", "", successCount); err != nil {
			return err
		}
	}
	return nil
}

func buildWindow(groupID int64, events []conversationdomain.ConversationEvent) *Window {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].TimestampUnix == events[j].TimestampUnix {
			return events[i].EventID < events[j].EventID
		}
		return events[i].TimestampUnix < events[j].TimestampUnix
	})
	window := &Window{GroupID: groupID, Events: events}
	if len(events) > 0 {
		window.StartTime = time.Unix(events[0].TimestampUnix, 0)
		window.EndTime = time.Unix(events[len(events)-1].TimestampUnix, 0)
	}
	return window
}

func validateCandidateEvidence(candidate *memorydomain.MemoryCandidate, groupID int64, events map[string]conversationdomain.ConversationEvent) error {
	if err := memorydomain.ValidateCandidate(candidate); err != nil {
		return err
	}
	if candidate.Scope != fmt.Sprintf("group:%d", groupID) {
		return fmt.Errorf("scope does not match group")
	}
	latest := int64(0)
	for _, eventID := range candidate.EvidenceEventIDs {
		event, ok := events[eventID]
		if !ok || event.Origin == "outbound" {
			return fmt.Errorf("invalid evidence event %q", eventID)
		}
		if candidate.SubjectKind == memorydomain.SubjectKindUser && candidate.SubjectID != strconv.FormatInt(event.UserID, 10) {
			return fmt.Errorf("subject is not the evidence author")
		}
		latest = max(latest, event.TimestampUnix)
	}
	if candidate.SubjectKind == memorydomain.SubjectKindGroup && candidate.SubjectID != strconv.FormatInt(groupID, 10) {
		return fmt.Errorf("group subject does not match scope")
	}
	candidate.ObservedAt = time.Unix(latest, 0)
	return nil
}

func candidateIntent(candidate *memorydomain.MemoryCandidate) memsvc.WriteIntent {
	evidence := append([]string(nil), candidate.EvidenceEventIDs...)
	sort.Strings(evidence)
	raw := strings.Join([]string{candidate.Scope, string(candidate.SubjectKind), candidate.SubjectID, string(candidate.Type), candidate.Content, strings.Join(evidence, ",")}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	sourceEventID := ""
	if len(evidence) > 0 {
		sourceEventID = evidence[0]
	}
	return memsvc.WriteIntent{
		MemoryID: fmt.Sprintf("memory-%x", sum[:12]), Scope: candidate.Scope,
		MemoryType: string(candidate.Type), Subject: candidate.SubjectID, Content: candidate.Content,
		SourceEventID: sourceEventID, SourceEventIDs: evidence, Origin: "extraction",
		Importance: 0.7, Confidence: 0.9,
	}
}

func (s *Service) markProgress(ctx context.Context, groupID int64, eventID, outcome, reason string, memoryCount int) error {
	now := time.Now()
	return s.progress.MarkLearningProgress(ctx, memorydomain.LearningEventProgress{
		EventID: eventID, ExtractorVersion: s.extractorVersion, GroupID: groupID,
		ProcessedAt: now, Outcome: outcome, SkipReason: reason, MemoryCount: memoryCount, CreatedAt: now,
	})
}
