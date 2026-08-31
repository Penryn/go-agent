package outputguard

import "testing"

func TestTruncateCharsPrefersCompleteSentence(t *testing.T) {
	guard := New(12, 0)
	result := guard.Clean([]string{"前半句完整。后半句还没有结束"})
	if len(result.Bubbles) != 1 || result.Bubbles[0] != "前半句完整。" {
		t.Fatalf("unexpected guarded output: %#v", result)
	}
}
