package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

var ErrMissingState = errors.New("state not found")

type StateStore struct {
	client *goredis.Client
}

func New(addr, password string, db int) *StateStore {
	return &StateStore{
		client: goredis.NewClient(&goredis.Options{
			Addr:     addr,
			Password: password,
			DB:       db,
		}),
	}
}

func (s *StateStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *StateStore) Close() error {
	return s.client.Close()
}

func (s *StateStore) GetRuntimeState(ctx context.Context, groupID int64) (policydomain.RuntimeState, error) {
	var state policydomain.RuntimeState
	data, err := s.client.Get(ctx, runtimeKey(groupID)).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return policydomain.RuntimeState{GroupID: groupID, State: policydomain.StateObserving}, nil
		}
		return policydomain.RuntimeState{}, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return policydomain.RuntimeState{}, err
	}
	return state, nil
}

func (s *StateStore) SaveRuntimeState(ctx context.Context, state policydomain.RuntimeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, runtimeKey(state.GroupID), data, 24*time.Hour).Err()
}

func (s *StateStore) GetPersonaState(ctx context.Context, personaID string, groupID int64) (personadomain.PersonaState, error) {
	var state personadomain.PersonaState
	data, err := s.client.Get(ctx, personaKey(personaID, groupID)).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			now := time.Now()
			return personadomain.PersonaState{
				PersonaID: personaID,
				GroupID:   groupID,
				Mood:      "steady",
				Energy:    "normal",
				UpdatedAt: now,
				ExpiresAt: now.Add(2 * time.Hour),
			}, nil
		}
		return personadomain.PersonaState{}, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return personadomain.PersonaState{}, err
	}
	return state, nil
}

func (s *StateStore) SavePersonaState(ctx context.Context, state personadomain.PersonaState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	ttl := time.Until(state.ExpiresAt)
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return s.client.Set(ctx, personaKey(state.PersonaID, state.GroupID), data, ttl).Err()
}

func runtimeKey(groupID int64) string {
	return "runtime_state:" + strconv.FormatInt(groupID, 10)
}

func personaKey(personaID string, groupID int64) string {
	return "persona_state:" + personaID + ":" + strconv.FormatInt(groupID, 10)
}
