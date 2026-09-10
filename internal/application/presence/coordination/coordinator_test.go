package coordination

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// Mock 实现
type mockWorkingMemory struct {
	observeCount int32
}

func (m *mockWorkingMemory) Observe(ctx context.Context, record presencedomain.EventRecord) (presencedomain.GroupWorkingMemory, error) {
	atomic.AddInt32(&m.observeCount, 1)
	return presencedomain.GroupWorkingMemory{}, nil
}

func (m *mockWorkingMemory) Snapshot(ctx context.Context, groupID int64) (presencedomain.GroupWorkingMemory, error) {
	return presencedomain.GroupWorkingMemory{GroupID: groupID}, nil
}

type mockDecisionEngine struct {
	shouldRespond bool
	decideCount   int32
	decideDelay   time.Duration
}

func (m *mockDecisionEngine) Decide(ctx context.Context, req *DecisionRequest) (*Decision, error) {
	atomic.AddInt32(&m.decideCount, 1)

	// 尊重 context 取消
	if m.decideDelay > 0 {
		select {
		case <-time.After(m.decideDelay):
			// 延迟完成
		case <-ctx.Done():
			// 被取消
			return nil, ctx.Err()
		}
	}

	return &Decision{ShouldRespond: m.shouldRespond}, nil
}

type mockPersonaAssembler struct{}

func (m *mockPersonaAssembler) Assemble(ctx context.Context, groupID int64, decision *Decision) (*PersonaContext, error) {
	return &PersonaContext{}, nil
}

type mockResponsePlanner struct{}

func (m *mockResponsePlanner) Plan(ctx context.Context, personaCtx *PersonaContext, decision *Decision) (*PlannedResponse, error) {
	return &PlannedResponse{
		Kind: "text",
		Text: "test response",
		Segments: []conversationdomain.MessageSegment{
			{Type: "text", Data: map[string]any{"text": "test response"}},
		},
	}, nil
}

type mockOutboundSender struct {
	sendCount int32
	sendDelay time.Duration
}

func (m *mockOutboundSender) Send(ctx context.Context, execution replydomain.ActionExecution) (*SendReceipt, error) {
	atomic.AddInt32(&m.sendCount, 1)
	if m.sendDelay > 0 {
		time.Sleep(m.sendDelay)
	}
	return &SendReceipt{
		EventID:           "evt_out_" + execution.ActionID,
		PlatformMessageID: "msg_" + execution.ActionID,
	}, nil
}

