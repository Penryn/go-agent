package persona

import "time"

type PersonaConfig struct {
	ID                string   `json:"id" yaml:"id"`
	Name              string   `json:"name" yaml:"name"`
	Aliases           []string `json:"aliases" yaml:"aliases"`
	Interests         []string `json:"interests" yaml:"interests"`
	SpeechStyle       string   `json:"speech_style" yaml:"speech_style"`
	Description       string   `json:"description" yaml:"description"`
	Constraints       []string `json:"constraints" yaml:"constraints"`
	ReplyMaxChars     int      `json:"reply_max_chars" yaml:"reply_max_chars"`
	ReplyMaxSentences int      `json:"reply_max_sentences" yaml:"reply_max_sentences"`
	AllowTeasing      bool     `json:"allow_teasing" yaml:"allow_teasing"`
	AllowQuestions    bool     `json:"allow_questions" yaml:"allow_questions"`
	PreferMemes       bool     `json:"prefer_memes" yaml:"prefer_memes"`
}

type PersonaProfile struct {
	PersonaID      string             `json:"persona_id" yaml:"persona_id"`
	DisplayName    string             `json:"display_name" yaml:"display_name"`
	StableTraits   []string           `json:"stable_traits" yaml:"stable_traits"`
	StyleRules     []string           `json:"style_rules" yaml:"style_rules"`
	AutonomyBias   map[string]float64 `json:"autonomy_bias" yaml:"autonomy_bias"`
	InteractionMap map[string]string  `json:"interaction_map" yaml:"interaction_map"`
	OutputRules    []string           `json:"output_rules" yaml:"output_rules"`
}

type PersonaState struct {
	PersonaID string    `json:"persona_id" yaml:"persona_id"`
	GroupID   int64     `json:"group_id" yaml:"group_id"`
	Mood      string    `json:"mood" yaml:"mood"`
	Energy    string    `json:"energy" yaml:"energy"`
	TalkBias  float64   `json:"talk_bias" yaml:"talk_bias"`
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`
}
