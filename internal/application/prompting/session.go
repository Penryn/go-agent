package prompting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func toolSchemaHash(ctx context.Context, tools []tool.BaseTool) string {
	hash := sha256.New()
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		raw, err := json.Marshal(info)
		if err != nil {
			continue
		}
		hash.Write(raw)
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)[:8])
}

func toolSchemaBytes(ctx context.Context, tools []tool.BaseTool) int {
	total := 0
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		raw, err := json.Marshal(info)
		if err == nil {
			total += len(raw)
		}
	}
	return total
}

func promptMessageBytes(messages []*schema.Message) int {
	total := 0
	for _, message := range messages {
		if message == nil {
			continue
		}
		total += len(message.Content)
		for _, call := range message.ToolCalls {
			total += len(call.ID) + len(call.Type) + len(call.Function.Name) + len(call.Function.Arguments)
		}
	}
	return total
}
