// Package modelusage records per-request main-model token usage and delays
// emission until the surrounding turn knows whether an outward action was sent.
package modelusage

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type Metadata struct {
	TraceID string
	GroupID int64
	UserID  int64
	Trigger string
	Phase   string
}

type FinalState struct {
	Sent        bool
	RateLimited bool
	Action      string
	DropReason  string
}

type Call struct {
	Iteration       int
	InputTokens     int
	CachedTokens    int
	CacheMissTokens int
	OutputTokens    int
	ReasoningTokens int
	Tools           []string
	UsageAvailable  bool
	Error           string
	DurationMS      int64
	startedAt       time.Time
}

type Sink interface {
	SaveModelUsage(context.Context, Metadata, Call, FinalState, time.Time) error
}

type Recorder struct {
	mu       sync.Mutex
	metadata Metadata
	calls    []*Call
	flushed  bool
	sink     Sink
}

type recorderKey struct{}

func WithRecorder(ctx context.Context, metadata Metadata) (context.Context, *Recorder) {
	recorder := &Recorder{metadata: metadata}
	return context.WithValue(ctx, recorderKey{}, recorder), recorder
}

func FromContext(ctx context.Context) *Recorder {
	recorder, _ := ctx.Value(recorderKey{}).(*Recorder)
	return recorder
}

func (r *Recorder) SetSink(sink Sink) {
	if r != nil {
		r.sink = sink
	}
}

// Calls returns a stable copy for tests and future metrics exporters.
func (r *Recorder) Calls() []Call {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Call, 0, len(r.calls))
	for _, call := range r.calls {
		copy := *call
		copy.Tools = slices.Clone(call.Tools)
		result = append(result, copy)
	}
	return result
}

func (r *Recorder) begin() *Call {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	call := &Call{Iteration: len(r.calls) + 1, startedAt: time.Now()}
	r.calls = append(r.calls, call)
	return call
}

func (r *Recorder) update(call *Call, message *schema.Message, callErr error) {
	if r == nil || call == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if callErr != nil {
		call.Error = callErr.Error()
	}
	if !call.startedAt.IsZero() {
		call.DurationMS = time.Since(call.startedAt).Milliseconds()
	}
	if message == nil {
		return
	}
	if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
		usage := message.ResponseMeta.Usage
		call.InputTokens = usage.PromptTokens
		call.CachedTokens = usage.PromptTokenDetails.CachedTokens
		if value, ok := message.Extra["prompt_cache_miss_tokens"].(int); ok {
			call.CacheMissTokens = value
		}
		call.OutputTokens = usage.CompletionTokens
		call.ReasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
		call.UsageAvailable = true
	}
	for _, toolCall := range message.ToolCalls {
		if name := strings.TrimSpace(toolCall.Function.Name); name != "" && !slices.Contains(call.Tools, name) {
			call.Tools = append(call.Tools, name)
		}
	}
}

// Flush emits one structured log entry per actual model invocation. It is
// idempotent because error paths and normal completion may share cleanup code.
func (r *Recorder) Flush(final FinalState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.flushed {
		r.mu.Unlock()
		return
	}
	r.flushed = true
	metadata := r.metadata
	sink := r.sink
	calls := make([]Call, 0, len(r.calls))
	for _, call := range r.calls {
		copy := *call
		copy.Tools = slices.Clone(call.Tools)
		calls = append(calls, copy)
	}
	r.mu.Unlock()

	for _, call := range calls {
		if sink != nil {
			_ = sink.SaveModelUsage(context.Background(), metadata, call, final, time.Now())
		}
		slog.Info("main model usage",
			"trace_id", metadata.TraceID,
			"group_id", metadata.GroupID,
			"user_id", metadata.UserID,
			"trigger", metadata.Trigger,
			"phase", metadata.Phase,
			"iteration", call.Iteration,
			"input_tokens", call.InputTokens,
			"cached_tokens", call.CachedTokens,
			"prompt_cache_miss_tokens", call.CacheMissTokens,
			"output_tokens", call.OutputTokens,
			"reasoning_tokens", call.ReasoningTokens,
			"usage_available", call.UsageAvailable,
			"tools", call.Tools,
			"sent", final.Sent,
			"rate_limited", final.RateLimited,
			"final_action", final.Action,
			"drop_reason", final.DropReason,
			"error", call.Error,
			"duration_ms", call.DurationMS,
		)
	}
}

type meteredModel struct {
	inner modelcomponent.BaseChatModel
}

func Wrap(model modelcomponent.BaseChatModel) modelcomponent.BaseChatModel {
	if model == nil {
		return nil
	}
	return &meteredModel{inner: model}
}

func (m *meteredModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomponent.Option) (*schema.Message, error) {
	recorder := FromContext(ctx)
	call := recorder.begin()
	message, err := m.inner.Generate(ctx, input, withCacheUsageOptions(m.inner, opts)...)
	recorder.update(call, message, err)
	return message, err
}

func (m *meteredModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomponent.Option) (*schema.StreamReader[*schema.Message], error) {
	recorder := FromContext(ctx)
	call := recorder.begin()
	reader, err := m.inner.Stream(ctx, input, withCacheUsageOptions(m.inner, opts)...)
	if err != nil {
		recorder.update(call, nil, err)
		return nil, err
	}
	return schema.StreamReaderWithConvert(reader, func(message *schema.Message) (*schema.Message, error) {
		recorder.update(call, message, nil)
		return message, nil
	}), nil
}

func withCacheUsageOptions(chatModel modelcomponent.BaseChatModel, opts []modelcomponent.Option) []modelcomponent.Option {
	if !isOpenAIModel(chatModel) {
		return opts
	}
	result := append([]modelcomponent.Option(nil), opts...)
	result = append(result,
		openaimodel.WithResponseMessageModifier(applyDeepSeekCacheUsage),
		openaimodel.WithResponseChunkMessageModifier(func(_ context.Context, message *schema.Message, raw []byte, _ bool) (*schema.Message, error) {
			return applyDeepSeekCacheUsage(context.Background(), message, raw)
		}),
	)
	return result
}

func isOpenAIModel(chatModel modelcomponent.BaseChatModel) bool {
	typeOf := reflect.TypeOf(chatModel)
	for typeOf != nil && typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	return typeOf != nil && typeOf.PkgPath() == "github.com/cloudwego/eino-ext/components/model/openai"
}

func applyDeepSeekCacheUsage(_ context.Context, message *schema.Message, raw []byte) (*schema.Message, error) {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return message, nil
	}
	var response struct {
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
		Usage                 struct {
			PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &response); err == nil {
		cachedTokens := response.Usage.PromptCacheHitTokens
		if cachedTokens == 0 {
			cachedTokens = response.PromptCacheHitTokens
		}
		if cachedTokens > 0 {
			message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens = cachedTokens
		}
		missTokens := response.Usage.PromptCacheMissTokens
		if missTokens == 0 {
			missTokens = response.PromptCacheMissTokens
		}
		if missTokens > 0 {
			if message.Extra == nil {
				message.Extra = make(map[string]any)
			}
			message.Extra["prompt_cache_miss_tokens"] = missTokens
		}
	}
	return message, nil
}
