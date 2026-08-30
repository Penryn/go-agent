package textutil

import "testing"

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
