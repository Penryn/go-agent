package learning

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type Input struct {
	GroupID int64
	Events  []conversationdomain.ConversationEvent
}

type Output struct {
	Candidates []memorydomain.LearningCandidate
}

type Service struct {
	runnable compose.Runnable[Input, Output]
}

func New(ctx context.Context) (*Service, error) {
	workflow := compose.NewWorkflow[Input, Output]()
	workflow.AddLambdaNode("extract", compose.InvokableLambda(extractCandidates)).AddInput(compose.START)
	workflow.AddLambdaNode("review", compose.InvokableLambda(reviewCandidates)).AddInput("extract")
	workflow.End().AddInput("review")

	runnable, err := workflow.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{runnable: runnable}, nil
}

func (s *Service) Run(ctx context.Context, input Input) (Output, error) {
	return s.runnable.Invoke(ctx, input)
}

func extractCandidates(_ context.Context, input Input) (Output, error) {
	counter := map[string]int{}
	for _, event := range input.Events {
		text := strings.TrimSpace(event.Text)
		if len([]rune(text)) >= 2 && len([]rune(text)) <= 12 {
			counter[text]++
		}
	}

	output := Output{}
	for phrase, count := range counter {
		if count < 2 {
			continue
		}
		output.Candidates = append(output.Candidates, memorydomain.LearningCandidate{
			ID:            "candidate-" + phrase,
			GroupID:       input.GroupID,
			Kind:          "group_slang",
			Value:         phrase,
			Meaning:       "群里高频复用的表达",
			EvidenceCount: count,
			Confidence:    0.6 + float64(count)/10,
			Status:        "pending",
			CreatedAt:     time.Now(),
		})
	}
	return output, nil
}

func reviewCandidates(_ context.Context, input Output) (Output, error) {
	filtered := input.Candidates[:0]
	for _, candidate := range input.Candidates {
		if candidate.Confidence >= 0.7 {
			filtered = append(filtered, candidate)
		}
	}
	input.Candidates = filtered
	return input, nil
}
