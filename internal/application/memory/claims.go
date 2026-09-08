package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type ClaimInput struct {
	GroupID          int64
	Type             string
	Subject          string
	Content          string
	EvidenceEventIDs []string
	Confidence       float64
	SuggestedTTL     string
	Source           string
}

type ClaimService struct{ store ports.MemoryClaimStore }

func NewClaimService(store ports.MemoryClaimStore) *ClaimService { return &ClaimService{store: store} }

func (s *ClaimService) Stage(ctx context.Context, input ClaimInput) (memorydomain.MemoryClaim, error) {
	if s == nil || s.store == nil {
		return memorydomain.MemoryClaim{}, errors.New("memory claims: store is not configured")
	}
	if input.GroupID <= 0 || strings.TrimSpace(input.Type) == "" || strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.Content) == "" {
		return memorydomain.MemoryClaim{}, errors.New("memory claims: group, type, subject and content are required")
	}
	evidence := uniqueEvidence(input.EvidenceEventIDs)
	if len(evidence) == 0 {
		return memorydomain.MemoryClaim{}, errors.New("memory claims: evidence is required")
	}
	if !allowedType(input.Type) {
		return memorydomain.MemoryClaim{}, fmt.Errorf("memory claims: unsupported type %q", input.Type)
	}
	now := time.Now()
	claim := memorydomain.MemoryClaim{
		ClaimID:          claimID(input.GroupID, input.Type, input.Subject, input.Content),
		Scope:            fmt.Sprintf("group:%d", input.GroupID),
		Type:             strings.TrimSpace(input.Type),
		Subject:          strings.TrimSpace(input.Subject),
		Content:          strings.TrimSpace(input.Content),
		EvidenceEventIDs: evidence,
		Confidence:       clamp01(input.Confidence),
		SuggestedTTL:     strings.TrimSpace(input.SuggestedTTL),
		Source:           strings.TrimSpace(input.Source),
		Status:           memorydomain.ClaimStaged,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if claim.Source == "" {
		claim.Source = "model"
	}
	if err := s.store.UpsertMemoryClaim(ctx, claim); err != nil {
		return memorydomain.MemoryClaim{}, err
	}
	return claim, nil
}

func allowedType(value string) bool {
	switch strings.TrimSpace(value) {
	case "episodic", "semantic", "social", "persona":
		return true
	default:
		return false
	}
}

func uniqueEvidence(values []string) []string {
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

func claimID(groupID int64, kind, subject, content string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", groupID, kind, subject, content)))
	return "claim-" + hex.EncodeToString(digest[:12])
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
