package delivery

import (
	"context"
	"errors"

	"github.com/phlin/go-agent/internal/qq"
)

type Request struct {
	GroupID int64
	Text    string
	Quote   string
	Path    string
}

type Service interface {
	Deliver(context.Context, Request) (qq.Receipt, error)
}

// StateMachine preserves the send boundary used by chat: mark sending before
// the OneBot call, confirm only after the call succeeds, and leave uncertain
// transport failures as unknown without retrying.
type StateMachine struct {
	Sender  Sender
	Before  func(context.Context, Request) error
	Confirm func(context.Context, Request, qq.Receipt) error
	Failure func(context.Context, Request, error, bool) error
}

type Sender interface {
	Send(context.Context, int64, string, string, string) (qq.Receipt, error)
}

func (s StateMachine) Deliver(ctx context.Context, req Request) (qq.Receipt, error) {
	if s.Sender == nil {
		return qq.Receipt{}, errors.New("delivery sender unavailable")
	}
	if s.Before != nil {
		if err := s.Before(ctx, req); err != nil {
			return qq.Receipt{}, err
		}
	}
	receipt, err := s.Sender.Send(ctx, req.GroupID, req.Text, req.Quote, req.Path)
	if err != nil {
		definite := false
		var sendErr *qq.SendError
		if errors.As(err, &sendErr) {
			definite = sendErr.Definite
		}
		if s.Failure != nil {
			_ = s.Failure(context.WithoutCancel(ctx), req, err, definite)
		}
		return qq.Receipt{}, err
	}
	if s.Confirm != nil {
		if err := s.Confirm(context.WithoutCancel(ctx), req, receipt); err != nil {
			return receipt, err
		}
	}
	return receipt, nil
}
