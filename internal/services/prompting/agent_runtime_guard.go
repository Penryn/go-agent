package prompting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cloudwego/eino/compose"
)

const (
	defaultMaxIterations      = 4
	defaultMaxToolCalls       = 12
	defaultToolResultMaxBytes = 12 * 1024
)

type toolRuntimeGuard struct {
	traceID       string
	maxToolCalls  int
	maxResultByte int

	mu    sync.Mutex
	calls int
	seen  map[string]struct{}
}

func newToolRuntimeGuard(traceID string, maxToolCalls, maxResultBytes int) *toolRuntimeGuard {
	return &toolRuntimeGuard{
		traceID:       traceID,
		maxToolCalls:  maxToolCalls,
		maxResultByte: maxResultBytes,
		seen:          make(map[string]struct{}),
	}
}

// middleware is installed on Eino's ToolsNode. State is scoped to one Plan
// invocation, so concurrent groups cannot suppress each other's calls.
func (g *toolRuntimeGuard) middleware(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		if input == nil {
			return next(ctx, input)
		}

		key := input.Name + ":" + hashToolArguments(input.Arguments)
		g.mu.Lock()
		if g.maxToolCalls > 0 && g.calls >= g.maxToolCalls {
			g.mu.Unlock()
			return &compose.ToolOutput{Result: `{"error":"tool_call_budget_exceeded","retryable":false}`}, nil
		}
		g.calls++
		if _, duplicate := g.seen[key]; duplicate {
			g.mu.Unlock()
			return &compose.ToolOutput{Result: `{"error":"duplicate_tool_call","retryable":false}`}, nil
		}
		g.seen[key] = struct{}{}
		g.mu.Unlock()

		result, err := next(ctx, input)
		if err != nil {
			return result, err
		}

		// A transient empty response is retried only for read-only tools. This
		// avoids replaying side effects while fixing flaky retrieval providers.
		if result == nil {
			result = &compose.ToolOutput{}
		}
		if strings.TrimSpace(result.Result) == "" && retryEmptyTool(input.Name) {
			retryResult, retryErr := next(ctx, input)
			if retryErr != nil {
				return retryResult, retryErr
			}
			result = retryResult
		}

		result.Result, _ = truncateToolResult(result.Result, g.maxResultByte)
		return result, nil
	}
}

func hashToolArguments(raw string) string {
	normalized := strings.TrimSpace(raw)
	var value any
	if json.Unmarshal([]byte(normalized), &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			normalized = string(encoded)
		}
	}
	digest := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(digest[:])
}

func retryEmptyTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "query_") ||
		strings.HasPrefix(name, "search_") ||
		name == "recall_recent_message"
}

// truncateToolResult keeps the model-facing result bounded while preserving a
// valid JSON envelope when the original result was JSON.
func truncateToolResult(result string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(result) <= maxBytes {
		return result, false
	}
	const marker = "...[truncated]"
	if maxBytes <= len(marker) {
		return string([]byte(result)[:maxBytes]), true
	}
	previewLimit := maxBytes - len(marker)
	preview := trimUTF8Bytes(result, previewLimit)
	if json.Valid([]byte(result)) && maxBytes >= len(`{"truncated":true,"preview":""}`) {
		// Keep this envelope valid so the next model turn can reason about the
		// truncation instead of receiving malformed tool JSON.
		for {
			envelope, _ := json.Marshal(map[string]any{"truncated": true, "preview": preview})
			if len(envelope) <= maxBytes {
				return string(envelope), true
			}
			preview = trimUTF8Bytes(preview, len(preview)-1)
		}
	}
	return trimUTF8Bytes(result, previewLimit) + marker, true
}

func trimUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	trimmed := value[:maxBytes]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
