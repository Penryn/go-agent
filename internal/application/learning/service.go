package learning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	memsvc "github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/runtime/scheduler"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// stopWords 是停用词集合，gram 首 rune 或末 rune 在此集合中时跳过该子串。
var stopWords = map[rune]struct{}{
	'的': {}, '了': {}, '啊': {}, '吧': {}, '呢': {},
	'吗': {}, '呀': {}, '哦': {}, '嗯': {}, '哈': {},
	'就': {}, '也': {}, '都': {}, '这': {}, '那': {},
	'是': {}, '在': {}, '我': {}, '你': {}, '他': {},
	'她': {}, '它': {}, '们': {}, '么': {}, '个': {},
}

// topicSuffixes 是话题关键词辅助识别白名单，命中后缀的 2-4 字 gram 也归入 topic_keyword。
var topicSuffixes = []string{"事件", "问题", "方向", "争议", "热点", "新闻", "漏洞", "更新", "功能", "方案"}

type phraseStats struct {
	count    int
	senders  map[int64]struct{}
	eventIDs []string
}

type Input struct {
	GroupID int64
	Events  []conversationdomain.ConversationEvent
}

type Output struct {
	Candidates []memorydomain.LearningCandidate
}

type Service struct {
	store      ports.MemoryStore
	state      ports.LearningStateStore
	candidates ports.LearningCandidateStore
	mem        *memsvc.Service
	outbox     ports.TaskSubmitter
}

type Option func(*Service)

func WithOutbox(runtime ports.TaskSubmitter) Option {
	return func(s *Service) { s.outbox = runtime }
}

func New(_ context.Context, store ports.MemoryStore, state ports.LearningStateStore, mem *memsvc.Service, opts ...Option) (*Service, error) {
	candidates, _ := state.(ports.LearningCandidateStore)
	service := &Service{store: store, state: state, candidates: candidates, mem: mem}
	for _, opt := range opts {
		opt(service)
	}
	return service, nil
}

func (s *Service) Run(ctx context.Context, input Input) (Output, error) {
	out, err := extractCandidates(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return out, nil
}

// RegisterJobs 向调度器注册学习相关定时任务，模式与 persona.Service.RegisterJobs 一致。
func (s *Service) RegisterJobs(sched *scheduler.Scheduler, groupIDs []int64) {
	sched.Register("learning_extract", 6*time.Hour, s.learnAllGroups(groupIDs))
}

func (s *Service) learnAllGroups(groupIDs []int64) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		for _, gid := range groupIDs {
			if s.outbox == nil {
				return fmt.Errorf("learning: outbox is not configured")
			}
			payload, err := json.Marshal(struct {
				GroupID int64 `json:"group_id"`
			}{GroupID: gid})
			if err != nil {
				return err
			}
			key := fmt.Sprintf("%d-%d", gid, time.Now().Unix()/(6*60*60))
			if err := s.outbox.Enqueue(ctx, "learning_extract", key, payload); err != nil {
				return err
			}
		}
		return nil
	}
}

// ProcessGroup executes one durable learning task.
func (s *Service) ProcessGroup(ctx context.Context, groupID int64) error {
	return s.learnGroup(ctx, groupID)
}

