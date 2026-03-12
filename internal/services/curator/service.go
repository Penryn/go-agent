package curator

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/compose"

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

type Service struct {
	runnable compose.Runnable[Input, Output]
}

func New(ctx context.Context) (*Service, error) {
	graph := compose.NewGraph[Input, Output]()
	if err := graph.AddLambdaNode("extract", compose.InvokableLambda(extract)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode("review", compose.InvokableLambda(review)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(compose.START, "extract"); err != nil {
		return nil, err
	}
	if err := graph.AddEdge("extract", "review"); err != nil {
		return nil, err
	}
	if err := graph.AddEdge("review", compose.END); err != nil {
		return nil, err
	}
	runnable, err := graph.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{runnable: runnable}, nil
}

func (s *Service) Run(ctx context.Context, input Input) (Output, error) {
	return s.runnable.Invoke(ctx, input)
}

func extract(_ context.Context, input Input) (Output, error) {
	text := strings.TrimSpace(input.Snapshot.Event.Text)
	if text == "" {
		return Output{}, nil
	}

	output := Output{}
	if len([]rune(text)) <= 24 {
		output.MemoryIntents = append(output.MemoryIntents, memsvc.WriteIntent{
			Scope:         "group_curator",
			MemoryType:    "conversation_highlight",
			Subject:       "event",
			Content:       text,
			SourceEventID: input.Snapshot.Event.EventID,
			Importance:    0.7,
			Confidence:    0.8,
		})
	}
	return output, nil
}

func review(_ context.Context, output Output) (Output, error) {
	filtered := output.MemoryIntents[:0]
	for _, intent := range output.MemoryIntents {
		if intent.Confidence >= 0.7 && intent.Importance >= 0.5 {
			filtered = append(filtered, intent)
		}
	}
	output.MemoryIntents = filtered
	return output, nil
}
