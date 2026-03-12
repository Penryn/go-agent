package redisstore

import (
	"context"
	"testing"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

func TestStateStoreIntegration(t *testing.T) {
	ctx := context.Background()
	store := New("127.0.0.1:6379", "", 0)
	defer store.Close()

	if err := store.Ping(ctx); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}

	runtimeState := policydomain.RuntimeState{
		GroupID:          1,
		State:            policydomain.StateCooldown,
		RepliesLast10Min: 2,
	}
	if err := store.SaveRuntimeState(ctx, runtimeState); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}

	gotRuntime, err := store.GetRuntimeState(ctx, 1)
	if err != nil {
		t.Fatalf("get runtime state: %v", err)
	}
	if gotRuntime.State != policydomain.StateCooldown {
		t.Fatalf("unexpected runtime state: %s", gotRuntime.State)
	}

	personaState := personadomain.PersonaState{
		PersonaID: "main",
		GroupID:   1,
		Mood:      "steady",
		Energy:    "normal",
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := store.SavePersonaState(ctx, personaState); err != nil {
		t.Fatalf("save persona state: %v", err)
	}

	gotPersona, err := store.GetPersonaState(ctx, "main", 1)
	if err != nil {
		t.Fatalf("get persona state: %v", err)
	}
	if gotPersona.PersonaID != "main" {
		t.Fatalf("unexpected persona id: %s", gotPersona.PersonaID)
	}
}
