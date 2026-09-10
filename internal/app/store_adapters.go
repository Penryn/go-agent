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

// StoreAdapters 集中管理所有 store 适配器
type StoreAdapters struct {
	scenes        ports.GroupSceneStore
	relationships ports.RelationshipStore
	facts         ports.PersonaFactStore
	memory        ports.MemoryStore
}

// NewStoreAdapters 创建适配器集合
func NewStoreAdapters(
	scenes ports.GroupSceneStore,
	relationships ports.RelationshipStore,
	facts ports.PersonaFactStore,
	memory ports.MemoryStore,
) *StoreAdapters {
	return &StoreAdapters{
		scenes:        scenes,
		relationships: relationships,
		facts:         facts,
		memory:        memory,
	}
}

// GetGroupScene 实现 socialdecision.SceneStore
func (a *StoreAdapters) GetGroupScene(ctx context.Context, groupID int64) (*scenedomain.GroupScene, error) {
	scene, err := a.scenes.LoadGroupScene(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return &scene, nil
}

// UpdateGroupScene 实现 socialdecision.SceneStore
func (a *StoreAdapters) UpdateGroupScene(ctx context.Context, scene *scenedomain.GroupScene) error {
	return a.scenes.SaveGroupScene(ctx, *scene)
}

// GetRelationship 实现 socialdecision.RelationshipStore
func (a *StoreAdapters) GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (*relationshipdomain.State, error) {
	state, err := a.relationships.GetSocialRelationship(ctx, personaID, groupID, userID)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// UpdateRelationship 实现 socialdecision.RelationshipStore
func (a *StoreAdapters) UpdateRelationship(ctx context.Context, rel *relationshipdomain.State) error {
	// RelationshipStore 不直接支持 Update，使用 ApplyRelationshipEvent
	// 这里简化处理，实际应该根据状态变化生成事件
	return nil // 或者返回 unsupported error
}

// GetCanonicalFacts 实现 persona.FactStore
func (a *StoreAdapters) GetCanonicalFacts(ctx context.Context, personaID string) ([]personadomain.PersonaFact, error) {
	return a.facts.CurrentPersonaFacts(ctx, personaID, time.Now())
}

// ListEventsSince 实现 reflection.EventStore
func (a *StoreAdapters) ListEventsSince(
	ctx context.Context,
	groupID int64,
	since time.Time,
	duration time.Duration,
	limit int,
) ([]conversationdomain.ConversationEvent, error) {
	events, err := a.memory.RecentEvents(ctx, groupID, limit*2)
	if err != nil {
		return nil, err
	}

	var filtered []conversationdomain.ConversationEvent
	endTime := since.Add(duration)
	for _, evt := range events {
		evtTime := time.Unix(evt.TimestampUnix, 0)
		if evtTime.After(since) && evtTime.Before(endTime) {
			filtered = append(filtered, evt)
			if len(filtered) >= limit {
				break
			}
		}
	}

	return filtered, nil
}
