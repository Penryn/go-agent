package multimodal

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/core/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

type Service struct {
	factory ports.ChatModelFactory
}

func New(factory ports.ChatModelFactory) *Service {
	return &Service{factory: factory}
}

func (s *Service) Understand(ctx context.Context, attachments []mediadomain.MultimodalAttachment) ([]mediadomain.MediaDescriptor, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	chatModel, err := s.factory.VisionChatModel(ctx)
	if err != nil || chatModel == nil {
		return fallbackDescriptors(attachments), nil
	}

	descriptors := make([]mediadomain.MediaDescriptor, 0, len(attachments))
	for _, attachment := range attachments {
		msg, err := chatModel.Generate(ctx, []*schema.Message{
			schema.SystemMessage("You are a multimodal QQ bot vision model. Output strict JSON fields: summary,scene_tags,entities,ocr_texts,emotion_hints,meme_signals,meme_keywords,safety_signals,confidence."),
			buildVisionMessage(attachment),
		})
		if err != nil {
			descriptors = append(descriptors, fallbackDescriptor(attachment))
			continue
		}

		var descriptor mediadomain.MediaDescriptor
		if err := json.Unmarshal([]byte(msg.Content), &descriptor); err != nil {
			descriptor = fallbackDescriptor(attachment)
		}
		descriptor.AttachmentID = attachment.AttachmentID
		descriptor.Kind = attachment.Kind
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

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

func fallbackDescriptors(attachments []mediadomain.MultimodalAttachment) []mediadomain.MediaDescriptor {
	descriptors := make([]mediadomain.MediaDescriptor, 0, len(attachments))
	for _, attachment := range attachments {
		descriptors = append(descriptors, fallbackDescriptor(attachment))
	}
	return descriptors
}

func fallbackDescriptor(attachment mediadomain.MultimodalAttachment) mediadomain.MediaDescriptor {
	return mediadomain.MediaDescriptor{
		AttachmentID: attachment.AttachmentID,
		Kind:         attachment.Kind,
		Summary:      "收到一个" + string(attachment.Kind) + "附件",
		Confidence:   0.2,
	}
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
