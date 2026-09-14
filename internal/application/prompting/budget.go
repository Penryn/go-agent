package prompting

import (
	"fmt"
	"strings"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// promptBudget centralizes every model-facing variable section. Byte limits
// are deterministic across providers; actual token usage is measured by the
// model-usage recorder rather than guessed with a provider-specific tokenizer.
type promptBudget struct {
	historyBytes    int
	memoryBytes     int
	mediaBytes      int
	thoughtBytes    int
	toolResultBytes int
}

var defaultPromptBudget = promptBudget{
	historyBytes:    4_800,
	memoryBytes:     1_800,
	mediaBytes:      1_800,
	thoughtBytes:    700,
	toolResultBytes: 4 * 1_024,
}

func memorySnippets(records []memorydomain.MemoryRecord, budget int) []string {
	seen := make(map[string]struct{}, len(records))
	result := make([]string, 0, len(records))
	for _, record := range records {
		key := strings.TrimSpace(record.MemoryID)
		if key == "" {
			key = fmt.Sprintf("%s\x00%s\x00%s", record.Type, record.Subject, record.Content)
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, formatMemorySnippet(record))
	}
	result, _ = retainLeadingStrings(result, budget)
	return result
}

func stringBytes(values []string) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}

// retainLeadingStrings is used for ranked data: retrieval and thought stores
// return the most relevant/newest item first, so trimming must preserve the
// head rather than the tail.
func retainLeadingStrings(values []string, budget int) ([]string, bool) {
	if budget <= 0 || len(values) == 0 {
		return values, false
	}
	used := 0
	end := 0
	for ; end < len(values); end++ {
		if used+len(values[end]) > budget {
			break
		}
		used += len(values[end])
	}
	if end == 0 {
		return []string{trimUTF8Bytes(values[0], budget)}, true
	}
	return values[:end], end < len(values)
}
