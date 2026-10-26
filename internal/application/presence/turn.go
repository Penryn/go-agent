package presence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/phlin/go-agent/internal/application/modelusage"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	workspaceapp "github.com/phlin/go-agent/internal/application/workspace"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

func (r *Runtime) deliberate(ctx context.Context, envelope conversationdomain.EventEnvelope) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{Envelope: envelope}, err
	}
	if r.deps.Turns != nil {
		allowed, err := r.deps.Turns.CanDeliberate(ctx, envelope.Event.GroupID, envelope.ReceivedAt)
		if err != nil {
			return Outcome{Envelope: envelope}, err
		}
		if !allowed {
			return r.silentBeforeModel(ctx, envelope, "turn_gate")
		}
	}
	if r.deps.Deliberator == nil {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "deliberator_unavailable")}, nil
	}
	var workspaceTask workspaceapp.Task
	if r.deps.Workspaces != nil && hasFileAttachment(envelope.Event.Attachments) {
		prepared, prepareErr := r.prepareWorkspace(ctx, envelope)
		if prepareErr != nil {
			slog.Warn("presence: prepare attachment workspace failed", "event_id", envelope.Event.EventID, "err", prepareErr)
		} else {
			workspaceTask = prepared
			defer func() {
				if cleanupErr := r.deps.Workspaces.Cleanup(workspaceTask.Workspace); cleanupErr != nil {
					slog.Warn("presence: cleanup attachment workspace failed", "workspace", workspaceTask.Workspace.Root, "err", cleanupErr)
				}
			}()
		}
	}
	planningCtx, usageRecorder := modelusage.WithRecorder(ctx, modelusage.Metadata{
		TraceID: envelope.TraceID, EventID: envelope.Event.EventID, GroupID: envelope.Event.GroupID,
		UserID: envelope.Event.UserID, Phase: "reply_planner",
	})
	if workspaceTask.Workspace.Root != "" {
		planningCtx = workspaceapp.WithTask(planningCtx, workspaceTask)
	}
	usageRecorder.SetSink(r.deps.ModelUsage)
	finalUsage := modelusage.FinalState{}
	defer func() { usageRecorder.Flush(finalUsage) }()
	result, err := r.deps.Deliberator.Deliberate(planningCtx, envelope)
	if err != nil {
		return Outcome{Envelope: envelope}, err
	}
	usageRecorder.SetTrigger(result.Decision.TriggerType)
	finalUsage.Action = string(result.Decision.Action)
	if err := ctx.Err(); err != nil {
		return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan}, err
	}
	if r.deps.Thoughts != nil {
		if thoughtErr := r.deps.Thoughts.SaveThought(ctx, result.Thought); thoughtErr != nil {
			slog.Warn("presence: save thought failed", "event_id", envelope.Event.EventID, "err", thoughtErr)
		}
	}
	if len(result.Plan.Bubbles) == 0 && result.Plan.FallbackText == "" && len(result.Plan.PlannedActions) == 0 {
		return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision}, nil
	}
	var proposal personasvc.CanonProposal
	if r.deps.Canon != nil && len(result.Plan.ProposedPersonaFacts) > 0 {
		proposal, err = r.deps.Canon.PreparePlan(ctx, result.Snapshot.PersonaView, result.Plan.ProposedPersonaFacts, plannedReplyText(result.Plan), result.Decision.DecisionID)
		if err != nil {
			return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan}, err
		}
		result.Plan.ProposedPersonaFacts = proposal.Candidates
	}
	if r.deps.Executor == nil {
		return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan}, nil
	}
	receipt, err := r.deps.Executor.Execute(ctx, result.Snapshot, result.Decision, result.Plan)
	finalUsage.Sent = receipt.Sent
	finalUsage.Action = string(result.Decision.Action)
	finalUsage.DropReason = receipt.DropReason
	outcome := Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan, Receipt: receipt}
	var lifecycleErrs []error
	if r.deps.Feedback != nil {
		if feedbackErr := runLifecycleHook(ctx, receipt.Sent, func(hookCtx context.Context) error {
			return r.deps.Feedback.ObserveSent(hookCtx, envelope.Event, receipt, result.Decision.DecisionID)
		}); feedbackErr != nil {
			lifecycleErrs = append(lifecycleErrs, fmt.Errorf("observe sent feedback: %w", feedbackErr))
		}
	}
	if proposal.ProposalID != "" {
		if receipt.Sent {
			if canonErr := runLifecycleHook(ctx, true, func(hookCtx context.Context) error {
				return r.deps.Canon.AfterDelivery(hookCtx, proposal, personasvc.CanonDelivery{
					GroupID: envelope.Event.GroupID, SelfID: envelope.SelfID, SourceEventID: receipt.ActionID, Text: receipt.DeliveredText,
				})
			}); canonErr != nil {
				lifecycleErrs = append(lifecycleErrs, fmt.Errorf("finalize persona canon: %w", canonErr))
			}
		} else {
			if canonErr := runLifecycleHook(ctx, false, func(hookCtx context.Context) error {
				return r.deps.Canon.AbortProposal(hookCtx, proposal)
			}); canonErr != nil {
				lifecycleErrs = append(lifecycleErrs, fmt.Errorf("abort persona canon: %w", canonErr))
			}
		}
	}
	// A partial multi-bubble send is already externally visible. Complete every
	// post-send hook even when Execute returned an error so cooldown, persona,
	// and feedback state cannot diverge based on hook ordering.
	if r.deps.Turns != nil && (err == nil || receipt.Sent) {
		if turnErr := runLifecycleHook(ctx, receipt.Sent, func(hookCtx context.Context) error {
			return r.deps.Turns.AfterTurn(hookCtx, result.Snapshot, result.Decision, receipt)
		}); turnErr != nil {
			lifecycleErrs = append(lifecycleErrs, fmt.Errorf("observe completed turn: %w", turnErr))
		}
	}
	return outcome, errors.Join(append([]error{err}, lifecycleErrs...)...)
}

