// Package textutil contains shared helpers used across runtime layers.
package textutil

import (
	"strings"
	"time"
)

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

// TruncateRunes 按 rune 数截断文本，超出时以省略号结尾。
func TruncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// Backoff 返回第 attempt 次重试的指数退避时长：2^attempt 秒，钳制在 [min, max]。
// attempt <= 0 按首次等待处理，直接返回 min。outbox 任务与 ws 重连共用这一条曲线。
func Backoff(attempt int, floor, ceiling time.Duration) time.Duration {
	if attempt < 1 {
		return floor
	}
	// 移位前先钳位防溢出；attempt 超 30 秒级退避必然已达 ceiling
	d := time.Duration(int64(1)<<min(attempt, 30)) * time.Second
	return min(max(d, floor), ceiling)
}
