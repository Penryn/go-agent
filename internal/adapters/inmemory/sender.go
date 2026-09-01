// Package inmemory 只保留测试与降级共用的内存替身 Sender。
// 曾经的内存 Store 已删除:生产与测试统一走 postgresstore(docker-compose
// 提供 PG,测试通过 internal/testsupport 建临时库)。
package inmemory

import (
	"context"
	"sync"

	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/application/ports"
)

var _ ports.OutboundSender = (*Sender)(nil)

type Sender struct {
	mu      sync.Mutex
	actions []replydomain.ActionExecution
}

func NewSender() *Sender {
	return &Sender{}
}

func (s *Sender) Send(_ context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.actions = append(s.actions, action)
	return replydomain.ActionReceipt{
		ActionID:          action.ActionID,
		PlatformMessageID: action.ActionID + "-sent",
		Sent:              true,
	}, nil
}

func (s *Sender) Actions() []replydomain.ActionExecution {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]replydomain.ActionExecution(nil), s.actions...)
}

// MarkRead 记录已读回执调用，供测试断言「发送前先标已读」的时序。
func (s *Sender) MarkRead(_ context.Context, _ int64, _ string) error {
	return nil
}
