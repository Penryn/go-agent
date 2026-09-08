package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	relationshipsvc "github.com/phlin/go-agent/internal/application/relationship"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	"github.com/phlin/go-agent/internal/domain/reply"
)

type stageMemoryClaimTool struct {
	service *memsvc.ClaimService
	session reply.ToolContext
}

type stageMemoryClaimArgs struct {
	Type             string   `json:"type"`
	Subject          string   `json:"subject"`
	Content          string   `json:"content"`
	EvidenceEventIDs []string `json:"evidence_event_ids"`
	Confidence       float64  `json:"confidence"`
	SuggestedTTL     string   `json:"suggested_ttl"`
}

func newStageMemoryClaimTool(service *memsvc.ClaimService, session reply.ToolContext) namedTool {
	return &stageMemoryClaimTool{service: service, session: session}
}

func (t *stageMemoryClaimTool) Name() string { return "stage_memory_claim" }

func (t *stageMemoryClaimTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Stage an evidence-backed memory observation for review. This does not create a durable memory.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"type":               {Type: schema.String, Required: true, Desc: "episodic, semantic, social, or persona."},
			"subject":            {Type: schema.String, Required: true, Desc: "Short subject."},
			"content":            {Type: schema.String, Required: true, Desc: "Observed fact, without speculation."},
			"evidence_event_ids": {Type: schema.Array, Required: true, Desc: "Supporting event IDs."},
			"confidence":         {Type: schema.Number, Desc: "Confidence from 0 to 1."},
			"suggested_ttl":      {Type: schema.String, Desc: "Optional duration such as 720h."},
		}),
	}, nil
}

func (t *stageMemoryClaimTool) InvokableRun(ctx context.Context, input string, _ ...tool.Option) (string, error) {
	var args stageMemoryClaimArgs
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", fmt.Errorf("decode stage_memory_claim args: %w", err)
	}
	if t.service == nil {
		return marshal(map[string]any{"accepted": false, "reason": "claim_store_unavailable"})
	}
	args.Type = strings.TrimSpace(args.Type)
	args.Subject = strings.TrimSpace(args.Subject)
	args.Content = strings.TrimSpace(args.Content)
	if args.Type == "" || args.Subject == "" || args.Content == "" {
		return marshal(map[string]any{"accepted": false, "reason": "type_subject_content_required"})
	}
	evidence := uniqueStrings(args.EvidenceEventIDs)
	if len(evidence) == 0 && t.session.TriggerEventID != "" {
		evidence = []string{t.session.TriggerEventID}
	}
	if len(evidence) == 0 {
		return marshal(map[string]any{"accepted": false, "reason": "evidence_required"})
	}
	if !evidenceInContext(evidence, t.session) {
		return marshal(map[string]any{"accepted": false, "reason": "evidence_out_of_context"})
	}
	confidence := clampF(args.Confidence, 0, 1)
	claim, err := t.service.Stage(ctx, memsvc.ClaimInput{
		GroupID: t.session.GroupID, Type: args.Type, Subject: args.Subject, Content: args.Content,
		EvidenceEventIDs: evidence, Confidence: confidence, SuggestedTTL: args.SuggestedTTL,
	})
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"accepted": true, "claim_id": claim.ClaimID, "status": claim.Status})
}

type relationshipSignalTool struct {
	service *relationshipsvc.Service
	session reply.ToolContext
}

type relationshipSignalArgs struct {
	UserID          int64   `json:"user_id"`
	Kind            string  `json:"kind"`
	Valence         float64 `json:"valence"`
	Intensity       float64 `json:"intensity"`
	Reason          string  `json:"reason"`
	EvidenceEventID string  `json:"evidence_event_id"`
}

func newRelationshipSignalTool(service *relationshipsvc.Service, session reply.ToolContext) namedTool {
	return &relationshipSignalTool{service: service, session: session}
}

func (t *relationshipSignalTool) Name() string { return "record_relationship_signal" }

func (t *relationshipSignalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Record an observed relationship signal. The relationship service computes the resulting state.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id":           {Type: schema.Integer, Required: true, Desc: "Target group member."},
			"kind":              {Type: schema.String, Required: true, Desc: "Signal kind such as positive_feedback, user_correction, or user_teasing."},
			"valence":           {Type: schema.Number, Desc: "Optional signed observation from -1 to 1."},
			"intensity":         {Type: schema.Number, Desc: "Signal intensity from 0 to 1."},
			"reason":            {Type: schema.String, Desc: "Short evidence-based reason."},
			"evidence_event_id": {Type: schema.String, Desc: "Supporting event ID."},
		}),
	}, nil
}

func (t *relationshipSignalTool) InvokableRun(ctx context.Context, input string, _ ...tool.Option) (string, error) {
	var args relationshipSignalArgs
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", fmt.Errorf("decode record_relationship_signal args: %w", err)
	}
	if t.service == nil {
		return marshal(map[string]any{"accepted": false, "reason": "relationship_service_unavailable"})
	}
	if args.UserID == 0 || strings.TrimSpace(args.Kind) == "" {
		return marshal(map[string]any{"accepted": false, "reason": "user_id_and_kind_required"})
	}
	evidence := args.EvidenceEventID
	if evidence == "" {
		evidence = t.session.TriggerEventID
	}
	if evidence == "" {
		return marshal(map[string]any{"accepted": false, "reason": "evidence_required"})
	}
	if !evidenceInContext([]string{evidence}, t.session) {
		return marshal(map[string]any{"accepted": false, "reason": "evidence_out_of_context"})
	}
	kind := strings.TrimSpace(args.Kind)
	event := relationshipdomain.Event{
		EventID:         fmt.Sprintf("relationship:signal:%s:%s:%d", t.session.TriggerEventID, kind, args.UserID),
		GroupID:         t.session.GroupID,
		UserID:          args.UserID,
		Kind:            relationshipdomain.EventKind(kind),
		Valence:         clampF(args.Valence, -1, 1),
		Intensity:       clampF(args.Intensity, 0, 1),
		Reason:          strings.TrimSpace(args.Reason),
		EvidenceEventID: evidence,
		DecisionID:      t.session.TraceID,
		CreatedAt:       time.Now(),
	}
	if err := t.service.Apply(ctx, event); err != nil {
		return "", err
	}
	return marshal(map[string]any{"accepted": true, "event_id": event.EventID, "kind": event.Kind})
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func evidenceInContext(evidence []string, session reply.ToolContext) bool {
	allowed := make(map[string]struct{}, len(session.EvidenceEventIDs)+1)
	for _, id := range session.EvidenceEventIDs {
		allowed[id] = struct{}{}
	}
	if session.TriggerEventID != "" {
		allowed[session.TriggerEventID] = struct{}{}
	}
	if len(allowed) == 0 {
		return false
	}
	for _, id := range evidence {
		if _, ok := allowed[id]; !ok {
			return false
		}
	}
	return true
}
