package textutil

import (
	"testing"
	"time"
)

func TestStripThinkBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "paired", input: "before<think>hidden</think>after", want: "beforeafter"},
		{name: "orphan close", input: "hidden reasoning</think>visible", want: "visible"},
		{name: "multiple", input: "<think>a</think>x<think>b</think>y", want: "xy"},
		{name: "trim", input: "  <think>hidden</think> answer  ", want: "answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripThinkBlocks(tt.input); got != tt.want {
				t.Fatalf("StripThinkBlocks(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	min, max := time.Second, 30*time.Second
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: time.Second}, // 钳到 min（等价原 minBackoff）
		{attempt: 1, want: 2 * time.Second},
		{attempt: 4, want: 16 * time.Second},
		{attempt: 5, want: 30 * time.Second}, // 32s 超上限，钳到 max
		{attempt: 99, want: 30 * time.Second},
	}
	for _, tt := range tests {
		if got := Backoff(tt.attempt, min, max); got != tt.want {
			t.Fatalf("Backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}
