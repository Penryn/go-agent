package prompting

import (
	"strings"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

// The context selector is deliberately small: the persona definition remains
// the source of truth, while only turn-relevant material crosses into prompt.
func relevantInterests(interests []string, trigger string) []string {
	if len(interests) == 0 {
		return nil
	}
	keywords := map[string][]string{
		"banter":       {"闲聊", "接梗", "热闹"},
		"support":      {"日常", "旅行", "故事"},
		"question":     {"旅行", "见闻", "日常"},
		"request_help": {"日常"},
	}
	return selectByKeywords(interests, keywords[trigger], 3)
}

func relevantBackground(summary, trigger string) string {
	lines := nonEmptyLines(summary)
	if len(lines) == 0 {
		return ""
	}
	keywords := map[string][]string{
		"support": {"孤独", "普通", "女孩", "善良", "敏感"},
		"banter":  {"热闹", "有趣", "庆典"},
	}
	if selected := selectByKeywords(lines, keywords[trigger], 1); len(selected) > 0 {
		return selected[0]
	}
	return lines[0]
}

func relevantTraits(traits []string, trigger string) []string {
	keywords := map[string][]string{
		"support":      {"敏感", "善良", "坚韧", "分寸"},
		"request_help": {"帮助", "认真", "顺从"},
		"banter":       {"活泼", "骄傲", "接梗", "热闹"},
	}
	if selected := selectByKeywords(traits, keywords[trigger], 3); len(selected) > 0 {
		return selected
	}
	return selectLimit(traits, 3)
}

func relevantHints(hints []string, trigger string) []string {
	if len(hints) == 0 {
		return nil
	}
	keywords := map[string][]string{
		"support":      {"倾诉", "情绪", "安静", "玩笑"},
		"request_help": {"请求", "任务", "完成", "追问"},
		"question":     {"不确定", "查证", "事实"},
		"banter":       {"调侃", "口语", "梗"},
	}
	if selected := selectByKeywords(hints, keywords[trigger], 2); len(selected) > 0 {
		return selected
	}
	return selectLimit(hints, 2)
}

func relevantFewShot(examples []personadomain.FewShotExample, trigger string) []personadomain.FewShotExample {
	keywords := map[string][]string{
		"support":      {"累", "难受", "烦心", "陪", "哭"},
		"request_help": {"帮", "查", "写", "需求", "问题"},
		"banter":       {"哈哈", "无聊", "笑", "游戏"},
		"question":     {"吗", "谁", "什么", "最近"},
	}
	terms := keywords[trigger]
	selected := make([]personadomain.FewShotExample, 0, 2)
	for _, example := range examples {
		if len(terms) == 0 || containsAnyText(example.UserSays, terms...) {
			selected = append(selected, example)
			if len(selected) == 2 {
				return selected
			}
		}
	}
	return selected
}

func selectByKeywords(values, keywords []string, limit int) []string {
	if len(keywords) == 0 {
		return nil
	}
	selected := make([]string, 0, limit)
	for _, value := range values {
		if containsAnyText(value, keywords...) {
			selected = append(selected, value)
			if len(selected) == limit {
				break
			}
		}
	}
	return selected
}

func containsAnyText(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func selectLimit[T any](values []T, limit int) []T {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
