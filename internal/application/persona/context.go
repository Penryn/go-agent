package persona

import (
	"context"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

// ContextAssembler 组装完整的 PersonaContext
type ContextAssembler struct {
	postureStore   PostureStore
	ephemeralStore EphemeralStateStore
	factStore      FactStore
}

// NewContextAssembler 创建 PersonaContext 组装器
func NewContextAssembler(
	postureStore PostureStore,
	ephemeralStore EphemeralStateStore,
	factStore FactStore,
) *ContextAssembler {
	return &ContextAssembler{
		postureStore:   postureStore,
		ephemeralStore: ephemeralStore,
		factStore:      factStore,
	}
}

// AssembleContext 组装当前回合的完整人格上下文
func (a *ContextAssembler) AssembleContext(
	ctx context.Context,
	identity personadomain.PersonaConfig,
	groupID int64,
) (*personadomain.PersonaContext, error) {
	personaID := identity.ID

	// 获取群姿态，不存在则创建默认
	posture, err := a.postureStore.GetGroupPosture(ctx, personaID, groupID)
	if err != nil {
		// 创建默认姿态
		posture = &personadomain.GroupPosture{}
		*posture = personadomain.DefaultGroupPosture(personaID, groupID)
		if err := a.postureStore.UpdateGroupPosture(ctx, posture); err != nil {
			return nil, err
		}
	}

	// 获取即时状态，不存在或过期则创建基线
	ephemeral, err := a.ephemeralStore.GetEphemeralState(ctx, personaID, groupID)
	if err != nil || ephemeral.ExpiresAt.Before(time.Now()) {
		ephemeral = &personadomain.EphemeralState{}
		*ephemeral = personadomain.DefaultEphemeralState(personaID, groupID)
		if err := a.ephemeralStore.UpdateEphemeralState(ctx, ephemeral); err != nil {
			return nil, err
		}
	}

	// 获取当前生效的 canonical facts
	facts, err := a.factStore.GetCanonicalFacts(ctx, personaID)
	if err != nil {
		return nil, err
	}

	canonicalFacts := make(map[string]string)
	for _, fact := range facts {
		canonicalFacts[fact.Key] = fact.Value
	}

	return &personadomain.PersonaContext{
		Identity:       identity,
		Posture:        *posture,
		EphemeralState: *ephemeral,
		CanonicalFacts: canonicalFacts,
	}, nil
}

// UpdatePosture 更新群姿态
func (a *ContextAssembler) UpdatePosture(
	ctx context.Context,
	posture *personadomain.GroupPosture,
) error {
	posture.UpdatedAt = time.Now()
	posture.Revision++
	return a.postureStore.UpdateGroupPosture(ctx, posture)
}

// UpdateEphemeralState 更新即时状态
func (a *ContextAssembler) UpdateEphemeralState(
	ctx context.Context,
	state *personadomain.EphemeralState,
) error {
	state.UpdatedAt = time.Now()
	return a.ephemeralStore.UpdateEphemeralState(ctx, state)
}

// PostureStore 群姿态存储接口
type PostureStore interface {
	GetGroupPosture(ctx context.Context, personaID string, groupID int64) (*personadomain.GroupPosture, error)
	UpdateGroupPosture(ctx context.Context, posture *personadomain.GroupPosture) error
}

// EphemeralStateStore 即时状态存储接口
type EphemeralStateStore interface {
	GetEphemeralState(ctx context.Context, personaID string, groupID int64) (*personadomain.EphemeralState, error)
	UpdateEphemeralState(ctx context.Context, state *personadomain.EphemeralState) error
}

// FactStore persona 事实存储接口
type FactStore interface {
	GetCanonicalFacts(ctx context.Context, personaID string) ([]personadomain.PersonaFact, error)
}
