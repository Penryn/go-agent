package persona

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ResolvedPersona is the immutable persona input for one group turn. Runtime
// mood/energy are intentionally absent; those belong to PersonaState and are
// loaded independently from the durable runtime state store.
type ResolvedPersona struct {
	Config          PersonaConfig
	Version         string
	Hash            string
	ToolAllowlist   []string
	FewShotExamples []FewShotExample
}

// Resolve merges the base config, an optional typed group override, and the
// legacy map-shaped policy overlay. Overlay values win, which lets operators
// hot-tune a group without changing the process-wide persona file.
func Resolve(base PersonaConfig, groupID int64, overlay map[string]any) ResolvedPersona {
	resolved := cloneConfig(base)
	if override, ok := base.GroupOverrides[groupID]; ok {
		applyOverride(&resolved, override)
	}
	if len(overlay) > 0 {
		if raw, err := json.Marshal(overlay); err == nil {
			var override PersonaOverride
			if json.Unmarshal(raw, &override) == nil {
				applyOverride(&resolved, override)
			}
		}
	}
	// The resolved definition is self-contained; do not carry the entire
	// override registry into snapshots or hashes.
	resolved.GroupOverrides = nil

	toolAllowlist := []string(nil)
	if override, ok := base.GroupOverrides[groupID]; ok && override.ToolAllowlist != nil {
		toolAllowlist = append([]string(nil), override.ToolAllowlist...)
	}
	if len(overlay) > 0 {
		var override PersonaOverride
		if raw, err := json.Marshal(overlay); err == nil && json.Unmarshal(raw, &override) == nil && override.ToolAllowlist != nil {
			toolAllowlist = append([]string(nil), override.ToolAllowlist...)
		}
	}

	hash := Hash(resolved)
	version := strings.TrimSpace(resolved.Version)
	if version == "" {
		version = hash[:12]
	}
	return ResolvedPersona{
		Config:          resolved,
		Version:         version,
		Hash:            hash,
		ToolAllowlist:   toolAllowlist,
		FewShotExamples: append([]FewShotExample(nil), resolved.Speech.FewShotExamples...),
	}
}

// Hash returns the deterministic content identity of a persona definition.
// The supplied Version is excluded so changing a label does not hide a
// content change (or make an unchanged definition look different).
func Hash(config PersonaConfig) string {
	copy := cloneConfig(config)
	copy.Version = ""
	copy.GroupOverrides = nil
	encoded, _ := json.Marshal(copy)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func cloneConfig(config PersonaConfig) PersonaConfig {
	config.Aliases = append([]string(nil), config.Aliases...)
	config.Interests = append([]string(nil), config.Interests...)
	config.Constraints = append([]string(nil), config.Constraints...)
	config.Traits = append([]string(nil), config.Traits...)
	config.FactUpdateUserWhitelist = append([]int64(nil), config.FactUpdateUserWhitelist...)
	config.InitialFacts = append([]PersonaFactSeed(nil), config.InitialFacts...)
	config.ResponseScenarios = append([]ResponseScenario(nil), config.ResponseScenarios...)
	for i := range config.ResponseScenarios {
		config.ResponseScenarios[i].Rules = append([]string(nil), config.ResponseScenarios[i].Rules...)
	}
	config.Background.BehaviorHints = append([]string(nil), config.Background.BehaviorHints...)
	config.Speech.Catchphrases = append([]string(nil), config.Speech.Catchphrases...)
	config.Speech.Avoidances = append([]string(nil), config.Speech.Avoidances...)
	config.Speech.FewShotExamples = append([]FewShotExample(nil), config.Speech.FewShotExamples...)
	if config.GroupOverrides != nil {
		config.GroupOverrides = make(map[int64]PersonaOverride, len(config.GroupOverrides))
		for groupID, override := range config.GroupOverrides {
			config.GroupOverrides[groupID] = override
		}
	}
	return config
}

func applyOverride(config *PersonaConfig, override PersonaOverride) {
	if override.Version != nil {
		config.Version = *override.Version
	}
	if override.Name != nil {
		config.Name = *override.Name
	}
	if override.Description != nil {
		config.Description = *override.Description
	}
	if override.SpeechStyle != nil {
		config.SpeechStyle = *override.SpeechStyle
	}
	if override.Interests != nil {
		config.Interests = append([]string(nil), override.Interests...)
	}
	if override.Constraints != nil {
		config.Constraints = append([]string(nil), override.Constraints...)
	}
	if override.Traits != nil {
		config.Traits = append([]string(nil), override.Traits...)
	}
	if override.ReplyMaxChars != nil {
		config.ReplyMaxChars = *override.ReplyMaxChars
	}
	if override.ReplyMaxSentences != nil {
		config.ReplyMaxSentences = *override.ReplyMaxSentences
	}
	if override.AllowTeasing != nil {
		config.AllowTeasing = *override.AllowTeasing
	}
	if override.AllowQuestions != nil {
		config.AllowQuestions = *override.AllowQuestions
	}
	if override.PreferMemes != nil {
		config.PreferMemes = *override.PreferMemes
	}
	if override.Speech != nil {
		config.Speech = *override.Speech
	}
	if override.FewShotExamples != nil {
		config.Speech.FewShotExamples = append([]FewShotExample(nil), override.FewShotExamples...)
	}
}
