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
	store  ports.MemoryStore
	state  ports.LearningStateStore
	mem    *memsvc.Service
	outbox interface {
		Enqueue(context.Context, string, string, []byte) error
	}
}

type Option func(*Service)

func WithOutbox(runtime interface {
	Enqueue(context.Context, string, string, []byte) error
}) Option {
	return func(s *Service) { s.outbox = runtime }
}

func New(_ context.Context, store ports.MemoryStore, state ports.LearningStateStore, mem *memsvc.Service, opts ...Option) (*Service, error) {
	service := &Service{store: store, state: state, mem: mem}
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
	return filterCandidates(out), nil
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
	if len(events) < 10 {
		return nil
	}
	out, err := s.Run(ctx, Input{GroupID: groupID, Events: events})
	if err != nil {
		return err
	}
	if len(out.Candidates) > 0 {
		if err := s.applyLearning(ctx, out.Candidates); err != nil {
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
			MemoryID:      fmt.Sprintf("memory-%x", sum[:8]),
			Scope:         scope,
			MemoryType:    candidate.Kind,
			Subject:       candidate.Value,
			Content:       candidate.Meaning,
			SourceEventID: evidenceEventID,
			Importance:    float64(candidate.EvidenceCount) / 20,
			Confidence:    candidate.Confidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

func extractCandidates(_ context.Context, input Input) (Output, error) {
	// counter 按短语统计，记录出现次数和发言人集合（用于去重）。
	counter := map[string]*phraseStats{}
	// userCounter 按用户分组统计，用于提取 user_catchphrase。
	userCounter := map[int64]map[string]*phraseStats{}
	// replyTexts 记录回复消息的前置文本，用于提取 reaction_pattern/conversation。
	replyTexts := map[string]int{}
	// memeTexts 记录图片/sticker 前的文本，用于提取 reaction_pattern/meme_trigger。
	memeTexts := map[string]int{}

	for i, event := range input.Events {
		text := strings.TrimSpace(event.Text)

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
			replyTexts[text]++
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
				memeTexts[prevText]++
			}
		}
	}

	output := Output{}

	// group_slang / topic_keyword（按 n-gram 长度区分）
	for phrase, stats := range counter {
		if stats.count < 3 || len(stats.senders) < 2 {
			continue
		}
		conf := math.Min(1.0, 0.5+float64(len(stats.senders))/10+float64(stats.count)/20)
		runeLen := len([]rune(phrase))
		kind := "group_slang"
		if runeLen >= 5 || hasTopicSuffix(phrase) {
			kind = "topic_keyword"
		}
		meaning := "群内高频表达"
		if kind == "topic_keyword" {
			meaning = "群内流行话题关键词"
		}
		output.Candidates = append(output.Candidates, memorydomain.LearningCandidate{
			ID:              "candidate-" + phrase,
			GroupID:         input.GroupID,
			Kind:            kind,
			Value:           phrase,
			Meaning:         meaning,
			EvidenceCount:   stats.count,
			ExampleEventIDs: stats.eventIDs,
			Confidence:      conf,
			Status:          "pending",
			CreatedAt:       time.Now(),
		})
	}

	// user_catchphrase（按用户分组，阈值 count>=3）
	for uid, uc := range userCounter {
		for phrase, stats := range uc {
			if stats.count < 3 {
				continue
			}
			conf := math.Min(1.0, 0.5+float64(stats.count)/10)
			output.Candidates = append(output.Candidates, memorydomain.LearningCandidate{
				ID:              "candidate-user-" + phrase,
				GroupID:         input.GroupID,
				Kind:            "user_catchphrase",
				Value:           phrase,
				Meaning:         "用户个人口头禅",
				EvidenceCount:   stats.count,
				ExampleEventIDs: stats.eventIDs,
				Confidence:      conf,
				Status:          "pending",
				CreatedAt:       time.Now(),
				TargetUserID:    uid,
			})
		}
	}

	// reaction_pattern/conversation（回复套路，count>=2）
	for text, count := range replyTexts {
		if count < 2 {
			continue
		}
		conf := math.Min(1.0, 0.5+float64(count)/10)
		output.Candidates = append(output.Candidates, memorydomain.LearningCandidate{
			ID:            "candidate-reply-" + text,
			GroupID:       input.GroupID,
			Kind:          "reaction_pattern",
			Value:         text,
			Meaning:       "[conversation] 群内高频回复套路",
			EvidenceCount: count,
			Confidence:    conf,
			Status:        "pending",
			CreatedAt:     time.Now(),
		})
	}

	// reaction_pattern/meme_trigger（触发图片的前置文本，count>=2）
	for text, count := range memeTexts {
		if count < 2 {
			continue
		}
		conf := math.Min(1.0, 0.5+float64(count)/10)
		output.Candidates = append(output.Candidates, memorydomain.LearningCandidate{
			ID:            "candidate-meme-" + text,
			GroupID:       input.GroupID,
			Kind:          "reaction_pattern",
			Value:         text,
			Meaning:       "[meme_trigger] 触发表情包发送的上文",
			EvidenceCount: count,
			Confidence:    conf,
			Status:        "pending",
			CreatedAt:     time.Now(),
		})
	}

	return output, nil
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
			if eventID != "" && len(counter[gram].eventIDs) < 3 {
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

// filterCandidates 丢弃置信度不足的候选（与 applyLearning 的阈值一致）。
func filterCandidates(input Output) Output {
	filtered := input.Candidates[:0]
	for _, candidate := range input.Candidates {
		if candidate.Confidence >= 0.7 {
			filtered = append(filtered, candidate)
		}
	}
	input.Candidates = filtered
	return input
}