func (s *Service) learnGroup(ctx context.Context, groupID int64) error {
	if s.state == nil {
		return fmt.Errorf("learning: watermark store is nil")
	}
	watermark, err := s.state.GetLearningWatermark(ctx, groupID, "learning_extract")
	if err != nil {
		return err
	}
	after := watermark.OccurredAt
	if after.IsZero() {
		after = time.Unix(0, 0)
	}
	events, err := s.state.EventsAfter(ctx, groupID, after, watermark.EventID, 200)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	out, err := s.Run(ctx, Input{GroupID: groupID, Events: events})
	if err != nil {
		return err
	}
	if len(out.Candidates) > 0 {
		if s.candidates != nil {
			existing, err := s.candidates.ListLearningCandidates(ctx, groupID, 200)
			if err != nil {
				return err
			}
			byID := make(map[string]memorydomain.LearningCandidate, len(existing))
			for _, candidate := range existing {
				byID[candidate.ID] = candidate
			}
			for _, candidate := range out.Candidates {
				candidate = mergeCandidateEvidence(byID[candidate.ID], candidate)
				candidate.Status = "staged"
				if err := s.candidates.UpsertLearningCandidate(ctx, candidate); err != nil {
					return err
				}
				byID[candidate.ID] = candidate
			}
			staged, err := s.candidates.ListLearningCandidates(ctx, groupID, 200)
			if err != nil {
				return err
			}
			if err := s.applyLearning(ctx, staged); err != nil {
				return err
			}
		} else if err := s.applyLearning(ctx, out.Candidates); err != nil {
			return err
		}
	}
	last := events[len(events)-1]
	return s.state.SaveLearningWatermark(ctx, memorydomain.LearningWatermark{
		GroupID:    groupID,
		Kind:       "learning_extract",
		OccurredAt: time.Unix(last.TimestampUnix, 0),
		EventID:    last.EventID,
		UpdatedAt:  time.Now(),
	})
}

func mergeCandidateEvidence(existing, incoming memorydomain.LearningCandidate) memorydomain.LearningCandidate {
	if existing.ID == "" {
		return incoming
	}
	seen := make(map[string]struct{}, len(existing.ExampleEventIDs)+len(incoming.ExampleEventIDs))
	for _, id := range existing.ExampleEventIDs {
		if id != "" {
			seen[id] = struct{}{}
		}
	}
	newEvidence := 0
	mergedIDs := append([]string(nil), existing.ExampleEventIDs...)
	for _, id := range incoming.ExampleEventIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		newEvidence++
		if len(mergedIDs) < 32 {
			mergedIDs = append(mergedIDs, id)
		}
	}
	if newEvidence == 0 {
		incoming.EvidenceCount = existing.EvidenceCount
	} else {
		incoming.EvidenceCount = existing.EvidenceCount + newEvidence
	}
	incoming.ExampleEventIDs = mergedIDs
	if incoming.Confidence < existing.Confidence {
		incoming.Confidence = existing.Confidence
	}
	if incoming.Meaning == "" {
		incoming.Meaning = existing.Meaning
	}
	incoming.CreatedAt = existing.CreatedAt
	return incoming
}

