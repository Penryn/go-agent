package persona

import "time"

// BackgroundStory 描述角色的背景故事与行为倾向。
type BackgroundStory struct {
	// Summary 是一段自由文本的背景介绍，渲染时直接追加到长期人格层。
	Summary string `json:"summary" yaml:"summary"`
	// BehaviorHints 是白话描述的行为倾向列表，每条独立一行渲染。
	BehaviorHints []string `json:"behavior_hints" yaml:"behavior_hints"`
}

// FewShotExample 是一组对话示例，用于引导模型模仿说话风格。
type FewShotExample struct {
	UserSays string `json:"user_says" yaml:"user_says"`
	BotSays  string `json:"bot_says" yaml:"bot_says"`
}

// SpeechPatterns 描述角色的口头习惯、禁忌用语和表情包频率。
type SpeechPatterns struct {
	// Catchphrases 是角色偶尔会说的习惯用语。
	Catchphrases []string `json:"catchphrases" yaml:"catchphrases"`
	// Avoidances 是角色绝对不说的词汇或表达。
	Avoidances []string `json:"avoidances" yaml:"avoidances"`
	// EmojiFrequency 控制表情包使用频率，取值：frequent/moderate/rare/none。
	EmojiFrequency string `json:"emoji_frequency" yaml:"emoji_frequency"`
	// FewShotExamples 是少样本对话示例。
	FewShotExamples []FewShotExample `json:"few_shot_examples" yaml:"few_shot_examples"`
}

type PersonaConfig struct {
	ID string `json:"id" yaml:"id"`
	// Version is an optional operator supplied revision. When empty, the
	// resolver derives a short content hash so prompt/audit records remain
	// attributable to the exact persona definition used for a turn.
	Version           string   `json:"version" yaml:"version"`
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
	// Traits 是性格特征列表，如 ["直率", "爱开玩笑"]。
	Traits     []string        `json:"traits" yaml:"traits"`
	Background BackgroundStory `json:"background" yaml:"background"`
	Speech     SpeechPatterns  `json:"speech" yaml:"speech"`
	// GroupOverrides are static, typed per-group persona changes. Dynamic
	// policy overlays are accepted separately by Resolve for backwards
	// compatible config loading.
	GroupOverrides map[int64]PersonaOverride `json:"group_overrides" yaml:"group_overrides"`
}

// PersonaOverride contains fields that may be changed for one group. Pointer
// scalars preserve the distinction between "unset" and an explicit false/0.
type PersonaOverride struct {
	Version           *string          `json:"version,omitempty" yaml:"version,omitempty"`
	Name              *string          `json:"name,omitempty" yaml:"name,omitempty"`
	Description       *string          `json:"description,omitempty" yaml:"description,omitempty"`
	SpeechStyle       *string          `json:"speech_style,omitempty" yaml:"speech_style,omitempty"`
	Interests         []string         `json:"interests,omitempty" yaml:"interests,omitempty"`
	Constraints       []string         `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Traits            []string         `json:"traits,omitempty" yaml:"traits,omitempty"`
	ReplyMaxChars     *int             `json:"reply_max_chars,omitempty" yaml:"reply_max_chars,omitempty"`
	ReplyMaxSentences *int             `json:"reply_max_sentences,omitempty" yaml:"reply_max_sentences,omitempty"`
	AllowTeasing      *bool            `json:"allow_teasing,omitempty" yaml:"allow_teasing,omitempty"`
	AllowQuestions    *bool            `json:"allow_questions,omitempty" yaml:"allow_questions,omitempty"`
	PreferMemes       *bool            `json:"prefer_memes,omitempty" yaml:"prefer_memes,omitempty"`
	Speech            *SpeechPatterns  `json:"speech,omitempty" yaml:"speech,omitempty"`
	ToolAllowlist     []string         `json:"tool_allowlist,omitempty" yaml:"tool_allowlist,omitempty"`
	FewShotExamples   []FewShotExample `json:"few_shot_examples,omitempty" yaml:"few_shot_examples,omitempty"`
}

type PersonaProfile struct {
	PersonaID   string `json:"persona_id" yaml:"persona_id"`
	DisplayName string `json:"display_name" yaml:"display_name"`
	Version     string `json:"version" yaml:"version"`
	Hash        string `json:"hash" yaml:"hash"`
	// Config is the immutable resolved definition for this group turn. It is
	// deliberately separate from PersonaState, which carries mutable mood and
	// energy and is persisted by the runtime state store.
	Config          PersonaConfig      `json:"config,omitempty" yaml:"config,omitempty"`
	StableTraits    []string           `json:"stable_traits" yaml:"stable_traits"`
	StyleRules      []string           `json:"style_rules" yaml:"style_rules"`
	AutonomyBias    map[string]float64 `json:"autonomy_bias" yaml:"autonomy_bias"`
	InteractionMap  map[string]string  `json:"interaction_map" yaml:"interaction_map"`
	OutputRules     []string           `json:"output_rules" yaml:"output_rules"`
	ToolAllowlist   []string           `json:"tool_allowlist" yaml:"tool_allowlist"`
	FewShotExamples []FewShotExample   `json:"few_shot_examples" yaml:"few_shot_examples"`
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

// Mood 取值：persona 的即时情绪倾向
type Mood string

const (
	MoodHappy     Mood = "happy"     // 愉悦，话更多，用词更活泼
	MoodSteady    Mood = "steady"    // 基线中性
	MoodWithdrawn Mood = "withdrawn" // 低落/退缩，话变少，语气平
	MoodAggro     Mood = "aggro"     // 被频繁 cue 烦了，语气略冲
)

// Energy 取值：persona 的活跃度/注意力资源
type Energy string

const (
	EnergyHigh   Energy = "high"   // 主动性强，可主动接话
	EnergyNormal Energy = "normal" // 基线
	EnergyLow    Energy = "low"    // 略倦，倾向更短回复
	EnergyTired  Energy = "tired"  // 疲劳，尽量 silent 或单句回应
)
