package postgresstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/phlin/go-agent/internal/application/modelusage"
)

func (s *Store) SaveModelUsage(ctx context.Context, metadata modelusage.Metadata, call modelusage.Call, final modelusage.FinalState, createdAt time.Time) error {
	tools, err := json.Marshal(call.Tools)
	if err != nil {
		return err
	}
	toolCalls, err := json.Marshal(call.ToolCalls)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO model_usage_records (
			event_id, trace_id, group_id, user_id, trigger, phase, iteration, input_tokens,
			cached_tokens, cache_miss_tokens, output_tokens, reasoning_tokens,
			duration_ms, tools_json, tool_calls_json, usage_available, error, sent, final_action,
			drop_reason, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`, metadata.EventID, metadata.TraceID, metadata.GroupID, metadata.UserID, metadata.Trigger, metadata.Phase,
		call.Iteration, call.InputTokens, call.CachedTokens, call.CacheMissTokens, call.OutputTokens,
		call.ReasoningTokens, call.DurationMS, tools, toolCalls, call.UsageAvailable, call.Error, final.Sent,
		final.Action, final.DropReason, createdAt)
	return err
}
