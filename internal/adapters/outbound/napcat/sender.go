package napcat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Sender struct {
	baseURL     string
	accessToken string
	client      *http.Client
}

type sendGroupMsgRequest struct {
	GroupID int64                               `json:"group_id"`
	Message []conversationdomain.MessageSegment `json:"message"`
}

type sendGroupMsgResponse struct {
	Status  string `json:"status"`
	RetCode int    `json:"retcode"`
	Data    struct {
		MessageID any `json:"message_id"`
	} `json:"data"`
	Message string `json:"message"`
	Wording string `json:"wording"`
}

type recallMessageRequest struct {
	MessageID string `json:"message_id"`
}

type pokeRequest struct {
	GroupID int64 `json:"group_id"`
	UserID  int64 `json:"user_id"`
}

type reactRequest struct {
	MessageID string `json:"message_id"`
	EmojiID   string `json:"emoji_id"`
}

func NewSender(baseURL, accessToken string, client *http.Client) *Sender {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Sender{
		baseURL:     strings.TrimRight(baseURL, "/"),
		accessToken: accessToken,
		client:      client,
	}
}

func (s *Sender) Send(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	if action.Kind != policydomain.ActionReply && action.Kind != policydomain.ActionMemeOnly {
		switch action.Kind {
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

	reqBody := BuildSendGroupMessageRequest(action)
	data, err := json.Marshal(reqBody)
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/send_group_msg", bytes.NewReader(data))
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.accessToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}
	defer resp.Body.Close()

	var decoded sendGroupMsgResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return replydomain.ActionReceipt{}, err
	}
	if resp.StatusCode >= 300 || decoded.RetCode != 0 {
		return replydomain.ActionReceipt{}, fmt.Errorf("send_group_msg failed: status=%d retcode=%d message=%s", resp.StatusCode, decoded.RetCode, decoded.Message)
	}

	return replydomain.ActionReceipt{
		ActionID:          action.ActionID,
		PlatformMessageID: fmt.Sprintf("%v", decoded.Data.MessageID),
		Sent:              true,
	}, nil
}

func BuildSendGroupMessageRequest(action replydomain.ActionExecution) sendGroupMsgRequest {
	return sendGroupMsgRequest{
		GroupID: action.GroupID,
		Message: action.Segments,
	}
}

func (s *Sender) sendRecall(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	resp, err := s.postJSON(ctx, "/delete_msg", recallMessageRequest{MessageID: action.TargetMessageID})
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}
	return replydomain.ActionReceipt{ActionID: action.ActionID, PlatformMessageID: fmt.Sprintf("%v", resp.Data.MessageID), Sent: true}, nil
}

func (s *Sender) sendPoke(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	_, err := s.postJSON(ctx, "/group_poke", pokeRequest{GroupID: action.GroupID, UserID: action.TargetUserID})
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}
	return replydomain.ActionReceipt{ActionID: action.ActionID, Sent: true}, nil
}

func (s *Sender) sendReact(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	emojiID, _ := action.Meta["emoji_id"].(string)
	if emojiID == "" {
		emojiID = "128077" // default: thumbs up 👍 (U+1F44D)
	}
	_, err := s.postJSON(ctx, "/set_msg_emoji_like", reactRequest{MessageID: action.TargetMessageID, EmojiID: emojiID})
	if err != nil {
		return replydomain.ActionReceipt{}, err
	}
	return replydomain.ActionReceipt{ActionID: action.ActionID, Sent: true}, nil
}

func (s *Sender) postJSON(ctx context.Context, path string, payload any) (sendGroupMsgResponse, error) {
	var decoded sendGroupMsgResponse
	data, err := json.Marshal(payload)
	if err != nil {
		return decoded, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return decoded, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.accessToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return decoded, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return decoded, err
	}
	if resp.StatusCode >= 300 || decoded.RetCode != 0 {
		return decoded, fmt.Errorf("onebot call failed: status=%d retcode=%d message=%s", resp.StatusCode, decoded.RetCode, decoded.Message)
	}
	return decoded, nil
}
