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
	FewShotExamples []FewShotExample
}

// Resolve returns the immutable persona input for one group turn. Runtime
// mood/energy are intentionally absent; those live in PersonaState and are
// loaded independently from the durable runtime state store.
func Resolve(base PersonaConfig) ResolvedPersona {
	resolved := cloneConfig(base)
	hash := Hash(resolved)
	version := strings.TrimSpace(resolved.Version)
	if version == "" {
		version = hash[:12]
	}
	return ResolvedPersona{
		Config:          resolved,
		Version:         version,
		Hash:            hash,
		FewShotExamples: append([]FewShotExample(nil), resolved.Speech.FewShotExamples...),
	}
}

// Hash returns the deterministic content identity of a persona definition.
// The supplied Version is excluded so changing a label does not hide a
// content change (or make an unchanged definition look different).
func Hash(config PersonaConfig) string {
	copy := cloneConfig(config)
	copy.Version = ""
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
	return config
}

