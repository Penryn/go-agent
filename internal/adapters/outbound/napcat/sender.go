package napcat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	napcatsdk "github.com/zjutjh/napcat-sdk"
	"github.com/zjutjh/napcat-sdk/api"
	"github.com/zjutjh/napcat-sdk/message"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Sender struct {
	client *napcatsdk.Client
}

func NewSender(baseURL, accessToken string, httpClient *http.Client) *Sender {
	options := []napcatsdk.Option{napcatsdk.WithToken(accessToken)}
	if httpClient == nil {
		options = append(options, napcatsdk.WithHTTPTimeout(5*time.Second))
	} else {
		options = append(options, napcatsdk.WithHTTPClient(httpClient))
	}

	return &Sender{client: napcatsdk.NewHTTPClient(baseURL, options...)}
}

func (s *Sender) Send(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	switch action.Kind {
	case policydomain.ActionReply, policydomain.ActionMemeOnly:
		return s.sendGroupMessage(ctx, action)
	case policydomain.ActionRecall:
		return s.sendRecall(ctx, action)
	case policydomain.ActionPokeBack:
		return s.sendPoke(ctx, action)
	case policydomain.ActionReact:
		return s.sendReact(ctx, action)
	default:
		return replydomain.ActionReceipt{ActionID: action.ActionID, Sent: false}, nil
	}
}

// MarkRead 回复前把该群标记已读（mark_group_msg_as_read），
// 模拟「看到之后才回」的真人时序。失败只记日志不阻断回复。
func (s *Sender) MarkRead(ctx context.Context, groupID int64, messageID string) error {
	group := strconv.FormatInt(groupID, 10)
	req := api.MarkGroupMsgAsReadRequest{GroupID: &group}
	if messageID != "" {
		req.MessageID = &messageID
	}
	if _, err := s.client.API().MarkGroupMsgAsRead(ctx, req); err != nil {
		return fmt.Errorf("mark_group_msg_as_read: %w", err)
	}
	return nil
}

// SetTyping 群聊没有「正在输入」能力（set_input_status 仅私聊生效），
// 保留接口占位：接入私聊场景后再启用。
func (s *Sender) SetTyping(ctx context.Context, _, _ int64) error {
	return nil
}

func (s *Sender) sendGroupMessage(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	request, err := BuildSendGroupMessageRequest(action)
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("build send_group_msg request: %w", err)
	}

	response, err := s.client.API().SendGroupMsg(ctx, request)
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("send_group_msg: %w", err)
	}

	return replydomain.ActionReceipt{
		ActionID:          action.ActionID,
		PlatformMessageID: strconv.FormatFloat(response.MessageID, 'f', -1, 64),
		Sent:              true,
	}, nil
}

func BuildSendGroupMessageRequest(action replydomain.ActionExecution) (api.SendGroupMsgRequest, error) {
	groupID := strconv.FormatInt(action.GroupID, 10)
	ob11Message, err := api.NewOB11Message(toSDKMessage(action.Segments))
	if err != nil {
		return api.SendGroupMsgRequest{}, err
	}

	return api.SendGroupMsgRequest{
		GroupID: &groupID,
		Message: ob11Message,
	}, nil
}

func toSDKMessage(segments []conversationdomain.MessageSegment) message.Chain {
	chain := make(message.Chain, len(segments))
	for i, segment := range segments {
		chain[i] = message.Segment{Type: segment.Type, Data: segment.Data}
	}
	return chain
}

func (s *Sender) sendRecall(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	messageID, err := json.Marshal(action.TargetMessageID)
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("encode delete_msg message_id: %w", err)
	}

	_, err = s.client.API().DeleteMsg(ctx, api.DeleteMsgRequest{
		MessageID: api.DeleteMsgRequestMessageIDUnion{Raw: messageID},
	})
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("delete_msg: %w", err)
	}

	return replydomain.ActionReceipt{
		ActionID:          action.ActionID,
		PlatformMessageID: action.TargetMessageID,
		Sent:              true,
	}, nil
}

func (s *Sender) sendPoke(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	groupID := strconv.FormatInt(action.GroupID, 10)
	_, err := s.client.API().GroupPoke(ctx, api.GroupPokeRequest{
		GroupID: &groupID,
		UserID:  strconv.FormatInt(action.TargetUserID, 10),
	})
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("group_poke: %w", err)
	}

	return replydomain.ActionReceipt{ActionID: action.ActionID, Sent: true}, nil
}

func (s *Sender) sendReact(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	emojiID, _ := action.Meta["emoji_id"].(string)
	if emojiID == "" {
		emojiID = "128077"
	}

	messageIDJSON, err := json.Marshal(action.TargetMessageID)
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("encode set_msg_emoji_like message_id: %w", err)
	}
	emojiIDJSON, err := json.Marshal(emojiID)
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("encode set_msg_emoji_like emoji_id: %w", err)
	}

	_, err = s.client.API().SetMsgEmojiLike(ctx, api.SetMsgEmojiLikeRequest{
		MessageID: api.SetMsgEmojiLikeRequestMessageIDUnion{Raw: messageIDJSON},
		EmojiID:   api.SetMsgEmojiLikeRequestEmojiIDUnion{Raw: emojiIDJSON},
	})
	if err != nil {
		return replydomain.ActionReceipt{}, fmt.Errorf("set_msg_emoji_like: %w", err)
	}

	return replydomain.ActionReceipt{ActionID: action.ActionID, Sent: true}, nil
}
