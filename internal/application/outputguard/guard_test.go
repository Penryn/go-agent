package outputguard

import "testing"

func TestTruncateCharsPrefersCompleteSentence(t *testing.T) {
	guard := New(12, 0)
	result := guard.Clean([]string{"前半句完整。后半句还没有结束"})
	if len(result.Bubbles) != 1 || result.Bubbles[0] != "前半句完整。" {
		t.Fatalf("unexpected guarded output: %#v", result)
	}
}

func TestCleanRemovesPromptMetadataLeak(t *testing.T) {
	guard := New(0, 0)
	result := guard.Clean([]string{"[时间=2026-09-03 00:27:15][你][msg_id=850233611] 行吧行吧……"})
	if len(result.Bubbles) != 1 || result.Bubbles[0] != "行吧行吧……" {
		t.Fatalf("prompt metadata leaked into output: %#v", result)
	}
}

func TestCleanSuppressesSelfReferentialAIWording(t *testing.T) {
	guard := New(0, 0)
	result := guard.Clean([]string{"还嫌我AI味，伤心了"})
	if !result.Suppressed {
		t.Fatalf("self-referential AI wording was not suppressed: %#v", result)
	}
}

func TestCleanRemovesPromptMetadataVariants(t *testing.T) {
	guard := New(0, 0)
	result := guard.Clean([]string{"[时间未知][用户7][群昵称=小明][QQ昵称=x][QQ=7] 这句留下"})
	if len(result.Bubbles) != 1 || result.Bubbles[0] != "这句留下" {
		t.Fatalf("prompt metadata variant leaked into output: %#v", result)
	}
}

func TestCleanRemovesTextEmojiButKeepsWords(t *testing.T) {
	guard := New(0, 0)
	result := guard.Clean([]string{"行吧😌，先这样😤"})
	if len(result.Bubbles) != 1 || result.Bubbles[0] != "行吧，先这样" {
		t.Fatalf("text emoji leaked into output: %#v", result)
	}
}
