package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

var _ ports.RuntimeStateStore = (*StateStore)(nil)

// StateStore 实现 ports.RuntimeStateStore,原 Redis JSON KV 的 PG 版。
// TTL 语义由 expires_at 列承载:读取时 WHERE expires_at > NOW(),过期即视为不存在。
// 过期行不主动删除,靠同 key 覆盖写自然回收;数据量为每群两行,无需清理任务。
type StateStore struct {
	db *sql.DB
}

func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{db: db}
}

func (s *StateStore) GetRuntimeState(ctx context.Context, groupID int64) (policydomain.RuntimeState, error) {
	var state policydomain.RuntimeState
	data, err := s.get(ctx, runtimeKey(groupID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	return s.set(ctx, runtimeKey(state.GroupID), data, 24*time.Hour)
}

func (s *StateStore) GetPersonaState(ctx context.Context, personaID string, groupID int64) (personadomain.PersonaState, error) {
	var state personadomain.PersonaState
	data, err := s.get(ctx, personaKey(personaID, groupID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			now := time.Now()
			return personadomain.PersonaState{
				PersonaID: personaID,
				GroupID:   groupID,
				Mood:      "steady",
				Energy:    "normal",
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
	// ttl 直接取 time.Until(ExpiresAt):过期状态写负 ttl → expires_at 在过去 → 读时不可见。
	// redis 版的 2h 兜底是为避开 go-redis 非正 TTL=永不过期的坑,PG 无此问题,不能复活已过期状态。
	return s.set(ctx, personaKey(state.PersonaID, state.GroupID), data, time.Until(state.ExpiresAt))
}

// get 读取未过期的状态;过期或缺失都返回 sql.ErrNoRows。
func (s *StateStore) get(ctx context.Context, key string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT state_json FROM runtime_states WHERE key = $1 AND expires_at > NOW()`, key,
	).Scan(&data)
	return data, err
}

// set 以 key 为主键覆盖写,expires_at = NOW() + ttl 秒。
// 注意:传秒数给 make_interval,不能传 Go duration 字符串(PG interval 解析不了 "24h0m0s")。
func (s *StateStore) set(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_states (key, state_json, expires_at)
		VALUES ($1, $2, NOW() + make_interval(secs => $3))
		ON CONFLICT (key) DO UPDATE SET state_json = EXCLUDED.state_json, expires_at = EXCLUDED.expires_at
	`, key, data, ttl.Seconds())
	return err
}

func runtimeKey(groupID int64) string {
	return "runtime_state:" + strconv.FormatInt(groupID, 10)
}

func personaKey(personaID string, groupID int64) string {
	return "persona_state:" + personaID + ":" + strconv.FormatInt(groupID, 10)
}