func hasFileAttachment(attachments []mediadomain.MultimodalAttachment) bool {
	for _, attachment := range attachments {
		if attachment.Kind == mediadomain.MediaFile {
			return true
		}
	}
	return false
}

func (r *Runtime) prepareWorkspace(ctx context.Context, envelope conversationdomain.EventEnvelope) (workspaceapp.Task, error) {
	ws, err := r.deps.Workspaces.Create(ctx, envelope.Event.EventID)
	if err != nil {
		return workspaceapp.Task{}, err
	}
	task := workspaceapp.Task{Manager: r.deps.Workspaces, Workspace: ws}
	for _, attachment := range envelope.Event.Attachments {
		if attachment.Kind != mediadomain.MediaFile {
			continue
		}
		path, stageErr := r.deps.Workspaces.StageAttachment(ctx, ws, attachment)
		if stageErr != nil {
			_ = r.deps.Workspaces.Cleanup(ws)
			return workspaceapp.Task{}, fmt.Errorf("stage attachment %s: %w", attachment.AttachmentID, stageErr)
		}
		relative, relErr := filepath.Rel(ws.Root, path)
		if relErr != nil {
			_ = r.deps.Workspaces.Cleanup(ws)
			return workspaceapp.Task{}, relErr
		}
		task.InputFiles = append(task.InputFiles, "./"+filepath.ToSlash(relative))
	}
	return task, nil
}

func runLifecycleHook(ctx context.Context, detach bool, hook func(context.Context) error) error {
	if !detach {
		return hook(ctx)
	}
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postSendHookTimeout)
	defer cancel()
	return hook(hookCtx)
}

func (r *Runtime) silentBeforeModel(ctx context.Context, envelope conversationdomain.EventEnvelope, reason string) (Outcome, error) {
	decision := silentDecision(envelope.TraceID, reason)
	plan := replydomain.ReplyPlan{PlanID: decision.DecisionID + "-plan", PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent}, SendMode: "silent"}
	var receipt replydomain.ActionReceipt
	var err error
	if r.deps.Executor != nil {
		receipt, err = r.deps.Executor.Execute(ctx, conversationdomain.ContextSnapshot{Event: envelope.Event}, decision, plan)
	} else {
		receipt.DropReason = "action_silent"
	}
	return Outcome{Envelope: envelope, Decision: decision, Plan: plan, Receipt: receipt}, err
}

func plannedReplyText(plan replydomain.ReplyPlan) string {
	if len(plan.Bubbles) > 0 {
		return strings.Join(plan.Bubbles, "")
	}
	if corrected, _ := plan.ActionParams["corrected_text"].(string); corrected != "" {
		return corrected
	}
	return plan.FallbackText
}

func silentDecision(id, reason string) policydomain.AutonomyDecision {
	return policydomain.AutonomyDecision{DecisionID: id + "-decision", Action: policydomain.ActionSilent, ReasonCodes: []string{reason}, Confidence: 1}
}
