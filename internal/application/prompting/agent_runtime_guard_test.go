package prompting

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/compose"
)

func TestToolRuntimeGuardBlocksDuplicateArgumentsRegardlessOfJSONSpacing(t *testing.T) {
	guard := newToolRuntimeGuard("trace-1", 4, 1024)
	var calls atomic.Int32
	next := guard.middleware(func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		calls.Add(1)
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})

	first, err := next(context.Background(), &compose.ToolInput{Name: "query_memory", Arguments: `{"query":"x","top_k":1}`})
	if err != nil || first.Result != `{"ok":true}` {
		t.Fatalf("first call: result=%q err=%v", first.Result, err)
	}
	second, err := next(context.Background(), &compose.ToolInput{Name: "query_memory", Arguments: `{ "top_k": 1, "query": "x" }`})
	if err != nil {
		t.Fatalf("duplicate call: %v", err)
	}
	if second.Result != `{"error":"duplicate_tool_call","retryable":false}` {
		t.Fatalf("unexpected duplicate result: %q", second.Result)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("underlying tool called %d times, want 1", got)
	}
}

func TestToolRuntimeGuardRetriesEmptyReadResult(t *testing.T) {
	guard := newToolRuntimeGuard("trace-1", 4, 1024)
	var calls atomic.Int32
	next := guard.middleware(func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		if calls.Add(1) == 1 {
			return &compose.ToolOutput{}, nil
		}
		return &compose.ToolOutput{Result: `{"items":[1]}`}, nil
	})

	result, err := next(context.Background(), &compose.ToolInput{Name: "query_memory", Arguments: `{}`})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.Result != `{"items":[1]}` || calls.Load() != 2 {
		t.Fatalf("result=%q calls=%d", result.Result, calls.Load())
	}
}

func TestTruncateToolResultPreservesJSONEnvelope(t *testing.T) {
	result, truncated := truncateToolResult(`{"items":["abcdefghijklmnopqrstuvwxyz"]}`, 32)
	if !truncated {
		t.Fatal("expected result to be marked truncated")
	}
	if len(result) > 32 {
		t.Fatalf("result is %d bytes, want <= 32: %q", len(result), result)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatalf("truncated result is not JSON: %v (%q)", err, result)
	}
	if envelope["truncated"] != true {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
}

func TestToolRuntimeGuardEnforcesCallBudget(t *testing.T) {
	guard := newToolRuntimeGuard("trace-1", 1, 1024)
	var calls atomic.Int32
	next := guard.middleware(func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		calls.Add(1)
		return &compose.ToolOutput{Result: `ok`}, nil
	})

	_, _ = next(context.Background(), &compose.ToolInput{Name: "query_memory", Arguments: `{"q":1}`})
	result, err := next(context.Background(), &compose.ToolInput{Name: "query_memory", Arguments: `{"q":2}`})
	if err != nil {
		t.Fatalf("budget call: %v", err)
	}
	if result.Result != `{"error":"tool_call_budget_exceeded","retryable":false}` {
		t.Fatalf("unexpected budget result: %q", result.Result)
	}
	if calls.Load() != 1 {
		t.Fatalf("underlying tool called %d times, want 1", calls.Load())
	}
}

func TestToolRuntimeGuardAllowsOnlyOneTerminalTool(t *testing.T) {
	guard := newToolRuntimeGuard("trace-1", 4, 1024, map[string]bool{
		"speak_text":  true,
		"stay_silent": true,
	})
	var calls atomic.Int32
	next := guard.middleware(func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		calls.Add(1)
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})
	_, _ = next(context.Background(), &compose.ToolInput{Name: "speak_text", Arguments: `{"text":"hi"}`})
	result, err := next(context.Background(), &compose.ToolInput{Name: "stay_silent", Arguments: `{}`})
	if err != nil {
		t.Fatalf("second terminal call: %v", err)
	}
	if result.Result != `{"error":"multiple_terminal_tool_calls","retryable":false}` {
		t.Fatalf("unexpected second terminal result: %q", result.Result)
	}
	if calls.Load() != 1 {
		t.Fatalf("underlying tool called %d times, want 1", calls.Load())
	}
}
