package media

import (
	"strings"
	"time"
)

type MediaKind string

const (
	MediaImage   MediaKind = "image"
	MediaSticker MediaKind = "sticker"
	MediaVideo   MediaKind = "video"
	MediaAudio   MediaKind = "audio"
	MediaFile    MediaKind = "file"
)

type MultimodalAttachment struct {
	AttachmentID string    `json:"attachment_id" yaml:"attachment_id"`
	Kind         MediaKind `json:"kind" yaml:"kind"`
	URL          string    `json:"url" yaml:"url"`
	ObjectKey    string    `json:"object_key" yaml:"object_key"`
	PlatformHint string    `json:"platform_hint" yaml:"platform_hint"`
	MIME         string    `json:"mime" yaml:"mime"`
	Width        int       `json:"width" yaml:"width"`
	Height       int       `json:"height" yaml:"height"`
	ContentHash  string    `json:"content_hash" yaml:"content_hash"`
}

type MediaDescriptor struct {
	AttachmentID  string    `json:"attachment_id" yaml:"attachment_id"`
	Kind          MediaKind `json:"kind" yaml:"kind"`
	Summary       string    `json:"summary" yaml:"summary"`
	SceneTags     []string  `json:"scene_tags" yaml:"scene_tags"`
	OCRTexts      []string  `json:"ocr_texts" yaml:"ocr_texts"`
	EmotionHints  []string  `json:"emotion_hints" yaml:"emotion_hints"`
	MemeSignals   []string  `json:"meme_signals" yaml:"meme_signals"`
	MemeKeywords  []string  `json:"meme_keywords" yaml:"meme_keywords"`
	SafetySignals []string  `json:"safety_signals" yaml:"safety_signals"`
	Confidence    float64   `json:"confidence" yaml:"confidence"`
}

type MemeAsset struct {
	MemeID         string     `json:"meme_id" yaml:"meme_id"`
	GroupID        int64      `json:"group_id" yaml:"group_id"`
	SourceEventID  string     `json:"source_event_id" yaml:"source_event_id"`
	ObjectKey      string     `json:"object_key" yaml:"object_key"`
	FileExt        string     `json:"file_ext" yaml:"file_ext"`
	ContentHash    string     `json:"content_hash" yaml:"content_hash"`
	PerceptualHash string     `json:"perceptual_hash" yaml:"perceptual_hash"`
	Width          int        `json:"width" yaml:"width"`
	Height         int        `json:"height" yaml:"height"`
	Animated       bool       `json:"animated" yaml:"animated"`
	Status         string     `json:"status" yaml:"status"`
	Revision       int64      `json:"revision" yaml:"revision"`
	CreatedAt      time.Time  `json:"created_at" yaml:"created_at"`
	LastSentAt     *time.Time `json:"last_sent_at,omitempty" yaml:"last_sent_at,omitempty"`
	// SendCount 是累计发送次数；DudCount 是发送后群里持续冷场的次数。
	// 两者构成「这个表情效果如何」的粗反馈：哑弹率高的表情检索时降权。
	SendCount int `json:"send_count" yaml:"send_count"`
	DudCount  int `json:"dud_count" yaml:"dud_count"`
}

type MemeDescriptor struct {
	MemeID      string    `json:"meme_id" yaml:"meme_id"`
	Title       string    `json:"title" yaml:"title"`
	Summary     string    `json:"summary" yaml:"summary"`
	Keywords    []string  `json:"keywords" yaml:"keywords"`
	EmotionTags []string  `json:"emotion_tags" yaml:"emotion_tags"`
	SceneTags   []string  `json:"scene_tags" yaml:"scene_tags"`
	UsageHints  []string  `json:"usage_hints" yaml:"usage_hints"`
	Language    string    `json:"language" yaml:"language"`
	Confidence  float64   `json:"confidence" yaml:"confidence"`
	Reviewed    bool      `json:"reviewed" yaml:"reviewed"`
	UpdatedAt   time.Time `json:"updated_at" yaml:"updated_at"`
}

// IndexText 将描述符的文字字段拼为向量索引/检索文本。
func (d MemeDescriptor) IndexText() string {
	parts := make([]string, 0, 6)
	for _, part := range []string{d.Title, d.Summary, strings.Join(d.Keywords, " "), strings.Join(d.EmotionTags, " "), strings.Join(d.SceneTags, " "), strings.Join(d.UsageHints, " ")} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n")
}

type MemeSearchResult struct {
	MemeID       string         `json:"meme_id" yaml:"meme_id"`
	Score        float64        `json:"score" yaml:"score"`
	MatchType    string         `json:"match_type" yaml:"match_type"`
	MatchedTerms []string       `json:"matched_terms" yaml:"matched_terms"`
	Descriptor   MemeDescriptor `json:"descriptor" yaml:"descriptor"`
}
