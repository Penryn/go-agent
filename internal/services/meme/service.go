package meme

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	retrievalsvc "github.com/phlin/go-agent/internal/services/retrieval"
)

type Service struct {
	store       ports.MemeStore
	vectorStore ports.VectorMemeStore
	cfg         config.MemeConfig
	retriever   *retrievalsvc.Service
	outbox      interface {
		Enqueue(context.Context, string, string, []byte) error
	}
	// 质量反馈：发送后记录观察起点，群消息到达时判定该表情是否哑弹
	pendingMu    sync.Mutex
	pendingWatch map[string]time.Time // memeID -> 发送时刻
}

// dudObservationWindow 是表情发出后等待群反应的窗口：窗口内群里没有任何
// 新消息视为哑弹（没人接梗）。
const dudObservationWindow = 5 * time.Minute

// markSentAt 记录表情发送时刻，开启哑弹观察。
func (s *Service) markSentAt(memeID string) {
	s.pendingMu.Lock()
	if s.pendingWatch == nil {
		s.pendingWatch = make(map[string]time.Time)
	}
	s.pendingWatch[memeID] = time.Now()
	s.pendingMu.Unlock()
}

// settleDudsOnActivity 在群新消息到达时调用：活跃说明表情没把天聊死，
// 清掉观察；发送后一直冷场到窗口期满的记一次哑弹。
func (s *Service) settleDudsOnActivity(ctx context.Context, now time.Time) {
	s.pendingMu.Lock()
	var duds []string
	for id, sentAt := range s.pendingWatch {
		if now.Sub(sentAt) >= dudObservationWindow {
			duds = append(duds, id)
		}
		delete(s.pendingWatch, id) // 群活跃了：不管有没有到期，这次发送都不算哑弹
	}
	s.pendingMu.Unlock()
	for _, id := range duds {
		if err := s.store.MarkMemeDud(ctx, id); err != nil {
			slog.Debug("meme: mark dud failed", "meme_id", id, "err", err)
		}
	}
}

// sweepExpiredDuds 由检索路径顺带触发：发送后群彻底没人说话（连
// ObserveEvent 都没来），在下次检索时兜底结算。
func (s *Service) sweepExpiredDuds(ctx context.Context, now time.Time) {
	s.pendingMu.Lock()
	var duds []string
	for id, sentAt := range s.pendingWatch {
		if now.Sub(sentAt) >= dudObservationWindow {
			duds = append(duds, id)
			delete(s.pendingWatch, id)
		}
	}
	s.pendingMu.Unlock()
	for _, id := range duds {
		if err := s.store.MarkMemeDud(ctx, id); err != nil {
			slog.Debug("meme: mark dud failed", "meme_id", id, "err", err)
		}
	}
}

