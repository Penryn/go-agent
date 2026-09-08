package scene

import (
	"time"
)

type Role string

const (
	RoleObserver    Role = "observer"
	RoleParticipant Role = "participant"
	RoleHelper      Role = "helper"
	RoleJoker       Role = "joker"
	RoleModerator   Role = "moderator"
	RoleWithdrawn   Role = "withdrawn"
)

type BotReception string

const (
	ReceptionUnknown BotReception = "unknown"
	ReceptionWarm    BotReception = "warm"
	ReceptionNeutral BotReception = "neutral"
	ReceptionCold    BotReception = "cold"
)

// GroupScene is the durable current projection of a group's social state.
// It is rebuildable from conversation facts and must not be treated as an
// authoritative event log.
type GroupScene struct {
	GroupID            int64        `json:"group_id" yaml:"group_id"`
	ActivityLevel      float64      `json:"activity_level" yaml:"activity_level"`
	SocialTemperature  float64      `json:"social_temperature" yaml:"social_temperature"`
	CurrentTopic       string       `json:"current_topic" yaml:"current_topic"`
	OpenLoops          []string     `json:"open_loops" yaml:"open_loops"`
	ActiveSpeakers     []int64      `json:"active_speakers" yaml:"active_speakers"`
	Audience           []int64      `json:"audience" yaml:"audience"`
	ConflictLevel      float64      `json:"conflict_level" yaml:"conflict_level"`
	BotReception       BotReception `json:"bot_reception" yaml:"bot_reception"`
	RecommendedRole    Role         `json:"recommended_role" yaml:"recommended_role"`
	LastHumanMessageAt time.Time    `json:"last_human_message_at" yaml:"last_human_message_at"`
	LastBotMessageAt   time.Time    `json:"last_bot_message_at" yaml:"last_bot_message_at"`
	LastEventID        string       `json:"last_event_id" yaml:"last_event_id"`
	Revision           int64        `json:"revision" yaml:"revision"`
	UpdatedAt          time.Time    `json:"updated_at" yaml:"updated_at"`
}

func New(groupID int64) GroupScene {
	return GroupScene{
		GroupID:         groupID,
		BotReception:    ReceptionUnknown,
		RecommendedRole: RoleObserver,
	}
}
