package modelusage

import (
	"context"
	"io"
	"testing"

	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeModel struct {
	response *schema.Message
}

func (f fakeModel) Generate(context.Context, []*schema.Message, ...modelcomponent.Option) (*schema.Message, error) {
	return f.response, nil
}

func (f fakeModel) Stream(context.Context, []*schema.Message, ...modelcomponent.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{f.response}), nil
}

func TestWrapRecordsUsageIterationAndTools(t *testing.T) {
	response := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "call-1", Type: "function", Function: schema.FunctionCall{Name: "query_memory"},
	}})
	response.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens:            120,
		PromptTokenDetails:      schema.PromptTokenDetails{CachedTokens: 80},
		CompletionTokens:        15,
		CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: 3},
	}}

	ctx, recorder := WithRecorder(context.Background(), Metadata{TraceID: "trace-1"})
	model := Wrap(fakeModel{response: response})
	if _, err := model.Generate(ctx, []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatal(err)
	}

	calls := recorder.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls=%d, want 1", len(calls))
	}
	call := calls[0]
	if call.Iteration != 1 || call.InputTokens != 120 || call.CachedTokens != 80 || call.OutputTokens != 15 || call.ReasoningTokens != 3 {
		t.Fatalf("unexpected usage: %+v", call)
	}
	if !call.UsageAvailable || len(call.Tools) != 1 || call.Tools[0] != "query_memory" {
		t.Fatalf("unexpected tool metadata: %+v", call)
	}
}

func TestWrapRecordsStreamingUsage(t *testing.T) {
	response := schema.AssistantMessage("done", nil)
	response.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 40, CompletionTokens: 5}}
	ctx, recorder := WithRecorder(context.Background(), Metadata{TraceID: "trace-stream"})
	reader, err := Wrap(fakeModel{response: response}).Stream(ctx, []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("stream end=%v, want EOF", err)
	}

	calls := recorder.Calls()
	if len(calls) != 1 || calls[0].InputTokens != 40 || calls[0].OutputTokens != 5 {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestApplyDeepSeekCacheUsageUsesProviderField(t *testing.T) {
	message := schema.AssistantMessage("done", nil)
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens:       120,
		PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 1},
	}}
	updated, err := applyDeepSeekCacheUsage(context.Background(), message, []byte(`{"usage":{"prompt_cache_hit_tokens":80}}`))
	if err != nil || updated.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 80 {
		t.Fatalf("cached tokens were not mapped: message=%+v err=%v", updated, err)
	}
}
