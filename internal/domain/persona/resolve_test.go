package persona

import "testing"

func TestResolvePreservesIdentity(t *testing.T) {
	base := PersonaConfig{
		ID: "main", Name: "Base", Description: "base", SpeechStyle: "short",
		Speech: SpeechPatterns{FewShotExamples: []FewShotExample{{UserSays: "hi", BotSays: "yo"}}},
	}
	resolved := Resolve(base)

	if resolved.Config.Name != "Base" || resolved.Config.SpeechStyle != "short" {
		t.Fatalf("unexpected resolved config: %#v", resolved.Config)
	}
	if len(resolved.FewShotExamples) != 1 || resolved.FewShotExamples[0].BotSays != "yo" {
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
