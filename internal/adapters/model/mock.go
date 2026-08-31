package model

import (
	"context"
	"sync"

	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// StaticFactory / MockChatModel 仅供跨包测试使用（prompting、multimodal）。

type StaticFactory struct {
	MainModel   modelcomponent.BaseChatModel
	VisionModel modelcomponent.BaseChatModel
}

func (f StaticFactory) MainChatModel(_ context.Context) (modelcomponent.BaseChatModel, error) {
	if f.MainModel == nil {
		return nil, ErrModelUnavailable
	}
	return f.MainModel, nil
}

func (f StaticFactory) VisionChatModel(_ context.Context) (modelcomponent.BaseChatModel, error) {
	if f.VisionModel == nil {
		return nil, ErrModelUnavailable
	}
	return f.VisionModel, nil
}

type MockChatModel struct {
	mu        sync.Mutex
	responses []*schema.Message
	index     int
	inputs    [][]*schema.Message
}

func NewMockChatModel(responses ...*schema.Message) *MockChatModel {
	return &MockChatModel{responses: responses}
}

func (m *MockChatModel) Generate(_ context.Context, input []*schema.Message, _ ...modelcomponent.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(input) > 0 {
		m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	}

	if len(m.responses) == 0 {
		return schema.AssistantMessage("", nil), nil
	}

	if m.index >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}

	response := m.responses[m.index]
	m.index++
	return response, nil
}

func (m *MockChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...modelcomponent.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *MockChatModel) Inputs() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]*schema.Message(nil), m.inputs...)
}
