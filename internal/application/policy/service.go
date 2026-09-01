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
		if override.ToolAllowlist != nil {
			policy.ToolAllowlist = override.ToolAllowlist
		}
		if override.MaxConsecutiveBot != 0 {
			policy.MaxConsecutiveBot = override.MaxConsecutiveBot
		}
	}

	return policy
}

func (s *Service) AutonomyPolicy() policydomain.AutonomyPolicy {
	return s.autonomy
}
