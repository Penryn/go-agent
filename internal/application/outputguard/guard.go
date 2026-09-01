package outputguard

import (
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/phlin/go-agent/internal/application/textutil"
)

// Result 描述清洗结果。
type Result struct {
	Bubbles    []string // 清洗后的气泡列表
	Suppressed bool     // true = 整条回复应被静默，不发送任何内容
	Reasons    []string // 触发的规则名，用于日志审计
}

// Guard 在内容发出前应用全部输出清洗规则。
type Guard struct {
	maxChars     int // 0 = 不限制
	maxSentences int // 0 = 不限制
}

// New 创建 Guard 实例；maxChars/maxSentences 为 0 时对应规则不限制。
func New(maxChars, maxSentences int) *Guard {
	return &Guard{maxChars: maxChars, maxSentences: maxSentences}
}

// ---- 规则实现 ----

// 规则1：ThinkBlock 过滤，清除 <think>...</think>（含跨行）
func stripThinkBlocks(s string) (string, bool) {
	cleaned := textutil.StripThinkBlocks(s)
	return cleaned, cleaned != s
}

// 规则2：RoleBreak 检测，匹配模型身份暴露关键词，命中则整条回复静默。
var roleBreakPatterns = []string{
	"我是ai", "我是人工智能", "我是语言模型", "我是大语言模型",
	"我是llm", "我是chatgpt", "我是claude", "我是gpt",
	"作为ai", "作为语言模型", "作为人工智能",
	"as an ai", "as a language model", "i'm an ai", "i am an ai",
}

func detectRoleBreak(s string) bool {
	lower := strings.ToLower(s)
	noSpace := strings.ReplaceAll(lower, " ", "")
	for _, pat := range roleBreakPatterns {
		if strings.Contains(noSpace, pat) {
			return true
		}
	}
	return false
}

// 规则3：字符数截断（按 Unicode rune 计算）
func truncateChars(s string, max int) (string, bool) {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s, false
	}
	runes := []rune(s)
	for i := max - 1; i >= 0; i-- {
		if strings.ContainsRune("。！？!?…", runes[i]) {
			return string(runes[:i+1]), true
		}
	}
	return string(runes[:max]) + "…", true
}

// 规则4：句子数截断，按中英文句末标点分割
var sentenceEndRe = regexp.MustCompile(`[。！？!?…]+`)

func truncateSentences(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	indices := sentenceEndRe.FindAllStringIndex(s, -1)
	if len(indices) <= max {
		return s, false
	}
	cutAt := indices[max-1][1]
	return s[:cutAt], true
}

// 规则5：空气泡清理，移除 think 过滤后产生的空字符串气泡
func removeEmptyBubbles(bubbles []string) []string {
	out := bubbles[:0]
	for _, b := range bubbles {
		if strings.TrimSpace(b) != "" {
			out = append(out, b)
		}
	}
	return out
}

// ---- Clean 主流程 ----

func (g *Guard) Clean(bubbles []string) Result {
	result := Result{Bubbles: make([]string, 0, len(bubbles))}

	for _, bubble := range bubbles {
		original := bubble

		// 规则1：剥除 think block
		cleaned, thinkStripped := stripThinkBlocks(bubble)
		if thinkStripped {
			result.Reasons = appendUniq(result.Reasons, "think_block_stripped")
			slog.Debug("outputguard: think block stripped",
				"original_len", len(original), "cleaned_len", len(cleaned))
		}

		// 规则2：RoleBreak — 命中则整条回复静默
		if detectRoleBreak(cleaned) {
			result.Suppressed = true
			result.Reasons = appendUniq(result.Reasons, "role_break_detected")
			slog.Warn("outputguard: role break detected, suppressing reply",
				"bubble_preview", textutil.TruncateRunes(cleaned, 40))
			return Result{
				Suppressed: true,
				Reasons:    result.Reasons,
			}
		}

		// 规则4：句子数截断（先于字符数，保证语义完整）
		if g.maxSentences > 0 {
			truncated, cut := truncateSentences(cleaned, g.maxSentences)
			if cut {
				result.Reasons = appendUniq(result.Reasons, "sentence_truncated")
				cleaned = truncated
			}
		}

		// 规则3：字符数截断
		if g.maxChars > 0 {
			truncated, cut := truncateChars(cleaned, g.maxChars)
			if cut {
				result.Reasons = appendUniq(result.Reasons, "char_truncated")
				cleaned = truncated
			}
		}

		result.Bubbles = append(result.Bubbles, cleaned)
	}

	// 规则5：移除空气泡
	result.Bubbles = removeEmptyBubbles(result.Bubbles)

	return result
}

func appendUniq(slice []string, s string) []string {
	if slices.Contains(slice, s) {
		return slice
	}
	return append(slice, s)
}