// 测试：同群串行执行
func TestMessageCoordinator_SerialExecution(t *testing.T) {
	workingMem := &mockWorkingMemory{}
	decisionEngine := &mockDecisionEngine{
		shouldRespond: true,
		decideDelay:   100 * time.Millisecond, // 模拟决策耗时
	}
	outbound := &mockOutboundSender{}

	coordinator := NewMessageCoordinator(
		workingMem,
		decisionEngine,
		&mockPersonaAssembler{},
		&mockResponsePlanner{},
		outbound,
	)

	groupID := int64(12345)
	ctx := context.Background()

	// 并发发送 5 条消息到同一个群
	var wg sync.WaitGroup
	errorCount := int32(0)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			record := presencedomain.EventRecord{
				EventID: generateEventID(idx),
				GroupID: groupID,
				Origin:  presencedomain.OriginInbound,
				Event: conversationdomain.ConversationEvent{
					EventID: generateEventID(idx),
					GroupID: groupID,
					Text:    "test message",
				},
			}
			err := coordinator.HandleInboundEvent(ctx, record)
			if err != nil && err.Error() == "group 12345 busy, skipping event "+generateEventID(idx) {
				// 预期的忙碌跳过
				atomic.AddInt32(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// 验证：只有一条消息被处理（其他被跳过）
	assert.Equal(t, int32(1), atomic.LoadInt32(&decisionEngine.decideCount), "只应决策一次")
	assert.Equal(t, int32(1), atomic.LoadInt32(&outbound.sendCount), "只应发送一次")
	assert.Equal(t, int32(4), errorCount, "应有4条消息被跳过")
}

// 测试：不同群可并行
func TestMessageCoordinator_ParallelAcrossGroups(t *testing.T) {
	workingMem := &mockWorkingMemory{}
	decisionEngine := &mockDecisionEngine{
		shouldRespond: true,
		decideDelay:   50 * time.Millisecond,
	}
	outbound := &mockOutboundSender{}

	coordinator := NewMessageCoordinator(
		workingMem,
		decisionEngine,
		&mockPersonaAssembler{},
		&mockResponsePlanner{},
		outbound,
	)

	ctx := context.Background()

	// 并发处理 3 个不同的群
	var wg sync.WaitGroup
	for groupID := int64(1); groupID <= 3; groupID++ {
		wg.Add(1)
		go func(gid int64) {
			defer wg.Done()
			record := presencedomain.EventRecord{
				EventID: generateEventID(int(gid)),
				GroupID: gid,
				Origin:  presencedomain.OriginInbound,
			}
			err := coordinator.HandleInboundEvent(ctx, record)
			assert.NoError(t, err)
		}(groupID)
	}

	wg.Wait()

	// 验证：3 个群都成功处理
	assert.Equal(t, int32(3), atomic.LoadInt32(&decisionEngine.decideCount))
	assert.Equal(t, int32(3), atomic.LoadInt32(&outbound.sendCount))
}

// 测试：不参与决策时不发送
func TestMessageCoordinator_NoResponseWhenDecided(t *testing.T) {
	workingMem := &mockWorkingMemory{}
	decisionEngine := &mockDecisionEngine{
		shouldRespond: false, // 决定不参与
	}
	outbound := &mockOutboundSender{}

	coordinator := NewMessageCoordinator(
		workingMem,
		decisionEngine,
		&mockPersonaAssembler{},
		&mockResponsePlanner{},
		outbound,
	)

	record := presencedomain.EventRecord{
		EventID: "evt_001",
		GroupID: 12345,
		Origin:  presencedomain.OriginInbound,
	}

	err := coordinator.HandleInboundEvent(context.Background(), record)
	require.NoError(t, err)

	// 验证：决策了，但没有发送
	assert.Equal(t, int32(1), atomic.LoadInt32(&decisionEngine.decideCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&outbound.sendCount))
}

// 测试：超时取消
func TestMessageCoordinator_TimeoutCancellation(t *testing.T) {
	workingMem := &mockWorkingMemory{}
	decisionEngine := &mockDecisionEngine{
		shouldRespond: true,
		decideDelay:   2 * time.Second, // 超过默认超时
	}
	outbound := &mockOutboundSender{}

	coordinator := NewMessageCoordinator(
		workingMem,
		decisionEngine,
		&mockPersonaAssembler{},
		&mockResponsePlanner{},
		outbound,
	)
	coordinator.turnTimeout = 500 * time.Millisecond // 设置短超时

	record := presencedomain.EventRecord{
		EventID: "evt_001",
		GroupID: 12345,
		Origin:  presencedomain.OriginInbound,
	}

	start := time.Now()
	err := coordinator.HandleInboundEvent(context.Background(), record)
	elapsed := time.Since(start)

	// 验证：应该在超时时间内返回错误
	assert.Error(t, err)
	assert.Less(t, elapsed, 1*time.Second, "应在超时后快速返回")
	assert.Equal(t, int32(0), atomic.LoadInt32(&outbound.sendCount), "超时应阻止发送")
}

// 测试：出站事件回写
func TestMessageCoordinator_OutboundEventRecording(t *testing.T) {
	workingMem := &mockWorkingMemory{}
	decisionEngine := &mockDecisionEngine{shouldRespond: true}
	outbound := &mockOutboundSender{}

	coordinator := NewMessageCoordinator(
		workingMem,
		decisionEngine,
		&mockPersonaAssembler{},
		&mockResponsePlanner{},
		outbound,
	)

	record := presencedomain.EventRecord{
		EventID: "evt_001",
		GroupID: 12345,
		Origin:  presencedomain.OriginInbound,
	}

	err := coordinator.HandleInboundEvent(context.Background(), record)
	require.NoError(t, err)

	// 验证：出站事件被记录
	assert.Equal(t, int32(1), atomic.LoadInt32(&workingMem.observeCount), "应记录出站事件")
}

// 测试：外部取消群执行
func TestMessageCoordinator_ExternalCancellation(t *testing.T) {
	workingMem := &mockWorkingMemory{}
	decisionEngine := &mockDecisionEngine{
		shouldRespond: true,
		decideDelay:   500 * time.Millisecond,
	}
	outbound := &mockOutboundSender{}

	coordinator := NewMessageCoordinator(
		workingMem,
		decisionEngine,
		&mockPersonaAssembler{},
		&mockResponsePlanner{},
		outbound,
	)

	groupID := int64(12345)
	record := presencedomain.EventRecord{
		EventID: "evt_001",
		GroupID: groupID,
		Origin:  presencedomain.OriginInbound,
	}

	// 启动处理
	done := make(chan error)
	go func() {
		done <- coordinator.HandleInboundEvent(context.Background(), record)
	}()

	// 100ms 后取消
	time.Sleep(100 * time.Millisecond)
	coordinator.CancelGroup(groupID)

	// 等待完成
	err := <-done

	// 验证：应该被取消
	assert.Error(t, err, "应该被取消")
	assert.Equal(t, int32(0), atomic.LoadInt32(&outbound.sendCount), "取消应阻止发送")
}

// 辅助函数
func generateEventID(idx int) string {
	return "evt_" + string(rune('0'+idx))
}
