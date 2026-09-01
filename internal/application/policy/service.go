package policy

import (
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/config"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

type Service struct {
	defaultPolicy policydomain.GroupPolicy
	overrides     map[int64]policydomain.GroupPolicy
	autonomy      policydomain.AutonomyPolicy
}

func New(cfg config.Config) *Service {
	overrides := make(map[int64]policydomain.GroupPolicy, len(cfg.GroupPolicies))
	for _, groupPolicy := range cfg.GroupPolicies {
		overrides[groupPolicy.GroupID] = groupPolicy
	}

	return &Service{
		defaultPolicy: cfg.DefaultPolicy,
		overrides:     overrides,
		autonomy:      cfg.Autonomy,
	}
}

func (s *Service) EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy {
	policy := s.defaultPolicy
	policy.GroupID = groupID

	if override, ok := s.overrides[groupID]; ok {
		if override.PresenceLevel != "" {
			policy.PresenceLevel = override.PresenceLevel
		}
		if override.ToolAllowlist != nil {
			policy.ToolAllowlist = override.ToolAllowlist
		}
		if override.QuietHours != nil {
			policy.QuietHours = override.QuietHours
		}
		if override.MaxConsecutiveBot != 0 {
			policy.MaxConsecutiveBot = override.MaxConsecutiveBot
		}
		if override.PersonaOverlay != nil {
			policy.PersonaOverlay = override.PersonaOverlay
		}
	}

	return policy
}

func (s *Service) AutonomyPolicy() policydomain.AutonomyPolicy {
	return s.autonomy
}

// QuietHourActive 报告 now 是否落在任一安静时段内（含边界，支持跨午夜）。
func (s *Service) QuietHourActive(now time.Time, policy policydomain.GroupPolicy) bool {
	for _, quietHour := range policy.QuietHours {
		if matchHourRange(now, quietHour) {
			return true
		}
	}
	return false
}

func matchHourRange(now time.Time, expr string) bool {
	parts := strings.Split(expr, "-")
	if len(parts) != 2 {
		return false
	}

	start, err := time.Parse("15:04", parts[0])
	if err != nil {
		return false
	}
	end, err := time.Parse("15:04", parts[1])
	if err != nil {
		return false
	}

	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := start.Hour()*60 + start.Minute()
	endMinutes := end.Hour()*60 + end.Minute()

	if startMinutes <= endMinutes {
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	}
	// 跨午夜区间（如 23:00-06:00）
	return currentMinutes >= startMinutes || currentMinutes <= endMinutes
}
