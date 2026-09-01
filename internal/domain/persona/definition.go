package persona

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	FactResolutionActive     = "active"
	FactResolutionShadowed   = "shadowed"
	FactResolutionDisallowed = "disallowed"
	FactResolutionOrphaned   = "orphaned"
)

var canonicalFactKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*(\.\*)?$`)

var ErrFactReservationConflict = errors.New("persona fact reservation conflict")

var legacyFactAliases = map[string]string{
	"school_status":      "education.enrollment.status",
	"school_familiarity": "education.campus.familiarity",
	"course_status":      "education.course.status",
	"current_goal":       "goal.current",
	"current_routine":    "routine.current",
	"recent_experience":  "experience.recent",
}

// PersonaDefinition is the compiled, immutable source of truth shared by
// context projection, prompt rendering, operator updates, and self Canon.
type PersonaDefinition struct {
	Config  PersonaConfig
	Version string
	Hash    string
	exact   map[string]PersonaFactDefinition
	aliases map[string]string
	wild    []PersonaFactDefinition
}

type PersonaView struct {
	PersonaID      string                  `json:"persona_id" yaml:"persona_id"`
	DefinitionHash string                  `json:"definition_hash" yaml:"definition_hash"`
	Version        string                  `json:"version" yaml:"version"`
	Facts          []PersonaFact           `json:"facts" yaml:"facts"`
	ReportedFacts  []PersonaFact           `json:"reported_facts,omitempty" yaml:"reported_facts,omitempty"`
	OpenSlots      []PersonaFactDefinition `json:"open_slots,omitempty" yaml:"open_slots,omitempty"`
	ForbiddenKeys  []string                `json:"forbidden_keys,omitempty" yaml:"forbidden_keys,omitempty"`
	ExcludedFacts  []PersonaFact           `json:"excluded_facts,omitempty" yaml:"excluded_facts,omitempty"`
}

func Compile(config PersonaConfig) (PersonaDefinition, error) {
	definition := PersonaDefinition{
		Config:  cloneConfig(config),
		Hash:    Hash(config),
		Version: strings.TrimSpace(config.Version),
		exact:   make(map[string]PersonaFactDefinition),
		aliases: make(map[string]string),
	}
	if definition.Version == "" {
		definition.Version = definition.Hash[:12]
	}
	for alias, key := range legacyFactAliases {
		definition.aliases[alias] = key
	}
	for _, raw := range config.Facts {
		fact := cloneFactDefinition(raw)
		fact.Key = strings.ToLower(strings.TrimSpace(fact.Key))
		fact.Value = strings.TrimSpace(fact.Value)
		if err := validateFactDefinition(fact); err != nil {
			return PersonaDefinition{}, err
		}
		if strings.HasSuffix(fact.Key, ".*") {
			for _, existing := range definition.wild {
				if existing.Key == fact.Key {
					return PersonaDefinition{}, fmt.Errorf("persona facts contains duplicate key %q", fact.Key)
				}
			}
			definition.wild = append(definition.wild, fact)
		} else {
			if _, exists := definition.exact[fact.Key]; exists {
				return PersonaDefinition{}, fmt.Errorf("persona facts contains duplicate key %q", fact.Key)
			}
			definition.exact[fact.Key] = fact
		}
		for _, rawAlias := range fact.Aliases {
			alias := strings.ToLower(strings.TrimSpace(rawAlias))
			if alias == "" || alias == fact.Key {
				continue
			}
			if current, exists := definition.aliases[alias]; exists && current != fact.Key {
				return PersonaDefinition{}, fmt.Errorf("persona fact alias %q maps to both %q and %q", alias, current, fact.Key)
			}
			definition.aliases[alias] = fact.Key
		}
	}
	// Legacy initial_facts remain readable for existing private configs. They
	// are compiled into operator-managed canonical definitions and should be
	// migrated to persona.facts over time.
	for _, seed := range config.InitialFacts {
		key := definition.CanonicalKey(seed.Key)
		value := strings.TrimSpace(seed.Value)
		if key == "" || value == "" {
			continue
		}
		if existing, ok := definition.exact[key]; ok {
			if existing.Value == "" {
				existing.Value = value
				definition.exact[key] = existing
			}
			continue
		}
		definition.exact[key] = PersonaFactDefinition{
			Key: key, Value: value, Policy: FactPolicyOperatorManaged, Aliases: []string{seed.Key},
		}
	}
	sort.Slice(definition.wild, func(i, j int) bool { return len(definition.wild[i].Key) > len(definition.wild[j].Key) })
	return definition, nil
}

func (d PersonaDefinition) CanonicalKey(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	seen := make(map[string]bool)
	for {
		if seen[key] {
			return key
		}
		seen[key] = true
		next, ok := d.aliases[key]
		if !ok {
			return key
		}
		key = next
	}
}

func (d PersonaDefinition) Rule(raw string) (PersonaFactDefinition, bool) {
	key := d.CanonicalKey(raw)
	if fact, ok := d.exact[key]; ok {
		return cloneFactDefinition(fact), true
	}
	for _, fact := range d.wild {
		prefix := strings.TrimSuffix(fact.Key, "*")
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			copy := cloneFactDefinition(fact)
			copy.Key = key
			copy.Value = ""
			return copy, true
		}
	}
	return PersonaFactDefinition{}, false
}

func (d PersonaDefinition) Rules() []PersonaFactDefinition {
	result := make([]PersonaFactDefinition, 0, len(d.exact)+len(d.wild))
	for _, fact := range d.exact {
		result = append(result, cloneFactDefinition(fact))
	}
	for _, fact := range d.wild {
		result = append(result, cloneFactDefinition(fact))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func ResolveView(definition PersonaDefinition, runtimeFacts []PersonaFact, now time.Time) PersonaView {
	view := PersonaView{PersonaID: definition.Config.ID, DefinitionHash: definition.Hash, Version: definition.Version}
	byKey := make(map[string][]PersonaFact)
	for _, fact := range runtimeFacts {
		if (!fact.ExpiresAt.IsZero() && !fact.ExpiresAt.After(now)) || fact.EffectiveAt.After(now) {
			continue
		}
		fact.Key = definition.CanonicalKey(fact.Key)
		rule, ok := definition.Rule(fact.Key)
		if !ok {
			// Legacy callers may construct a PersonaConfig without a fact registry.
			// Production configs should register keys explicitly; this fallback keeps
			// old in-memory/test personas readable during the migration.
			if len(definition.exact) == 0 && len(definition.wild) == 0 {
				fact.ResolutionState = FactResolutionActive
				byKey[fact.Key] = append(byKey[fact.Key], fact)
				continue
			}
			fact.ResolutionState = FactResolutionOrphaned
			view.ExcludedFacts = append(view.ExcludedFacts, fact)
			continue
		}
		fact.Policy = rule.Policy
		if rule.Policy == FactPolicyForbidden {
			fact.ResolutionState = FactResolutionDisallowed
			view.ExcludedFacts = append(view.ExcludedFacts, fact)
			continue
		}
		if fact.Status == PersonaFactReported {
			fact.ResolutionState = FactResolutionActive
			view.ReportedFacts = append(view.ReportedFacts, fact)
			continue
		}
		byKey[fact.Key] = append(byKey[fact.Key], fact)
	}

	for _, definitionFact := range definition.Rules() {
		if strings.HasSuffix(definitionFact.Key, ".*") {
			if definitionFact.Policy == FactPolicySelfCompleteOnce || definitionFact.Policy == FactPolicySelfMutable {
				view.OpenSlots = append(view.OpenSlots, definitionFact)
			}
			if definitionFact.Policy == FactPolicyForbidden {
				view.ForbiddenKeys = append(view.ForbiddenKeys, definitionFact.Key)
			}
			continue
		}
		key := definitionFact.Key
		selected := selectEffectiveFact(definition, definitionFact, byKey[key], now)
		if selected.FactID != "" {
			view.Facts = append(view.Facts, selected)
		} else if definitionFact.Policy == FactPolicySelfCompleteOnce || definitionFact.Policy == FactPolicySelfMutable {
			view.OpenSlots = append(view.OpenSlots, definitionFact)
		}
		if definitionFact.Policy == FactPolicyForbidden {
			view.ForbiddenKeys = append(view.ForbiddenKeys, key)
		}
	}
	// Dynamic wildcard-backed facts are not visited by the exact definition loop.
	for key, facts := range byKey {
		if _, exact := definition.exact[key]; exact {
			continue
		}
		rule, ok := definition.Rule(key)
		if !ok {
			if len(definition.exact) == 0 && len(definition.wild) == 0 {
				rule = PersonaFactDefinition{Key: key, Policy: FactPolicySelfMutable}
			} else {
				continue
			}
		}
		if selected := selectEffectiveFact(definition, rule, facts, now); selected.FactID != "" {
			view.Facts = append(view.Facts, selected)
		}
	}
	sort.Slice(view.Facts, func(i, j int) bool { return view.Facts[i].Key < view.Facts[j].Key })
	sort.Slice(view.ReportedFacts, func(i, j int) bool { return view.ReportedFacts[i].Key < view.ReportedFacts[j].Key })
	sort.Slice(view.OpenSlots, func(i, j int) bool { return view.OpenSlots[i].Key < view.OpenSlots[j].Key })
	sort.Strings(view.ForbiddenKeys)
	return view
}

func selectEffectiveFact(definition PersonaDefinition, rule PersonaFactDefinition, runtime []PersonaFact, now time.Time) PersonaFact {
	var verified, canon PersonaFact
	for _, fact := range runtime {
		switch fact.Status {
		case PersonaFactVerified:
			verified = newerFact(verified, fact)
		case PersonaFactCanon:
			canon = newerFact(canon, fact)
		}
	}
	configFact := configFact(definition, rule, now)
	var selected PersonaFact
	switch rule.Policy {
	case FactPolicyLocked:
		selected = configFact
	case FactPolicyOperatorManaged:
		if verified.FactID != "" {
			selected = verified
		} else {
			selected = configFact
		}
	case FactPolicySelfCompleteOnce:
		if verified.FactID != "" {
			selected = verified
		} else if configFact.FactID != "" {
			selected = configFact
		} else {
			selected = canon
		}
	case FactPolicySelfMutable:
		if verified.FactID != "" {
			selected = verified
		} else if canon.FactID != "" {
			selected = canon
		} else {
			selected = configFact
		}
	}
	if selected.FactID != "" {
		selected.Policy = rule.Policy
		selected.ResolutionState = FactResolutionActive
	}
	return selected
}

func configFact(definition PersonaDefinition, rule PersonaFactDefinition, now time.Time) PersonaFact {
	if strings.TrimSpace(rule.Value) == "" || strings.HasSuffix(rule.Key, ".*") {
		return PersonaFact{}
	}
	return PersonaFact{
		FactID: "config:" + definition.Hash[:12] + ":" + rule.Key, PersonaID: definition.Config.ID,
		Key: rule.Key, Value: rule.Value, Status: PersonaFactVerified, SourceKind: "config",
		DefinitionHash: definition.Hash, Confidence: 1, EffectiveAt: now, RecordedAt: now,
	}
}

func newerFact(current, candidate PersonaFact) PersonaFact {
	if current.FactID == "" || candidate.EffectiveAt.After(current.EffectiveAt) ||
		(candidate.EffectiveAt.Equal(current.EffectiveAt) && candidate.RecordedAt.After(current.RecordedAt)) {
		return candidate
	}
	return current
}

func validateFactDefinition(fact PersonaFactDefinition) error {
	if len(fact.Key) > 96 || !strings.Contains(fact.Key, ".") || !canonicalFactKeyPattern.MatchString(fact.Key) {
		return fmt.Errorf("invalid persona fact key %q", fact.Key)
	}
	if !slices.Contains([]PersonaFactPolicy{
		FactPolicyLocked, FactPolicyOperatorManaged, FactPolicySelfCompleteOnce, FactPolicySelfMutable, FactPolicyForbidden,
	}, fact.Policy) {
		return fmt.Errorf("invalid policy %q for persona fact %q", fact.Policy, fact.Key)
	}
	if strings.HasSuffix(fact.Key, ".*") && fact.Value != "" {
		return fmt.Errorf("wildcard persona fact %q cannot define a value", fact.Key)
	}
	if fact.Policy == FactPolicyLocked && fact.Value == "" {
		return fmt.Errorf("locked persona fact %q requires a value", fact.Key)
	}
	if fact.Policy == FactPolicyForbidden && fact.Value != "" {
		return fmt.Errorf("forbidden persona fact %q cannot define a value", fact.Key)
	}
	if len([]rune(fact.Value)) > 240 {
		return fmt.Errorf("persona fact %q value exceeds 240 characters", fact.Key)
	}
	return nil
}

func cloneFactDefinition(fact PersonaFactDefinition) PersonaFactDefinition {
	fact.Aliases = append([]string(nil), fact.Aliases...)
	fact.AllowedValues = append([]string(nil), fact.AllowedValues...)
	return fact
}
