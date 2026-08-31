package curator

import (
	"context"
	"strings"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
)

type Input struct {
	Snapshot conversationdomain.ContextSnapshot
}

type Output struct {
	MemoryIntents   []memsvc.WriteIntent
	TraitCandidates []profiledomain.MemberTrait
}

// Service 从一轮对话快照里提取群聊亮点。两个阈值：extract 的 confidence=0.8
// 恒大于 review 的过滤线 0.7，所以整条链等价于「短文本直接入库」。
type Service struct{}

func New(_ context.Context) (*Service, error) { return &Service{}, nil }

func (s *Service) Run(_ context.Context, input Input) (Output, error) {
	text := strings.TrimSpace(input.Snapshot.Event.Text)
	if text == "" || len([]rune(text)) > 24 {
		return Output{}, nil
	}
	return Output{MemoryIntents: []memsvc.WriteIntent{{
		Scope:         "group_curator",
		MemoryType:    "conversation_highlight",
		Subject:       "event",
		Content:       text,
		SourceEventID: input.Snapshot.Event.EventID,
		Importance:    0.7,
		Confidence:    0.8,
	}}}, nil
}