// New 创建 MemeService，cfg 控制自动收集、配额、冷却等行为。
func New(store ports.MemeStore, cfg config.MemeConfig, opts ...Option) *Service {
	svc := &Service{store: store, cfg: cfg}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Option 是 MemeService 的可选配置函数。
type Option func(*Service)

// WithVectorStore 注入 VectorMemeStore，启用语义向量搜索。
// 未注入时降级为纯关键词搜索。
func WithVectorStore(vs ports.VectorMemeStore) Option {
	return func(s *Service) { s.vectorStore = vs }
}

func WithRetriever(retriever *retrievalsvc.Service) Option {
	return func(s *Service) { s.retriever = retriever }
}

func WithOutbox(runtime interface {
	Enqueue(context.Context, string, string, []byte) error
}) Option {
	return func(s *Service) { s.outbox = runtime }
}

type VectorIndexTask struct {
	MemeID   string `json:"meme_id"`
	Text     string `json:"text"`
	GroupID  int64  `json:"group_id"`
	Revision int64  `json:"revision"`
}

// ObserveEvent 观察群聊事件，将图片/贴纸收入表情包库。
// descriptors 是 visionSvc.Understand 已经计算好的视觉描述，可为 nil（降级到默认描述符）。
func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent, descriptors []mediadomain.MediaDescriptor) error {
	if !s.cfg.AutoCollect {
		return nil
	}

	// 质量反馈：群里来了新消息，说明之前发的表情没有把天聊死，
	// 结算观察窗内挂着的表情；超窗冷场的记一次哑弹。
	s.settleDudsOnActivity(ctx, time.Now())

	// D-3: per_group_limit 写入前清理超额记录
	if s.cfg.PerGroupLimit > 0 {
		count, err := s.store.CountMemesByGroup(ctx, event.GroupID)
		if err != nil {
			slog.Warn("meme.ObserveEvent: CountMemesByGroup failed", "group_id", event.GroupID, "err", err)
		} else if count >= s.cfg.PerGroupLimit {
			excess := count - s.cfg.PerGroupLimit + 1
			if delErr := s.store.DeleteOldestMemes(ctx, event.GroupID, excess); delErr != nil {
				slog.Warn("meme.ObserveEvent: DeleteOldestMemes failed", "group_id", event.GroupID, "err", delErr)
			}
		}
	}

	for _, attachment := range event.Attachments {
		if attachment.Kind != mediadomain.MediaImage && attachment.Kind != mediadomain.MediaSticker {
			continue
		}
		descriptor, ok := descriptorFor(attachment.AttachmentID, descriptors)
		if !ok || !s.collectible(attachment, descriptor) {
			continue
		}

		memeID := buildMemeID(attachment)
		memeDescriptor := buildMemeDescriptor(memeID, attachment, []mediadomain.MediaDescriptor{descriptor}, s.cfg.CandidateThreshold)
		memeAsset := mediadomain.MemeAsset{
			MemeID:         memeID,
			GroupID:        event.GroupID,
			SourceEventID:  event.EventID,
			ObjectKey:      attachment.ObjectKey,
			FileExt:        fileExt(attachment.ObjectKey),
			ContentHash:    coalesce(attachment.ContentHash, memeID),
			PerceptualHash: coalesce(attachment.ContentHash, memeID),
			Width:          attachment.Width,
			Height:         attachment.Height,
			Animated:       strings.EqualFold(fileExt(attachment.ObjectKey), ".gif"),
			Status:         "approved",
			CreatedAt:      time.Now(),
		}

		// Vector indexing is durable background work when an outbox is configured.
		if s.vectorStore != nil {
			indexText := buildIndexText(memeDescriptor)
			groupID := event.GroupID
			if s.outbox != nil {
				revision := time.Now().UnixNano()
				memeAsset.Revision = revision
				idempotencyKey := fmt.Sprintf("%s:%d", memeID, revision)
				payload, marshalErr := json.Marshal(VectorIndexTask{MemeID: memeID, Text: indexText, GroupID: groupID, Revision: revision})
				if marshalErr == nil {
					if atomicStore, ok := s.store.(ports.AtomicMemeProjectionStore); ok {
						if err := atomicStore.UpsertMemeAndEnqueueVector(ctx, memeAsset, memeDescriptor, ports.OutboxTask{
							ID: "meme-vector-" + idempotencyKey, Kind: "meme_vector_index",
							IdempotencyKey: idempotencyKey, Payload: payload,
						}); err != nil {
							return err
						}
						continue
					}
					if err := s.store.UpsertMeme(ctx, memeAsset, memeDescriptor); err != nil {
						return err
					}
					if enqueueErr := s.outbox.Enqueue(ctx, "meme_vector_index", idempotencyKey, payload); enqueueErr == nil {
						continue
					} else {
						marshalErr = enqueueErr
					}
				}
				return fmt.Errorf("enqueue meme vector index: %w", marshalErr)
			}
			if err := s.store.UpsertMeme(ctx, memeAsset, memeDescriptor); err != nil {
				return err
			}
			if err := s.vectorStore.IndexMeme(ctx, memeID, indexText, groupID); err != nil {
				slog.Warn("meme: vector index failed", "meme_id", memeID, "err", err)
			}
			continue
		}
		if err := s.store.UpsertMeme(ctx, memeAsset, memeDescriptor); err != nil {
			return err
		}
	}
	return nil
}

// ProcessVectorIndex executes one durable meme vector indexing task.
func (s *Service) ProcessVectorIndex(ctx context.Context, task VectorIndexTask) error {
	if s == nil || s.vectorStore == nil {
		return fmt.Errorf("meme: vector store is not configured")
	}
	if versioned, ok := s.vectorStore.(ports.VersionedVectorMemeStore); ok {
		return versioned.IndexMemeVersioned(ctx, task.MemeID, task.Text, task.GroupID, task.Revision)
	}
	return s.vectorStore.IndexMeme(ctx, task.MemeID, task.Text, task.GroupID)
}

