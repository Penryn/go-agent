package persona

import "time"

// Background 描述角色的背景故事与行为倾向。
type Background struct {
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

// ResponseScenario describes how to respond to a class of situations without
// freezing a time-sensitive answer into the long-lived persona definition.
type ResponseScenario struct {
	Situation string   `json:"situation" yaml:"situation"`
	Rules     []string `json:"rules" yaml:"rules"`
}

// PersonaFactSeed provides the initial value for a mutable runtime fact. A
// newer verified runtime fact with the same key supersedes this seed.
type PersonaFactSeed struct {
	Key         string `json:"key" yaml:"key"`
	Value       string `json:"value" yaml:"value"`
	EffectiveAt string `json:"effective_at,omitempty" yaml:"effective_at,omitempty"`
}

const (
	PersonaFactVerified = "verified"
	PersonaFactReported = "reported"
)

// PersonaFact is an append-only, source-attributed fact about the persona's
// current life. Verified facts may override config seeds; reported facts are
// injected only as hearsay and normally expire.
type PersonaFact struct {
	FactID        string    `json:"fact_id" yaml:"fact_id"`
	PersonaID     string    `json:"persona_id" yaml:"persona_id"`
	Key           string    `json:"key" yaml:"key"`
	Value         string    `json:"value" yaml:"value"`
	Status        string    `json:"status" yaml:"status"`
	SourceKind    string    `json:"source_kind" yaml:"source_kind"`
	SourceGroupID int64     `json:"source_group_id" yaml:"source_group_id"`
	SourceUserID  int64     `json:"source_user_id" yaml:"source_user_id"`
	SourceEventID string    `json:"source_event_id" yaml:"source_event_id"`
	Confidence    float64   `json:"confidence" yaml:"confidence"`
	EffectiveAt   time.Time `json:"effective_at" yaml:"effective_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	RecordedAt    time.Time `json:"recorded_at" yaml:"recorded_at"`
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
	// FactUpdateUserWhitelist lists QQ users allowed to turn a direct statement
	// into a durable verified persona fact. Other users may only contribute
	// short-lived reported facts.
	FactUpdateUserWhitelist []int64            `json:"-" yaml:"fact_update_user_whitelist,omitempty"`
	InitialFacts            []PersonaFactSeed  `json:"initial_facts,omitempty" yaml:"initial_facts,omitempty"`
	ResponseScenarios       []ResponseScenario `json:"response_scenarios,omitempty" yaml:"response_scenarios,omitempty"`
	// Traits 是性格特征列表，如 ["直率", "爱开玩笑"]。
	Traits     []string       `json:"traits" yaml:"traits"`
	Background Background     `json:"background" yaml:"background"`
	Speech     SpeechPatterns `json:"speech" yaml:"speech"`
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
