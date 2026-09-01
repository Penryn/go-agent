package persona

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type canonFactStore struct{ facts []personadomain.PersonaFact }

func (s *canonFactStore) AppendPersonaFact(_ context.Context, fact personadomain.PersonaFact) error {
	s.facts = append(s.facts, fact)
	return nil
}

func (s *canonFactStore) CurrentPersonaFacts(_ context.Context, personaID string, now time.Time) ([]personadomain.PersonaFact, error) {
	result := make([]personadomain.PersonaFact, 0, len(s.facts))
	for _, fact := range s.facts {
		if fact.PersonaID == personaID && !fact.EffectiveAt.After(now) && (fact.ExpiresAt.IsZero() || fact.ExpiresAt.After(now)) {
			result = append(result, fact)
		}
	}
	return result, nil
}

func canonDefinition(t *testing.T) personadomain.PersonaDefinition {
	t.Helper()
	definition, err := personadomain.Compile(personadomain.PersonaConfig{
		ID: "main", Name: "Test", Facts: []personadomain.PersonaFactDefinition{
			{Key: "identity.display_name", Value: "Test", Policy: personadomain.FactPolicyLocked},
			{Key: "education.high_school.track", Policy: personadomain.FactPolicySelfMutable},
			{Key: "preference.*", Policy: personadomain.FactPolicySelfMutable},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestCanonPrepareDeliverAndExplicitCorrection(t *testing.T) {
	store := &canonFactStore{}
	service := NewCanonService(store, canonDefinition(t), nil)
	ctx := context.Background()
	view, _ := service.View(ctx, time.Now())
	firstCandidate := []replydomain.PersonaFactCandidate{{
		Key: "education.high_school.track", Value: "文科", EvidenceText: "我高中读的是文科",
	}}
	proposal, err := service.PreparePlan(ctx, view, firstCandidate, "我高中读的是文科。", "proposal-1")
	if err != nil {
		t.Fatalf("prepare first canon: %v", err)
	}
	if err := service.AfterDelivery(ctx, proposal, CanonDelivery{GroupID: 1, SelfID: 9, SourceEventID: "outbound-1", Text: "我高中读的是文科。"}); err != nil {
		t.Fatalf("deliver first canon: %v", err)
	}
	if len(store.facts) != 1 || store.facts[0].DefinitionHash == "" {
		t.Fatalf("first canon not stored with definition lineage: %+v", store.facts)
	}

	view, _ = service.View(ctx, time.Now().Add(time.Second))
	conflict := []replydomain.PersonaFactCandidate{{
		Key: "education.high_school.track", Value: "理科", EvidenceText: "我高中读的是理科",
	}}
	var conflictErr *CanonConflictError
	if _, err := service.PreparePlan(ctx, view, conflict, "我高中读的是理科。", "proposal-2"); !errors.As(err, &conflictErr) {
		t.Fatalf("expected canon conflict, got %v", err)
	}

	conflict[0].Correction = true
	text := "我之前说错了，更正一下，我高中读的是理科。"
	proposal, err = service.PreparePlan(ctx, view, conflict, text, "proposal-3")
	if err != nil {
		t.Fatalf("prepare correction: %v", err)
	}
	if err := service.AfterDelivery(ctx, proposal, CanonDelivery{GroupID: 1, SelfID: 9, SourceEventID: "outbound-3", Text: text}); err != nil {
		t.Fatalf("deliver correction: %v", err)
	}
	if len(store.facts) != 2 || store.facts[1].SupersedesFactID != store.facts[0].FactID {
		t.Fatalf("correction did not supersede old canon: %+v", store.facts)
	}
}

func TestCanonNeverOverridesOperatorVerifiedFact(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	store := &canonFactStore{facts: []personadomain.PersonaFact{{
		FactID: "verified-1", PersonaID: "main", Key: "education.high_school.track", Value: "文科",
		Status: personadomain.PersonaFactVerified, SourceKind: "owner_statement", EffectiveAt: now, RecordedAt: now,
	}}}
	service := NewCanonService(store, canonDefinition(t), nil)
	view, _ := service.View(context.Background(), time.Now())
	_, err := service.PreparePlan(context.Background(), view, []replydomain.PersonaFactCandidate{{
		Key: "education.high_school.track", Value: "理科", EvidenceText: "我高中读的是理科", Correction: true,
	}}, "更正一下，我高中读的是理科。", "proposal-1")
	var conflictErr *CanonConflictError
	if !errors.As(err, &conflictErr) || len(store.facts) != 1 {
		t.Fatalf("verified fact should be immutable: err=%v facts=%+v", err, store.facts)
	}
}

func TestCanonExtractionUsesDefinitionPolicy(t *testing.T) {
	store := &canonFactStore{}
	mock := modeladapter.NewMockChatModel(schema.AssistantMessage(`{
		"facts":[{"key":"preference.favorite_drink","value":"茉莉花茶","evidence_text":"我最喜欢喝茉莉花茶","correction":false}]
	}`, nil))
	service := NewCanonService(store, canonDefinition(t), modeladapter.StaticFactory{MainModel: mock})
	err := service.ProcessExtraction(context.Background(), CanonExtractionTask{
		GroupID: 1, SelfID: 9, SourceEventID: "outbound-1", Text: "我最喜欢喝茉莉花茶。",
	})
	if err != nil {
		t.Fatalf("extract delivered canon: %v", err)
	}
	if len(store.facts) != 1 || store.facts[0].Key != "preference.favorite_drink" {
		t.Fatalf("extracted canon not stored: %+v", store.facts)
	}
}