// Search 搜索表情包，向量优先；向量无结果时 fallback 到关键词搜索。
// 关键词搜索路径自动应用 RepeatCooldown 冷却过滤；若冷却期内无结果则放开冷却重试。
func (s *Service) Search(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	s.sweepExpiredDuds(ctx, time.Now())
	if query.TopK <= 0 {
		query.TopK = s.cfg.SearchTopK
	}
	if query.TopK <= 0 {
		query.TopK = 5
	}
	if s.retriever == nil {
		return nil, errors.New("meme: retriever is not configured")
	}
	results, err := s.retriever.SearchMemes(ctx, query)
	if err != nil {
		return nil, err
	}
	filtered, err := s.rankAndFilter(ctx, results, query)
	if err != nil || len(filtered) > 0 || strings.TrimSpace(query.Query) == "" {
		return filtered, err
	}
	fallback := query
	fallback.TopK = max(query.TopK*5, 20)
	results, err = s.store.SearchMemes(ctx, fallback)
	if err != nil {
		return nil, err
	}
	return s.rankAndFilter(ctx, results, query)
}

type rankedMeme struct {
	result mediadomain.MemeSearchResult
	asset  mediadomain.MemeAsset
	recent bool
}

func (s *Service) rankAndFilter(ctx context.Context, results []mediadomain.MemeSearchResult, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	cooldown, _ := time.ParseDuration(s.cfg.RepeatCooldown)
	cutoff := time.Now().Add(-cooldown)
	ranked := make([]rankedMeme, 0, len(results))
	for _, result := range results {
		asset, descriptor, err := s.store.GetMeme(ctx, result.MemeID)
		if err != nil || asset.Status != "approved" {
			continue
		}
		if !matchesTag(descriptor.EmotionTags, query.Emotion) || !matchesTag(descriptor.SceneTags, query.Scene) {
			continue
		}
		recent := cooldown > 0 && asset.LastSentAt != nil && !asset.LastSentAt.Before(cutoff)
		if query.ExcludeRecent && recent {
			continue
		}
		result.Descriptor = descriptor
		ranked = append(ranked, rankedMeme{result: result, asset: asset, recent: recent})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if s.cfg.PreferGroupScoped && (left.asset.GroupID == query.GroupID) != (right.asset.GroupID == query.GroupID) {
			return left.asset.GroupID == query.GroupID
		}
		if left.recent != right.recent {
			return !left.recent
		}
		if leftRate, rightRate := dudRate(left.asset), dudRate(right.asset); leftRate != rightRate {
			return leftRate < rightRate
		}
		return left.result.Score > right.result.Score
	})
	if len(ranked) > query.TopK {
		ranked = ranked[:query.TopK]
	}
	filtered := make([]mediadomain.MemeSearchResult, len(ranked))
	for i := range ranked {
		filtered[i] = ranked[i].result
	}
	return filtered, nil
}

func matchesTag(tags []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	if wanted == "" {
		return true
	}
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), wanted) {
			return true
		}
	}
	return false
}

func dudRate(asset mediadomain.MemeAsset) float64 {
	if asset.SendCount < 3 {
		return 0
	}
	return float64(asset.DudCount) / float64(asset.SendCount)
}

// BuildSendSegments 构建发送消息片段。
func (s *Service) BuildSendSegments(ctx context.Context, memeID string, replyToMessageID string, caption string) ([]conversationdomain.MessageSegment, error) {
	asset, _, err := s.store.GetMeme(ctx, memeID)
	if err != nil {
		return nil, err
	}
	if asset.Status != "approved" {
		return nil, fmt.Errorf("meme %s is not approved", memeID)
	}

	segments := make([]conversationdomain.MessageSegment, 0, 3)
	if replyToMessageID != "" {
		segments = append(segments, conversationdomain.MessageSegment{
			Type: "reply",
			Data: map[string]any{"id": replyToMessageID},
		})
	}
	segments = append(segments, conversationdomain.MessageSegment{
		Type: "image",
		Data: map[string]any{"file": asset.ObjectKey},
	})
	if strings.TrimSpace(caption) != "" {
		segments = append(segments, conversationdomain.MessageSegment{
			Type: "text",
			Data: map[string]any{"text": caption},
		})
	}
	return segments, nil
}

func descriptorFor(attachmentID string, descriptors []mediadomain.MediaDescriptor) (mediadomain.MediaDescriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.AttachmentID == attachmentID {
			return descriptor, true
		}
	}
	return mediadomain.MediaDescriptor{}, false
}

func (s *Service) collectible(attachment mediadomain.MultimodalAttachment, descriptor mediadomain.MediaDescriptor) bool {
	if descriptor.Confidence < s.cfg.CandidateThreshold || len(descriptor.SafetySignals) > 0 {
		return false
	}
	if attachment.Kind == mediadomain.MediaSticker {
		return true
	}
	return len(descriptor.MemeSignals) > 0 || len(descriptor.MemeKeywords) > 0 || containsFold(descriptor.SceneTags, "meme")
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			return true
		}
	}
	return false
}

