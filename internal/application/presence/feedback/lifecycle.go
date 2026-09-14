package feedback

import (
	"context"

	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// LifecycleObserver wires the in-memory feedback window to the owning
// GroupActor. It deliberately records only attributable replies; unrelated
// group traffic is not treated as feedback.
type LifecycleObserver struct {
	working *groupactor.Manager
	windows *WindowManager
}

func NewLifecycleObserver(working *groupactor.Manager, recorder RelationshipRecorder) *LifecycleObserver {
	return &LifecycleObserver{working: working, windows: NewWindowManager(recorder)}
}

func (o *LifecycleObserver) ObserveInbound(ctx context.Context, event conversationdomain.ConversationEvent) error {
	if o == nil || o.working == nil || o.windows == nil || event.GroupID == 0 {
		return nil
	}
	return o.working.Update(ctx, event.GroupID, func(memory *presencedomain.GroupWorkingMemory) error {
		return o.windows.CheckInboundEvent(ctx, memory, event)
	})
}

func (o *LifecycleObserver) ObserveSent(ctx context.Context, event conversationdomain.ConversationEvent, receipt replydomain.ActionReceipt, decisionID string) error {
	if o == nil || o.working == nil || o.windows == nil || !receipt.Sent || receipt.PlatformMessageID == "" || event.GroupID == 0 {
		return nil
	}
	return o.working.Update(ctx, event.GroupID, func(memory *presencedomain.GroupWorkingMemory) error {
		o.windows.OpenWindow(memory, receipt.ActionID, receipt.PlatformMessageID, decisionID)
		return nil
	})
}
