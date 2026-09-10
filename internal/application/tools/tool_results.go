package tools

import (
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// speak_text 工具的类型
type speakTextArgs struct {
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}

type speakTextResult struct {
	Tool             string                             `json:"tool"`
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}

// stay_silent 工具的类型
type staySilentArgs struct {
	ReasonCode string `json:"reason_code"`
	TTLMS      int    `json:"ttl_ms"`
}

// react_emoji 工具的类型
type reactEmojiArgs struct {
	EmojiID    string `json:"emoji_id"`
	MessageID  string `json:"message_id"`
	ReasonCode string `json:"reason_code"`
}

type reactEmojiResult struct {
	Tool      string `json:"tool"`
	EmojiID   string `json:"emoji_id"`
	MessageID string `json:"message_id"`
}

// query_memory 工具的类型
type queryMemoryArgs struct {
	Query       string   `json:"query"`
	Scope       string   `json:"scope"`
	TopK        int      `json:"top_k"`
	MemoryTypes []string `json:"memory_types"`
}

// search_meme 工具的类型
type searchMemeArgs struct {
	Query         string `json:"query"`
	Emotion       string `json:"emotion"`
	Scene         string `json:"scene"`
	TopK          int    `json:"top_k"`
	ExcludeRecent bool   `json:"exclude_recent"`
}

// send_meme 工具的类型
type sendMemeArgs struct {
	MemeID           string `json:"meme_id"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	Caption          string `json:"caption"`
}

type sendMemeResult struct {
	Tool             string `json:"tool"`
	MemeID           string `json:"meme_id"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	Caption          string `json:"caption"`
}

// quote_reply 工具的类型
type quoteReplyArgs struct {
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}

type quoteReplyResult struct {
	Tool             string                             `json:"tool"`
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}

// query_member_profile 工具的类型
type queryMemberProfileArgs struct {
	UserID int64    `json:"user_id"`
	Fields []string `json:"fields"`
}

// repair_message 工具的类型
type repairMessageArgs struct {
	MessageID        string `json:"message_id"`
	CorrectedText    string `json:"corrected_text"`
	ReplyToMessageID string `json:"reply_to_message_id"`
}

type repairMessageResult struct {
	Tool             string `json:"tool"`
	MessageID        string `json:"message_id"`
	CorrectedText    string `json:"corrected_text"`
	ReplyToMessageID string `json:"reply_to_message_id"`
}

// poke_member 工具的类型
type pokeMemberArgs struct {
	UserID int64 `json:"user_id"`
}

type pokeMemberResult struct {
	Tool   string `json:"tool"`
	UserID int64  `json:"user_id"`
}

// update_persona_fact 工具的类型
type updatePersonaFactArgs struct {
	Key             string  `json:"key"`
	Value           string  `json:"value"`
	SourceKind      string  `json:"source_kind"`
	EvidenceEventID string  `json:"evidence_event_id"`
	Confidence      float64 `json:"confidence"`
	TTLHours        int     `json:"ttl_hours"`
}
