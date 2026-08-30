// Package textutil contains shared text normalization used by model-facing
// services before content reaches planning or delivery.
package textutil

import "strings"

// StripThinkBlocks removes model reasoning blocks from text. Some providers
// return a complete <think>...</think> pair while others strip the opening tag
// and leave only </think>; both forms are treated as non-user-facing content.
func StripThinkBlocks(s string) string {
	for {
		end := strings.Index(s, "</think>")
		if end < 0 {
			break
		}
		start := strings.Index(s, "<think>")
		if start >= 0 && start < end {
			s = s[:start] + s[end+len("</think>"):]
		} else {
			s = s[end+len("</think>"):]
		}
	}
	return strings.TrimSpace(s)
}
