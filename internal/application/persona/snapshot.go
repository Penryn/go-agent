package persona

import (
	"context"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

// PersonaStateSnapshot 人格状态快照
// 一次读取，多处使用，确保决策和生成使用一致的状态
type PersonaStateSnapshot struct {
	PersonaID string
	GroupID   int64

	// 群姿态
	Posture personadomain.GroupPosture

	// 即时状态
	EphemeralState personadomain.EphemeralState

	// 快照时间
	SnapshotAt time.Time
}

// StateSnapshotService 状态快照服务
// 职责：
// 1. 一次读取姿态和即时状态
// 2. 自动重置过期的即时状态（关键！）
// 3. 提供一致的状态快照给决策引擎和上下文组装器
type StateSnapshotService struct {
	postureStore   PostureStore
	ephemeralStore EphemeralStateStore
}

// NewStateSnapshotService 创建状态快照服务
func NewStateSnapshotService(
	postureStore PostureStore,
	ephemeralStore EphemeralStateStore,
) *StateSnapshotService {
	return &StateSnapshotService{
		postureStore:   postureStore,
		ephemeralStore: ephemeralStore,
	}
}

// GetSnapshot 获取状态快照
// 保证：
// 1. 过期状态立即重置为默认值
// 2. 不存在的状态创建默认值
// 3. 只读取一次数据库
func (s *StateSnapshotService) GetSnapshot(
	ctx context.Context,
	personaID string,
	groupID int64,
) (*PersonaStateSnapshot, error) {
	// 1. 读取群姿态
	posture, err := s.postureStore.GetGroupPosture(ctx, personaID, groupID)
	if err != nil {
		return nil, err
	}
	if posture == nil {
		// 不存在则创建默认值
		defaultPosture := personadomain.DefaultGroupPosture(personaID, groupID)
		posture = &defaultPosture
		// 异步写回，不阻塞
		go func() {
			_ = s.postureStore.UpdateGroupPosture(context.Background(), posture)
		}()
	}

	// 2. 读取即时状态
	ephemeral, err := s.ephemeralStore.GetEphemeralState(ctx, personaID, groupID)
	if err != nil {
		return nil, err
	}

	// 3. 检查是否过期或不存在
	now := time.Now()
	if ephemeral == nil || ephemeral.ExpiresAt.Before(now) {
		// 过期或不存在，重置为默认值（关键！）
		defaultEphemeral := personadomain.DefaultEphemeralState(personaID, groupID)
		ephemeral = &defaultEphemeral
		// 异步写回，不阻塞
		go func() {
			_ = s.ephemeralStore.UpdateEphemeralState(context.Background(), ephemeral)
		}()
	}

	// 4. 构建快照
	snapshot := &PersonaStateSnapshot{
		PersonaID:      personaID,
		GroupID:        groupID,
		Posture:        *posture,
		EphemeralState: *ephemeral,
		SnapshotAt:     now,
	}

	return snapshot, nil
}

// IsExpired 检查快照是否过期（用于长时间保持的快照）
func (s *PersonaStateSnapshot) IsExpired(maxAge time.Duration) bool {
	return time.Since(s.SnapshotAt) > maxAge
}

// ShouldRefresh 建议是否刷新快照
// 规则：
// 1. 快照超过 5 分钟 → 刷新
// 2. 即时状态即将过期（1小时内）→ 刷新
func (s *PersonaStateSnapshot) ShouldRefresh() bool {
	// 快照本身太旧
	if time.Since(s.SnapshotAt) > 5*time.Minute {
		return true
	}

	// 即时状态即将过期
	if time.Until(s.EphemeralState.ExpiresAt) < 1*time.Hour {
		return true
	}

	return false
}
