package multimodal

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/config"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

const defaultDownloadTimeout = 10 * time.Second

// visionModelFactory 是本包对模型工厂的最小依赖面(接口定义在消费方)。
type visionModelFactory interface {
	VisionChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error)
}

type Service struct {
	factory visionModelFactory
	downloadTimeout time.Duration
}

func New(factory visionModelFactory, cfg config.MultimodalConfig) *Service {
	timeout := defaultDownloadTimeout
	if cfg.DownloadTimeout != "" {
		if d, err := time.ParseDuration(cfg.DownloadTimeout); err == nil && d > 0 {
			timeout = d
		}
	}
	return &Service{
		factory:         factory,
		downloadTimeout: timeout,
	}
}

// Understand 逐附件调用 Vision 模型生成描述符。看不懂就返回不完整的结果：
// 失败的附件没有描述符（下游 meme 收藏 / 接图判断按空处理），不编造
// "收到一张图片，内容暂时无法识别" 这类假描述污染数据。
func (s *Service) Understand(ctx context.Context, attachments []mediadomain.MultimodalAttachment) ([]mediadomain.MediaDescriptor, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	chatModel, err := s.factory.VisionChatModel(ctx)
	if err != nil || chatModel == nil {
		slog.Warn("multimodal: vision model unavailable, skipping attachments",
			"attachments", len(attachments), "err", err)
		return nil, nil
	}

	descriptors := make([]mediadomain.MediaDescriptor, 0, len(attachments))
	for _, attachment := range attachments {
		// audio 和 file 类型无可视内容，直接跳过 Vision 调用
		if attachment.Kind == mediadomain.MediaAudio || attachment.Kind == mediadomain.MediaFile {
			continue
		}

		// 每个附件独立派生超时 context，互不影响
		callCtx, callCancel := context.WithTimeout(ctx, s.downloadTimeout)
		msg, genErr := chatModel.Generate(callCtx, []*schema.Message{
			schema.SystemMessage(visionSystemPrompt()),
			buildVisionMessage(attachment),
		})
		callCancel()

		if genErr != nil {
			slog.Warn("multimodal: vision generate failed, skipping attachment",
				"attachment_id", attachment.AttachmentID,
				"kind", attachment.Kind,
				"err", genErr)
			continue
		}

		// strip markdown 包裹 + json.Valid 预校验
		raw := stripMarkdownFence(msg.Content)
		var descriptor mediadomain.MediaDescriptor
		if !json.Valid([]byte(raw)) {
			preview := raw
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			slog.Warn("multimodal: vision response not valid JSON, skipping attachment",
				"attachment_id", attachment.AttachmentID,
				"raw_preview", preview)
			continue
		} else if unmarshalErr := json.Unmarshal([]byte(raw), &descriptor); unmarshalErr != nil {
			slog.Warn("multimodal: vision response unmarshal failed, skipping attachment",
				"attachment_id", attachment.AttachmentID,
				"err", unmarshalErr)
			continue
		}

		// AttachmentID 和 Kind 始终以附件原始值为准，防止模型填错
		descriptor.AttachmentID = attachment.AttachmentID
		descriptor.Kind = attachment.Kind
		slog.Info("multimodal: vision descriptor",
			"attachment_id", attachment.AttachmentID,
			"kind", attachment.Kind,
			"summary", descriptor.Summary,
			"confidence", descriptor.Confidence,
			"ocr_texts", descriptor.OCRTexts,
			"emotion_hints", descriptor.EmotionHints,
			"meme_signals", descriptor.MemeSignals,
			"meme_keywords", descriptor.MemeKeywords,
			"scene_tags", descriptor.SceneTags,
		)
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

// visionSystemPrompt 返回 Vision 模型的 system prompt。
func visionSystemPrompt() string {
	return "You are a vision model for a Chinese QQ group chat bot. " +
		"Respond ONLY with a valid JSON object — no markdown fences, no explanation. " +
		"Required fields: summary(string), scene_tags([]string), entities([]string), " +
		"ocr_texts([]string), emotion_hints([]string), meme_signals([]string), " +
		"meme_keywords([]string), safety_signals([]string), confidence(float 0-1). " +
		"If the attachment cannot be understood, return {\"summary\":\"无法识别\",\"confidence\":0.1}."
}

// buildVisionMessage 根据附件类型构造 Vision 请求消息。
// MediaSticker 使用 ImageURLDetailLow（表情包内容简单，节省 token）。
func buildVisionMessage(attachment mediadomain.MultimodalAttachment) *schema.Message {
	parts := []schema.MessageInputPart{
		{
			Type: schema.ChatMessagePartTypeText,
			Text: "Describe this QQ attachment for reply planning and meme reuse.",
		},
	}

	switch attachment.Kind {
	case mediadomain.MediaVideo:
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeVideoURL,
			Video: &schema.MessageInputVideo{
				MessagePartCommon: schema.MessagePartCommon{
					URL:      stringPtr(attachment.URL),
					MIMEType: attachment.MIME,
				},
			},
		})
	case mediadomain.MediaSticker:
		// 贴纸内容简单，Low detail 已足够，降低 token 消耗
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{
					URL:      stringPtr(attachment.URL),
					MIMEType: attachment.MIME,
				},
				Detail: schema.ImageURLDetailLow,
			},
		})
	default:
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{
					URL:      stringPtr(attachment.URL),
					MIMEType: attachment.MIME,
				},
				Detail: schema.ImageURLDetailHigh,
			},
		})
	}

	return &schema.Message{
		Role:                  schema.User,
		UserInputMultiContent: parts,
	}
}

// stripMarkdownFence 移除模型输出中可能包裹 JSON 的 markdown 代码块标记。
// 处理模型未遵守格式约束时的防御性清理。
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	var body string
	if strings.HasPrefix(s, "```json") {
		body = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		body = strings.TrimPrefix(s, "```")
	} else {
		return s
	}
	body = strings.TrimLeft(body, "\r\n")
	// 用 LastIndex 定位结尾 ```，JSON 内容本身不含三反引号
	if idx := strings.LastIndex(body, "```"); idx >= 0 {
		body = body[:idx]
	}
	return strings.TrimSpace(body)
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