// applyLearning 把置信度达标的学习候选写入长期记忆。MemoryID 由
// 「群+类型+值」三元组哈希而来，同一事实重复学习时覆盖而非新增；
// TargetUserID 为 0 记群级 scope，非零记用户级 scope。
func (s *Service) applyLearning(ctx context.Context, candidates []memorydomain.LearningCandidate) error {
	for _, candidate := range candidates {
		if candidate.Confidence < 0.7 {
			continue
		}
		raw := fmt.Sprintf("learning-%d-%s-%s", candidate.GroupID, candidate.Kind, candidate.Value)
		sum := sha256.Sum256([]byte(raw))
		scope := fmt.Sprintf("group:%d", candidate.GroupID)
		if candidate.TargetUserID != 0 {
			scope = fmt.Sprintf("group:%d:user:%d", candidate.GroupID, candidate.TargetUserID)
		}
		evidenceEventID := ""
		if len(candidate.ExampleEventIDs) > 0 {
			evidenceEventID = candidate.ExampleEventIDs[0]
		}
		if _, err := s.mem.MarkIntent(ctx, memsvc.WriteIntent{
			MemoryID:       fmt.Sprintf("memory-%x", sum[:8]),
			Scope:          scope,
			MemoryType:     candidate.Kind,
			Subject:        candidate.Value,
			Content:        candidate.Meaning,
			SourceEventID:  evidenceEventID,
			SourceEventIDs: append([]string(nil), candidate.ExampleEventIDs...),
			Importance:     float64(candidate.EvidenceCount) / 20,
			Confidence:     candidate.Confidence,
		}); err != nil {
			return err
		}
		if s.candidates != nil {
			if err := s.candidates.UpdateLearningCandidateStatus(ctx, candidate.ID, "promoted"); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractCandidates(_ context.Context, input Input) (Output, error) {
	// counter 按短语统计，记录出现次数和发言人集合（用于去重）。
	counter := map[string]*phraseStats{}
	// userCounter 按用户分组统计，用于提取 user_catchphrase。
	userCounter := map[int64]map[string]*phraseStats{}
	// replyTexts/memeTexts 同时保留证据事件，供 candidate evidence ledger 去重。
	replyTexts := map[string]*phraseStats{}
	memeTexts := map[string]*phraseStats{}
	// behaviorTexts 记录明确的互动偏好或纠正语句；这类信号即使只出现一次
	// 也有较高信息量，作为行为学习候选进入同一生命周期。
	behaviorTexts := map[string]*phraseStats{}

	for i, event := range input.Events {
		text := strings.TrimSpace(event.Text)
		if kind, ok := behaviorSignal(text); ok {
			key := kind + "\x00" + text
			stats := behaviorTexts[key]
			if stats == nil {
				stats = &phraseStats{senders: map[int64]struct{}{}}
				behaviorTexts[key] = stats
			}
			stats.count++
			stats.senders[event.UserID] = struct{}{}
			if event.EventID != "" && len(stats.eventIDs) < 32 {
				stats.eventIDs = append(stats.eventIDs, event.EventID)
			}
		}

		// 统计群级 n-gram
		extractNgrams(text, event.UserID, event.EventID, counter)

		// 统计用户级 n-gram（user_catchphrase）
		if event.UserID != 0 {
			if userCounter[event.UserID] == nil {
				userCounter[event.UserID] = map[string]*phraseStats{}
			}
			extractNgrams(text, event.UserID, event.EventID, userCounter[event.UserID])
		}

		// 提取回复套路前置文本（reaction_pattern/conversation）
		if event.ReplyToMessageID != "" && text != "" {
			stats := replyTexts[text]
			if stats == nil {
				stats = &phraseStats{senders: map[int64]struct{}{}}
				replyTexts[text] = stats
			}
			stats.count++
			if event.EventID != "" && len(stats.eventIDs) < 32 {
				stats.eventIDs = append(stats.eventIDs, event.EventID)
			}
		}

		// 提取触发图片/sticker 的前置文本（reaction_pattern/meme_trigger）
		hasMedia := false
		for _, att := range event.Attachments {
			if att.Kind == mediadomain.MediaImage || att.Kind == mediadomain.MediaSticker {
				hasMedia = true
				break
			}
		}
		if hasMedia && i > 0 {
			prevText := strings.TrimSpace(input.Events[i-1].Text)
			if len([]rune(prevText)) >= 2 && len([]rune(prevText)) <= 20 {
				stats := memeTexts[prevText]
				if stats == nil {
					stats = &phraseStats{senders: map[int64]struct{}{}}
					memeTexts[prevText] = stats
				}
				stats.count++
				if event.EventID != "" && len(stats.eventIDs) < 32 {
					stats.eventIDs = append(stats.eventIDs, event.EventID)
				}
			}
		}
	}

	output := Output{}
	// emit 是四类候选共用的构造点：字段完全同构，只差 ID 前缀和语义标注。
	emit := func(_ string, kind, value, meaning string, evidence int, eventIDs []string, conf float64, targetUser int64) {
		raw := fmt.Sprintf("candidate-%d-%d-%s-%s", input.GroupID, targetUser, kind, value)
		sum := sha256.Sum256([]byte(raw))
		output.Candidates = append(output.Candidates, memorydomain.LearningCandidate{
			ID:              fmt.Sprintf("candidate-%x", sum[:8]),
			GroupID:         input.GroupID,
			Kind:            kind,
			Value:           value,
			Meaning:         meaning,
			EvidenceCount:   evidence,
			ExampleEventIDs: eventIDs,
			Confidence:      conf,
			Status:          "pending",
			CreatedAt:       time.Now(),
			TargetUserID:    targetUser,
		})
	}

	// group_slang / topic_keyword（按 n-gram 长度区分）
	for phrase, stats := range counter {
		if stats.count < 3 || len(stats.senders) < 2 {
			continue
		}
		conf := math.Min(1.0, 0.5+float64(len(stats.senders))/10+float64(stats.count)/20)
		kind, meaning := "group_slang", "群内高频表达"
		if len([]rune(phrase)) >= 5 || hasTopicSuffix(phrase) {
			kind, meaning = "topic_keyword", "群内流行话题关键词"
		}
		emit("candidate-", kind, phrase, meaning, stats.count, stats.eventIDs, conf, 0)
	}

	// user_catchphrase（按用户分组，阈值 count>=3）
	for uid, uc := range userCounter {
		for phrase, stats := range uc {
			if stats.count < 3 {
				continue
			}
			conf := math.Min(1.0, 0.5+float64(stats.count)/10)
			emit("candidate-user-", "user_catchphrase", phrase, "用户个人口头禅", stats.count, stats.eventIDs, conf, uid)
		}
	}

	// reaction_pattern/conversation（回复套路，count>=2）
	for text, stats := range replyTexts {
		if stats.count < 2 {
			continue
		}
		emit("candidate-reply-", "reaction_pattern", text, "[conversation] 群内高频回复套路", stats.count, stats.eventIDs, math.Min(1.0, 0.5+float64(stats.count)/10), 0)
	}

	// reaction_pattern/meme_trigger（触发图片的前置文本，count>=2）
	for text, stats := range memeTexts {
		if stats.count < 2 {
			continue
		}
		emit("candidate-meme-", "reaction_pattern", text, "[meme_trigger] 触发表情包发送的上文", stats.count, stats.eventIDs, math.Min(1.0, 0.5+float64(stats.count)/10), 0)
	}

	// behavior_rule / behavior_correction capture explicit interaction policy,
	// which is stronger evidence than frequency and can promote after one event.
	for key, stats := range behaviorTexts {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 || stats.count == 0 {
			continue
		}
		meaning := "显式互动行为偏好"
		if parts[0] == "behavior_correction" {
			meaning = "显式纠正信号，后续行为应避免重复"
		}
		conf := math.Min(1.0, 0.8+float64(stats.count-1)/10)
		emit("candidate-behavior-", parts[0], parts[1], meaning, stats.count, stats.eventIDs, conf, 0)
	}

	return output, nil
}

func behaviorSignal(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	for _, marker := range []string{"不对", "不是", "更正", "纠正", "应该是"} {
		if strings.Contains(text, marker) {
			return "behavior_correction", true
		}
	}
	for _, prefix := range []string{"以后", "请", "不要", "别再", "别", "记得"} {
		if strings.HasPrefix(text, prefix) && len([]rune(text)) >= len([]rune(prefix))+2 {
			return "behavior_rule", true
		}
	}
	return "", false
}

// extractNgrams 从 text 中提取 2-8 字的 n-gram 子串，更新到 counter 中。
func extractNgrams(text string, senderID int64, eventID string, counter map[string]*phraseStats) {
	runes := []rune(text)
	n := len(runes)
	for length := 2; length <= 8; length++ {
		for start := 0; start+length <= n; start++ {
			gram := string(runes[start : start+length])
			first := runes[start]
			last := runes[start+length-1]
			// 首尾含停用词则跳过
			if _, ok := stopWords[first]; ok {
				continue
			}
			if _, ok := stopWords[last]; ok {
				continue
			}
			if _, ok := counter[gram]; !ok {
				counter[gram] = &phraseStats{senders: map[int64]struct{}{}}
			}
			counter[gram].count++
			counter[gram].senders[senderID] = struct{}{}
			if eventID != "" && len(counter[gram].eventIDs) < 32 {
				counter[gram].eventIDs = append(counter[gram].eventIDs, eventID)
			}
		}
	}
}

// hasTopicSuffix 检查 phrase 是否以话题关键词后缀结尾。
func hasTopicSuffix(phrase string) bool {
	for _, suffix := range topicSuffixes {
		if strings.HasSuffix(phrase, suffix) {
			return true
		}
	}
	return false
}
