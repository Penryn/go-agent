package persona

import "testing"

func TestResolveAppliesGroupOverrideAndPolicyOverlay(t *testing.T) {
	base := PersonaConfig{
		ID: "main", Name: "Base", Description: "base", SpeechStyle: "short",
		AllowTeasing: false,
		Speech:       SpeechPatterns{FewShotExamples: []FewShotExample{{UserSays: "hi", BotSays: "yo"}}},
		GroupOverrides: map[int64]PersonaOverride{
			7: {Name: stringPtr("Group"), AllowTeasing: boolPtr(true), ToolAllowlist: []string{"query_memory"}},
		},
	}
	resolved := Resolve(base, 7, map[string]any{
		"speech_style":      "casual",
		"few_shot_examples": []any{map[string]any{"user_says": "bye", "bot_says": "later"}},
		"tool_allowlist":    []any{"speak_text"},
	})

	if resolved.Config.Name != "Group" || !resolved.Config.AllowTeasing || resolved.Config.SpeechStyle != "casual" {
		t.Fatalf("unexpected resolved config: %#v", resolved.Config)
	}
	if len(resolved.ToolAllowlist) != 1 || resolved.ToolAllowlist[0] != "speak_text" {
		t.Fatalf("unexpected tools: %#v", resolved.ToolAllowlist)
	}
	if len(resolved.FewShotExamples) != 1 || resolved.FewShotExamples[0].BotSays != "later" {
		t.Fatalf("unexpected examples: %#v", resolved.FewShotExamples)
	}
	if resolved.Version == "" || len(resolved.Hash) != 64 {
		t.Fatalf("missing identity: version=%q hash=%q", resolved.Version, resolved.Hash)
	}
}

func TestHashIgnoresVersionLabel(t *testing.T) {
	first := PersonaConfig{ID: "main", Name: "A", Version: "v1"}
	second := first
	second.Version = "v2"
	if Hash(first) != Hash(second) {
		t.Fatal("version label should not alter content hash")
	}
}

func stringPtr(value string) *string { return &value }
func boolPtr(value bool) *bool       { return &value }
