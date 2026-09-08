package memory

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/domain/memory"
)

type claimStore struct{ claim memory.MemoryClaim }

func (s *claimStore) UpsertMemoryClaim(_ context.Context, claim memory.MemoryClaim) error {
	s.claim = claim
	return nil
}

func (*claimStore) ListMemoryClaims(context.Context, string, int) ([]memory.MemoryClaim, error) {
	return nil, nil
}

func TestClaimServiceRequiresEvidenceAndNormalizes(t *testing.T) {
	store := &claimStore{}
	service := NewClaimService(store)
	if _, err := service.Stage(context.Background(), ClaimInput{GroupID: 1, Type: "semantic", Subject: "x", Content: "y"}); err == nil {
		t.Fatal("claim without evidence should fail")
	}
	claim, err := service.Stage(context.Background(), ClaimInput{
		GroupID: 1, Type: "semantic", Subject: " 喜欢游戏 ", Content: " 经常玩游戏 ",
		EvidenceEventIDs: []string{"event-1", "event-1"}, Confidence: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Status != memory.ClaimStaged || claim.Confidence != 1 || len(claim.EvidenceEventIDs) != 1 || store.claim.ClaimID != claim.ClaimID {
		t.Fatalf("unexpected claim: %+v", claim)
	}
}
