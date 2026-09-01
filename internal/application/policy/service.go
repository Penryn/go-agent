package policy

import (
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
		if override.ActiveHours != nil {
			policy.ActiveHours = override.ActiveHours
		}
		if override.MaxConsecutiveBot != 0 {
			policy.MaxConsecutiveBot = override.MaxConsecutiveBot
		}
		if override.ReplyToImageChance != 0 {
			policy.ReplyToImageChance = override.ReplyToImageChance
		}
		policy.Enabled = override.Enabled
		policy.AllowPokeBack = override.AllowPokeBack
		policy.AllowRecall = override.AllowRecall
		if override.PersonaOverlay != nil {
			policy.PersonaOverlay = override.PersonaOverlay
		}
	}

	return policy
}

func (s *Service) AutonomyPolicy() policydomain.AutonomyPolicy {
	return s.autonomy
}
