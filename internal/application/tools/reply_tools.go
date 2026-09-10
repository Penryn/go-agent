package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/application/textutil"
)

// speak_text 工具 - 发送文本消息
type speakTextTool struct{}

func newSpeakTextTool() *speakTextTool { return &speakTextTool{} }

func (t *speakTextTool) Name() string { return "speak_text" }

func (t *speakTextTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "speak_text",
		Desc: "Send a text message in the current group chat",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"text":               {Type: "string", Desc: "The message text to send", Required: true},
			"bubbles":            {Type: "array", Desc: "Split message into multiple bubbles for readability"},
			"reply_to_message_id": {Type: "string", Desc: "ID of message to reply to (optional)"},
			"self_facts":         {Type: "array", Desc: "Facts about yourself you want remembered (optional)"},
		}),
	}, nil
}

func (t *speakTextTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args speakTextArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode speak_text args: %w", err)
	}
	if strings.TrimSpace(args.Text) == "" && len(args.Bubbles) == 0 {
		return "", errors.New("text or bubbles is required")
	}
	preview := args.Text
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	slog.Debug("tool: speak_text", "bubbles", len(args.Bubbles), "reply_to", args.ReplyToMessageID, "text", preview)
	result := speakTextResult{
		Tool:             "speak_text",
		Text:             strings.TrimSpace(args.Text),
		Bubbles:          compactStrings(args.Bubbles, 2),
		ReplyToMessageID: args.ReplyToMessageID,
		SelfFacts:        args.SelfFacts,
	}
	return marshal(result)
}

// stay_silent 工具 - 选择不回复
type staySilentTool struct{}

func newStaySilentTool() *staySilentTool { return &staySilentTool{} }

func (t *staySilentTool) Name() string { return "stay_silent" }

func (t *staySilentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "stay_silent",
		Desc: "Choose not to respond in the current context",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"reason_code": {Type: "string", Desc: "Reason code for staying silent", Required: true},
			"ttl_ms":      {Type: "integer", Desc: "Time to live in milliseconds"},
		}),
	}, nil
}

func (t *staySilentTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args staySilentArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode stay_silent args: %w", err)
	}
	slog.Debug("tool: stay_silent", "reason_code", args.ReasonCode, "ttl_ms", args.TTLMS)
	result := map[string]any{
		"tool":        "stay_silent",
		"reason_code": args.ReasonCode,
	}
	if args.TTLMS > 0 {
		result["ttl_ms"] = args.TTLMS
	}
	return marshal(result)
}

// react_emoji 工具 - 添加表情回应
type reactEmojiTool struct{}

func newReactEmojiTool() *reactEmojiTool { return &reactEmojiTool{} }
func (t *reactEmojiTool) Name() string   { return "react_emoji" }

func (t *reactEmojiTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "react_emoji",
		Desc: "Add an emoji reaction to a message",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"emoji_id":    {Type: "string", Desc: "Emoji ID to react with", Required: true},
			"message_id":  {Type: "string", Desc: "ID of the message to react to", Required: true},
			"reason_code": {Type: "string", Desc: "Reason code for the reaction"},
		}),
	}, nil
}

func (t *reactEmojiTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args reactEmojiArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode react_emoji args: %w", err)
	}
	if args.EmojiID == "" {
		args.EmojiID = "👍"
	}
	slog.Debug("tool: react_emoji", "emoji_id", args.EmojiID, "message_id", args.MessageID)
	result := reactEmojiResult{
		Tool:      "react_emoji",
		EmojiID:   args.EmojiID,
		MessageID: args.MessageID,
	}
	return marshal(result)
}

// quote_reply 工具 - 引用回复
type quoteReplyTool struct{}

func newQuoteReplyTool() *quoteReplyTool { return &quoteReplyTool{} }
func (t *quoteReplyTool) Name() string   { return "quote_reply" }
func (t *quoteReplyTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "quote_reply",
		Desc: "Reply to a specific message with a quote",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"reply_to_message_id": {Type: "string", Desc: "ID of message to quote", Required: true},
			"text":                {Type: "string", Desc: "Your reply text"},
			"bubbles":             {Type: "array", Desc: "Split reply into bubbles"},
			"self_facts":          {Type: "array", Desc: "Facts to remember"},
		}),
	}, nil
}

func (t *quoteReplyTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args quoteReplyArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode quote_reply args: %w", err)
	}
	if strings.TrimSpace(args.Text) == "" && len(args.Bubbles) == 0 {
		return "", errors.New("text or bubbles is required")
	}
	cleanText := textutil.StripThinkBlocks(args.Text)
	slog.Debug("tool: quote_reply", "reply_to", args.ReplyToMessageID, "text", cleanText)
	result := quoteReplyResult{
		Tool:             "quote_reply",
		ReplyToMessageID: args.ReplyToMessageID,
		Text:             cleanText,
		Bubbles:          compactStrings(args.Bubbles, 2),
		SelfFacts:        args.SelfFacts,
	}
	return marshal(result)
}

// poke_member 工具 - 戳一戳成员
type pokeMemberTool struct{}

func newPokeMemberTool() *pokeMemberTool { return &pokeMemberTool{} }
func (t *pokeMemberTool) Name() string   { return "poke_member" }
func (t *pokeMemberTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "poke_member",
		Desc: "Send a 'poke' action to a group member",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id": {Type: "integer", Desc: "User ID to poke", Required: true},
		}),
	}, nil
}

func (t *pokeMemberTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args pokeMemberArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode poke_member args: %w", err)
	}
	if args.UserID == 0 {
		return "", errors.New("user_id is required")
	}
	slog.Debug("tool: poke_member", "user_id", args.UserID)
	result := pokeMemberResult{
		Tool:   "poke_member",
		UserID: args.UserID,
	}
	return marshal(result)
}

// 辅助函数
func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(b), nil
}

func compactStrings(items []string, minLength int) []string {
	result := make([]string, 0, len(items))
	for _, s := range items {
		trimmed := strings.TrimSpace(s)
		if len(trimmed) >= minLength {
			result = append(result, trimmed)
		}
	}
	return result
}