// MarkSent 标记表情包已发送，更新发送计数和最后发送时间。
func (s *Service) MarkSent(ctx context.Context, memeID string) error {
	s.markSentAt(memeID)
	return s.store.MarkMemeSent(ctx, memeID)
}

// buildMemeDescriptor 将视觉理解结果映射为 MemeDescriptor。
// 当 descriptors 中找不到对应 attachment 时（视觉服务未启用或理解失败），回退到默认值。
// confidenceThreshold > 0 时，置信度不足的 vision 结果也走回退路径。
func buildMemeDescriptor(memeID string, attachment mediadomain.MultimodalAttachment, descriptors []mediadomain.MediaDescriptor, confidenceThreshold float64) mediadomain.MemeDescriptor {
	for _, d := range descriptors {
		if d.AttachmentID != attachment.AttachmentID {
			continue
		}
		// 置信度不足时回退到默认
		if confidenceThreshold > 0 && d.Confidence < confidenceThreshold {
			break
		}

		keywords := make([]string, 0, len(d.MemeKeywords)+len(d.OCRTexts))
		keywords = append(keywords, d.MemeKeywords...)
		keywords = append(keywords, d.OCRTexts...)
		if len(keywords) == 0 {
			keywords = []string{string(attachment.Kind)}
		}

		emotionTags := d.EmotionHints
		if len(emotionTags) == 0 {
			emotionTags = []string{"neutral"}
		}

		sceneTags := d.SceneTags
		if len(sceneTags) == 0 {
			sceneTags = []string{"group"}
		}

		title := summaryTitle(d.Summary, d.OCRTexts)
		usageHints := d.MemeSignals
		if len(usageHints) == 0 {
			usageHints = []string{"回复时可作为图梗素材"}
		}

		return mediadomain.MemeDescriptor{
			MemeID:      memeID,
			Title:       title,
			Summary:     d.Summary,
			Keywords:    keywords,
			EmotionTags: emotionTags,
			SceneTags:   sceneTags,
			UsageHints:  usageHints,
			Language:    "zh",
			Confidence:  d.Confidence,
			Reviewed:    false,
			UpdatedAt:   time.Now(),
		}
	}

	// 默认回退描述符
	return mediadomain.MemeDescriptor{
		MemeID:      memeID,
		Title:       "群聊图片",
		Summary:     fmt.Sprintf("来自群聊的%s素材", attachment.Kind),
		Keywords:    []string{string(attachment.Kind), "群聊"},
		EmotionTags: []string{"neutral"},
		SceneTags:   []string{"group"},
		UsageHints:  []string{"回复时可作为图梗素材"},
		Language:    "zh",
		Confidence:  0.4,
		Reviewed:    true,
		UpdatedAt:   time.Now(),
	}
}

// summaryTitle 从视觉摘要或 OCR 文字中提取简短标题（最多20字符）。
func summaryTitle(summary string, ocrTexts []string) string {
	if len(ocrTexts) > 0 {
		t := ocrTexts[0]
		if len([]rune(t)) > 20 {
			t = string([]rune(t)[:20])
		}
		return t
	}
	if summary != "" {
		t := summary
		if len([]rune(t)) > 20 {
			t = string([]rune(t)[:20])
		}
		return t
	}
	return "群聊图片"
}

func buildMemeID(attachment mediadomain.MultimodalAttachment) string {
	if attachment.ContentHash != "" {
		return "meme-" + attachment.ContentHash
	}
	hash := sha1.Sum([]byte(attachment.ObjectKey + attachment.URL))
	return "meme-" + hex.EncodeToString(hash[:8])
}

func fileExt(key string) string {
	if ext := path.Ext(key); ext != "" {
		return ext
	}
	return ".bin"
}

func coalesce(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

// buildIndexText 将 MemeDescriptor 的文字字段拼接为向量索引文本。
func buildIndexText(d mediadomain.MemeDescriptor) string {
	parts := make([]string, 0, 6)
	if d.Title != "" {
		parts = append(parts, d.Title)
	}
	if d.Summary != "" {
		parts = append(parts, d.Summary)
	}
	if len(d.Keywords) > 0 {
		parts = append(parts, strings.Join(d.Keywords, " "))
	}
	if len(d.EmotionTags) > 0 {
		parts = append(parts, strings.Join(d.EmotionTags, " "))
	}
	if len(d.SceneTags) > 0 {
		parts = append(parts, strings.Join(d.SceneTags, " "))
	}
	if len(d.UsageHints) > 0 {
		parts = append(parts, strings.Join(d.UsageHints, " "))
	}
	return strings.Join(parts, "\n")
}
