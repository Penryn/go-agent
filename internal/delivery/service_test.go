package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/phlin/go-agent/internal/qq"
)

type testSender struct{ err error }

func (s testSender) Send(context.Context, int64, string, string, string) (qq.Receipt, error) {
	if s.err != nil {
		return qq.Receipt{}, s.err
	}
	return qq.Receipt{MessageID: "m1"}, nil
}

func TestStateMachineConfirmsOnlyAfterSender(t *testing.T) {
	var confirmed bool
	s := StateMachine{Sender: testSender{}, Confirm: func(context.Context, Request, qq.Receipt) error { confirmed = true; return nil }}
	if _, err := s.Deliver(context.Background(), Request{GroupID: 1, Text: "hi"}); err != nil || !confirmed {
		t.Fatalf("delivery not confirmed: %v %v", err, confirmed)
	}
	unknown := false
	s = StateMachine{Sender: testSender{err: &qq.SendError{Err: errors.New("timeout"), Definite: false}}, Failure: func(_ context.Context, _ Request, _ error, definite bool) error { unknown = !definite; return nil }}
	if _, err := s.Deliver(context.Background(), Request{GroupID: 1}); err == nil || !unknown {
		t.Fatal("uncertain send was not reported")
	}
}
