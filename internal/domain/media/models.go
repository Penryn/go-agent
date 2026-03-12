package media

import "time"

type MediaKind string

const (
	MediaImage   MediaKind = "image"
	MediaSticker MediaKind = "sticker"
	MediaVideo   MediaKind = "video"
)

type MultimodalAttachment struct {
	AttachmentID string    `json:"attachment_id" yaml:"attachment_id"`
	Kind         MediaKind `json:"kind" yaml:"kind"`
	URL          string    `json:"url" yaml:"url"`
	ObjectKey    string    `json:"object_key" yaml:"object_key"`
	MIME         string    `json:"mime" yaml:"mime"`
	SizeBytes    int64     `json:"size_bytes" yaml:"size_bytes"`
	Width        int       `json:"width" yaml:"width"`
	Height       int       `json:"height" yaml:"height"`
	DurationMs   int       `json:"duration_ms" yaml:"duration_ms"`
	ContentHash  string    `json:"content_hash" yaml:"content_hash"`
}

type MediaDescriptor struct {
	AttachmentID  string    `json:"attachment_id" yaml:"attachment_id"`
	Kind          MediaKind `json:"kind" yaml:"kind"`
	Summary       string    `json:"summary" yaml:"summary"`
	SceneTags     []string  `json:"scene_tags" yaml:"scene_tags"`
	Entities      []string  `json:"entities" yaml:"entities"`
	OCRTexts      []string  `json:"ocr_texts" yaml:"ocr_texts"`
	EmotionHints  []string  `json:"emotion_hints" yaml:"emotion_hints"`
	MemeSignals   []string  `json:"meme_signals" yaml:"meme_signals"`
	MemeKeywords  []string  `json:"meme_keywords" yaml:"meme_keywords"`
	SafetySignals []string  `json:"safety_signals" yaml:"safety_signals"`
	Keyframes     []string  `json:"keyframes" yaml:"keyframes"`
	Confidence    float64   `json:"confidence" yaml:"confidence"`
	CostTokens    int       `json:"cost_tokens" yaml:"cost_tokens"`
}

type MemeAsset struct {
	MemeID         string    `json:"meme_id" yaml:"meme_id"`
	GroupID        int64     `json:"group_id" yaml:"group_id"`
	SourceEventID  string    `json:"source_event_id" yaml:"source_event_id"`
	ObjectKey      string    `json:"object_key" yaml:"object_key"`
	FileExt        string    `json:"file_ext" yaml:"file_ext"`
	ContentHash    string    `json:"content_hash" yaml:"content_hash"`
	PerceptualHash string    `json:"perceptual_hash" yaml:"perceptual_hash"`
	Width          int       `json:"width" yaml:"width"`
	Height         int       `json:"height" yaml:"height"`
	Animated       bool      `json:"animated" yaml:"animated"`
	Status         string    `json:"status" yaml:"status"`
	CreatedAt      time.Time `json:"created_at" yaml:"created_at"`
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

type MemeSearchResult struct {
	MemeID       string         `json:"meme_id" yaml:"meme_id"`
	Score        float64        `json:"score" yaml:"score"`
	MatchType    string         `json:"match_type" yaml:"match_type"`
	MatchedTerms []string       `json:"matched_terms" yaml:"matched_terms"`
	Descriptor   MemeDescriptor `json:"descriptor" yaml:"descriptor"`
}
