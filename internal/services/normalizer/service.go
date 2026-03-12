package normalizer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

type Service struct {
	source  string
	selfID  int64
	aliases []string
}

func New(source string, selfID int64, aliases []string) *Service {
	return &Service{
		source:  source,
		selfID:  selfID,
		aliases: aliases,
	}
}

type oneBotEvent struct {
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	NoticeType  string          `json:"notice_type"`
	SubType     string          `json:"sub_type"`
	Time        int64           `json:"time"`
	SelfID      int64           `json:"self_id"`
	GroupID     int64           `json:"group_id"`
	UserID      int64           `json:"user_id"`
	MessageID   any             `json:"message_id"`
	RawMessage  string          `json:"raw_message"`
	Message     []oneBotSegment `json:"message"`
}

type oneBotSegment struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func (s *Service) Normalize(payload []byte) (conversationdomain.EventEnvelope, error) {
	var raw oneBotEvent
	if err := json.Unmarshal(payload, &raw); err != nil {
		return conversationdomain.EventEnvelope{}, fmt.Errorf("decode onebot payload: %w", err)
	}

	kind := conversationdomain.EventMeta
	switch raw.PostType {
	case "message":
		kind = conversationdomain.EventMessage
	case "notice":
		if raw.NoticeType == "notify" && raw.SubType == "poke" {
			kind = conversationdomain.EventPoke
		} else {
			kind = conversationdomain.EventNotice
		}
	}

	selfID := s.selfID
	if selfID == 0 {
		selfID = raw.SelfID
	}

	textParts := make([]string, 0, len(raw.Message))
	segments := make([]conversationdomain.MessageSegment, 0, len(raw.Message))
	attachments := []mediadomain.MultimodalAttachment{}
	var mentionedBot bool
	var replyToMessageID string

	for idx, segment := range raw.Message {
		segments = append(segments, conversationdomain.MessageSegment{
			Type: segment.Type,
			Data: segment.Data,
		})

		switch segment.Type {
		case "text":
			if text, ok := stringField(segment.Data["text"]); ok {
				textParts = append(textParts, text)
			}
		case "at":
			if qq, ok := stringField(segment.Data["qq"]); ok && qq == strconv.FormatInt(selfID, 10) {
				mentionedBot = true
			}
		case "reply":
			if id, ok := stringField(segment.Data["id"]); ok {
				replyToMessageID = id
			}
		case "image", "mface", "video", "record", "file":
			attachments = append(attachments, s.normalizeAttachment(idx, segment))
		}
	}

	text := strings.TrimSpace(strings.Join(textParts, ""))
	if text == "" {
		text = strings.TrimSpace(raw.RawMessage)
	}

	namedBot := containsAlias(strings.ToLower(text), s.aliases)
	now := time.Unix(raw.Time, 0)
	if raw.Time == 0 {
		now = time.Now()
	}
	messageID := normalizeID(raw.MessageID)
	traceID := fmt.Sprintf("trace-%d-%s", now.UnixNano(), messageID)

	return conversationdomain.EventEnvelope{
		Source:        s.source,
		SelfID:        selfID,
		ReceivedAt:    time.Now(),
		RawPayload:    payload,
		TraceID:       traceID,
		CorrelationID: messageID,
		Event: conversationdomain.ConversationEvent{
			EventID:          traceID,
			GroupID:          raw.GroupID,
			UserID:           raw.UserID,
			MessageID:        messageID,
			ReplyToMessageID: replyToMessageID,
			Kind:             kind,
			Text:             text,
			Segments:         segments,
			MentionedBot:     mentionedBot,
			NamedBot:         namedBot,
			IsReplyToBot:     replyToMessageID != "",
			Attachments:      attachments,
			TimestampUnix:    now.Unix(),
		},
	}, nil
}

func (s *Service) normalizeAttachment(idx int, segment oneBotSegment) mediadomain.MultimodalAttachment {
	kind := mediadomain.MediaImage
	switch segment.Type {
	case "mface":
		kind = mediadomain.MediaSticker
	case "video":
		kind = mediadomain.MediaVideo
	case "record":
		kind = mediadomain.MediaAudio
	case "file":
		kind = mediadomain.MediaFile
	}

	file, _ := stringField(segment.Data["file"])
	url, _ := stringField(segment.Data["url"])
	return mediadomain.MultimodalAttachment{
		AttachmentID: fmt.Sprintf("%s-%d", file, idx),
		Kind:         kind,
		URL:          url,
		ObjectKey:    file,
		MIME:         mimeForSegmentType(segment.Type),
	}
}

func containsAlias(text string, aliases []string) bool {
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(alias)) {
			return true
		}
	}
	return false
}

func normalizeID(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func stringField(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	default:
		return "", false
	}
}

func mimeForSegmentType(segmentType string) string {
	switch segmentType {
	case "video":
		return "video/mp4"
	case "mface":
		return "image/webp"
	case "record":
		return "audio/silk"
	case "file":
		return "application/octet-stream"
	default:
		return "image/jpeg"
	}
}
