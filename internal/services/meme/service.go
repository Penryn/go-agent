package meme

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

type Service struct {
	store       ports.MemeStore
	vectorStore ports.VectorMemeStore
	cfg         config.MemeConfig
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

// ObserveEvent 观察群聊事件，将图片/贴纸收入表情包库。
// descriptors 是 visionSvc.Understand 已经计算好的视觉描述，可为 nil（降级到默认描述符）。
func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent, descriptors []mediadomain.MediaDescriptor) error {
	if !s.cfg.AutoCollect {
		return nil
	}

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

		memeID := buildMemeID(attachment)
		descriptor := buildMemeDescriptor(memeID, attachment, descriptors, s.cfg.CandidateThreshold)
		err := s.store.UpsertMeme(ctx, mediadomain.MemeAsset{
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
		}, descriptor)
		if err != nil {
			return err
		}

		// 异步写向量索引，使用独立 context 避免主链路 ctx cancel 中断写操作
		if s.vectorStore != nil {
			indexText := buildIndexText(descriptor)
			groupID := event.GroupID
			go func() {
				indexCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.vectorStore.IndexMeme(indexCtx, memeID, indexText, groupID); err != nil {
					slog.Warn("meme: async vector index failed", "meme_id", memeID, "err", err)
				}
			}()
		}
	}
	return nil
}

// Search 搜索表情包，向量优先；向量无结果时 fallback 到关键词搜索。
// 关键词搜索路径自动应用 RepeatCooldown 冷却过滤；若冷却期内无结果则放开冷却重试。
func (s *Service) Search(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	// 向量优先路径
	if s.vectorStore != nil && s.cfg.SemanticTopK > 0 && strings.TrimSpace(query.Query) != "" {
		vectorResults, err := s.vectorStore.SearchMemes(ctx, query.GroupID, query.Query, s.cfg.SemanticTopK, s.cfg.SemanticThreshold)
		if err != nil {
			slog.Warn("meme.Search: vector search failed, fallback to keyword", "group_id", query.GroupID, "err", err)
		} else if len(vectorResults) > 0 {
			// 用 memeID 回查 MySQL 补全 Descriptor，并走 RepeatCooldown 过滤
			return s.enrichAndFilter(ctx, vectorResults, query)
		}
	}

	// fallback：关键词搜索路径（含 RepeatCooldown 冷却过滤）
	return s.keywordSearch(ctx, query)
}

// keywordSearch 执行关键词搜索并应用 RepeatCooldown 冷却过滤。
func (s *Service) keywordSearch(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	results, err := s.store.SearchMemes(ctx, query)
	if err != nil || len(results) == 0 {
		return results, err
	}
	return s.applyCooldown(ctx, results, query)
}

// enrichAndFilter 根据向量搜索返回的 memeID 回查 MySQL 补全 Descriptor，然后应用冷却过滤。
func (s *Service) enrichAndFilter(ctx context.Context, vectorResults []mediadomain.MemeSearchResult, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	enriched := make([]mediadomain.MemeSearchResult, 0, len(vectorResults))
	for _, r := range vectorResults {
		_, desc, err := s.store.GetMeme(ctx, r.MemeID)
		if err != nil {
			slog.Warn("meme.enrichAndFilter: GetMeme failed, skipping", "meme_id", r.MemeID, "err", err)
			continue
		}
		r.Descriptor = desc
		enriched = append(enriched, r)
	}
	if len(enriched) == 0 {
		return s.keywordSearch(ctx, query)
	}
	return s.applyCooldown(ctx, enriched, query)
}

// applyCooldown 按 RepeatCooldown 过滤最近发送过的表情包；若全部被过滤则放开限制返回原结果。
func (s *Service) applyCooldown(ctx context.Context, results []mediadomain.MemeSearchResult, _ ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	cooldown, parseErr := time.ParseDuration(s.cfg.RepeatCooldown)
	if parseErr != nil || cooldown <= 0 {
		return results, nil
	}
	cutoff := time.Now().Add(-cooldown)
	filtered := results[:0]
	for _, r := range results {
		asset, _, getErr := s.store.GetMeme(ctx, r.MemeID)
		if getErr != nil {
			filtered = append(filtered, r)
			continue
		}
		if asset.LastSentAt == nil || asset.LastSentAt.Before(cutoff) {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) > 0 {
		return filtered, nil
	}
	slog.Debug("meme.Search: all results in cooldown, fallback without cooldown filter")
	return results, nil
}

// BuildSendSegments 构建发送消息片段。
func (s *Service) BuildSendSegments(ctx context.Context, memeID string, replyToMessageID string, caption string) ([]conversationdomain.MessageSegment, error) {
	asset, _, err := s.store.GetMeme(ctx, memeID)
	if err != nil {
		return nil, err
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

// MarkSent 标记表情包已发送，更新发送计数和最后发送时间。
func (s *Service) MarkSent(ctx context.Context, memeID string) error {
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

func fileExt(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx:]
	}
	return ".bin"
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
