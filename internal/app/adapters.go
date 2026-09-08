package app

import (
	"context"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

// sceneStoreAdapter 适配 GroupSceneStore 到 socialdecision.SceneStore
type sceneStoreAdapter struct {
	store ports.GroupSceneStore
}

func (a *sceneStoreAdapter) GetGroupScene(ctx context.Context, groupID int64) (*scenedomain.GroupScene, error) {
	scene, err := a.store.LoadGroupScene(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return &scene, nil
}

func (a *sceneStoreAdapter) UpdateGroupScene(ctx context.Context, scene *scenedomain.GroupScene) error {
	return a.store.SaveGroupScene(ctx, *scene)
}

// relationshipStoreAdapter 适配 RelationshipStore 到 socialdecision.RelationshipStore
type relationshipStoreAdapter struct {
	store ports.RelationshipStore
}

func (a *relationshipStoreAdapter) GetRelationship(
	ctx context.Context,
	personaID string,
	groupID, userID int64,
) (*relationshipdomain.State, error) {
	state, err := a.store.GetSocialRelationship(ctx, personaID, groupID, userID)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// factStoreAdapter 适配 PersonaFactStore 到 persona.FactStore
type factStoreAdapter struct {
	store ports.PersonaFactStore
}

func (a *factStoreAdapter) GetCanonicalFacts(
	ctx context.Context,
	personaID string,
) ([]personadomain.PersonaFact, error) {
	// PersonaFactStore 已经有这个方法签名，但是需要时间参数
	// 我们使用当前时间
	return a.store.CurrentPersonaFacts(ctx, personaID, time.Now())
}

// eventStoreAdapter 适配 MemoryStore 到 reflection.EventStore
type eventStoreAdapter struct {
	store ports.MemoryStore
}

func (a *eventStoreAdapter) ListEventsSince(
	ctx context.Context,
	groupID int64,
	since time.Time,
	duration time.Duration,
	limit int,
) ([]conversationdomain.ConversationEvent, error) {
	// MemoryStore.RecentEvents 只支持按数量查询，不支持时间范围
	// 这里我们使用 RecentEvents 并在应用层过滤
	events, err := a.store.RecentEvents(ctx, groupID, limit*2) // 获取更多事件以便过滤
	if err != nil {
		return nil, err
	}

	// 过滤出时间范围内的事件
	var filtered []conversationdomain.ConversationEvent
	cutoff := since.Add(-duration)
	for _, evt := range events {
		evtTime := time.Unix(evt.TimestampUnix, 0)
		if evtTime.After(cutoff) && evtTime.Before(since) {
			filtered = append(filtered, evt)
			if len(filtered) >= limit {
				break
			}
		}
	}

	return filtered, nil
}

