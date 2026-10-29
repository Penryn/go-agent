package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExampleLoads(t *testing.T) {
	cfg, err := Load("../../configs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Meme.AutoCollect || cfg.Memory.TopK < 1 || cfg.Models.Embedding.Dimensions < 1 {
		t.Fatal("missing accepted capabilities")
	}
}
func TestRejectsRemovedCapabilitiesAndInvalidLimits(t *testing.T) {
	for _, body := range []string{"tools:\n  codex:\n    enabled: true\n", "chat:\n  max_per_minute: 0\n", "meme:\n  max_image_bytes: 999999999\n", "models:\n  embedding:\n    dimensions: 0\n", "memory:\n  semantic_distance_threshold: 0\n", "memory:\n  preference_budget: 51\n", "memory:\n  preference_budget: 10\n  member_budget: 10\n  episode_budget: 10\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid configuration accepted", strings.Split(body, "\n")[0])
		}
	}
}
