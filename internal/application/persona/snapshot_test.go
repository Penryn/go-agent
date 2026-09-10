package persona

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

// TestGetSnapshot_Fresh 测试获取新鲜状态
func TestGetSnapshot_Fresh(t *testing.T) {
	// 准备数据
	posture := personadomain.DefaultGroupPosture("bot1", 12345)
	ephemeral := personadomain.DefaultEphemeralState("bot1", 12345)
	ephemeral.ExpiresAt = time.Now().Add(12 * time.Hour) // 未过期

	postureStore := &mockPostureStore{posture: &posture}
	ephemeralStore := &mockEphemeralStore{state: &ephemeral}
	service := NewStateSnapshotService(postureStore, ephemeralStore)

	// 获取快照
	snapshot, err := service.GetSnapshot(context.Background(), "bot1", 12345)
	require.NoError(t, err)
	assert.NotNil(t, snapshot)

	// 验证快照内容
	assert.Equal(t, "bot1", snapshot.PersonaID)
	assert.Equal(t, int64(12345), snapshot.GroupID)
	assert.Equal(t, posture.Familiarity, snapshot.Posture.Familiarity)
	assert.Equal(t, ephemeral.Mood, snapshot.EphemeralState.Mood)
	assert.WithinDuration(t, time.Now(), snapshot.SnapshotAt, 1*time.Second)
}

// TestGetSnapshot_ExpiredState 测试过期状态自动重置
func TestGetSnapshot_ExpiredState(t *testing.T) {
	// 准备过期状态
	posture := personadomain.DefaultGroupPosture("bot1", 12345)
	ephemeral := personadomain.DefaultEphemeralState("bot1", 12345)
	ephemeral.Mood = personadomain.MoodWithdrawn // 设置为低落
	ephemeral.Energy = personadomain.EnergyTired
	ephemeral.ExpiresAt = time.Now().Add(-1 * time.Hour) // 已过期

	postureStore := &mockPostureStore{posture: &posture}
	ephemeralStore := &mockEphemeralStore{state: &ephemeral}
	service := NewStateSnapshotService(postureStore, ephemeralStore)

	// 获取快照
	snapshot, err := service.GetSnapshot(context.Background(), "bot1", 12345)
	require.NoError(t, err)
	assert.NotNil(t, snapshot)

	// 验证状态已重置为默认值
	assert.Equal(t, personadomain.MoodSteady, snapshot.EphemeralState.Mood, "过期状态应重置")
	assert.Equal(t, personadomain.EnergyNormal, snapshot.EphemeralState.Energy, "过期精力应重置")
	assert.True(t, snapshot.EphemeralState.ExpiresAt.After(time.Now()), "新过期时间应在未来")

	// 等待异步写回
	time.Sleep(100 * time.Millisecond)

	// 验证数据库中的状态也被更新
	assert.True(t, ephemeralStore.updated, "状态应被异步更新")
	assert.Equal(t, personadomain.MoodSteady, ephemeralStore.state.Mood, "数据库状态应被更新")
}

// TestGetSnapshot_MissingState 测试状态不存在时创建默认值
func TestGetSnapshot_MissingState(t *testing.T) {
	postureStore := &mockPostureStore{}      // 不设置 posture，模拟不存在
	ephemeralStore := &mockEphemeralStore{} // 不设置 state，模拟不存在
	service := NewStateSnapshotService(postureStore, ephemeralStore)

	// 获取快照
	snapshot, err := service.GetSnapshot(context.Background(), "bot1", 12345)
	require.NoError(t, err)
	assert.NotNil(t, snapshot)

	// 验证使用了默认值
	assert.Equal(t, "bot1", snapshot.PersonaID)
	assert.Equal(t, int64(12345), snapshot.GroupID)
	assert.Equal(t, 0.3, snapshot.Posture.Familiarity, "应使用默认熟悉度")
	assert.Equal(t, personadomain.MoodSteady, snapshot.EphemeralState.Mood, "应使用默认心情")
	assert.Equal(t, personadomain.EnergyNormal, snapshot.EphemeralState.Energy, "应使用默认精力")
}

// TestSnapshotIsExpired 测试快照过期判断
func TestSnapshotIsExpired(t *testing.T) {
	snapshot := &PersonaStateSnapshot{
		SnapshotAt: time.Now().Add(-10 * time.Minute),
	}

	assert.True(t, snapshot.IsExpired(5*time.Minute), "10分钟前的快照应被判断为过期（阈值5分钟）")
	assert.False(t, snapshot.IsExpired(15*time.Minute), "10分钟前的快照不应被判断为过期（阈值15分钟）")
}

// TestSnapshotShouldRefresh 测试快照刷新建议
func TestSnapshotShouldRefresh(t *testing.T) {
	tests := []struct {
		name           string
		snapshotAge    time.Duration
		stateExpiresIn time.Duration
		shouldRefresh  bool
	}{
		{
			name:           "新鲜快照，状态未到期",
			snapshotAge:    1 * time.Minute,
			stateExpiresIn: 12 * time.Hour,
			shouldRefresh:  false,
		},
		{
			name:           "快照太旧",
			snapshotAge:    10 * time.Minute,
			stateExpiresIn: 12 * time.Hour,
			shouldRefresh:  true,
		},
		{
			name:           "状态即将过期",
			snapshotAge:    1 * time.Minute,
			stateExpiresIn: 30 * time.Minute,
			shouldRefresh:  true,
		},
		{
			name:           "两者都不好",
			snapshotAge:    10 * time.Minute,
			stateExpiresIn: 30 * time.Minute,
			shouldRefresh:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := &PersonaStateSnapshot{
				SnapshotAt: time.Now().Add(-tt.snapshotAge),
				EphemeralState: personadomain.EphemeralState{
					ExpiresAt: time.Now().Add(tt.stateExpiresIn),
				},
			}

			result := snapshot.ShouldRefresh()
			assert.Equal(t, tt.shouldRefresh, result)
		})
	}
}

// TestGetSnapshot_Consistency 测试状态一致性
func TestGetSnapshot_Consistency(t *testing.T) {
	// 准备数据
	posture := personadomain.DefaultGroupPosture("bot1", 12345)
	posture.Familiarity = 0.8
	ephemeral := personadomain.DefaultEphemeralState("bot1", 12345)
	ephemeral.Mood = personadomain.MoodHappy

	postureStore := &mockPostureStore{posture: &posture}
	ephemeralStore := &mockEphemeralStore{state: &ephemeral}
	service := NewStateSnapshotService(postureStore, ephemeralStore)

	// 获取两次快照
	snapshot1, _ := service.GetSnapshot(context.Background(), "bot1", 12345)
	snapshot2, _ := service.GetSnapshot(context.Background(), "bot1", 12345)

	// 验证两次快照内容一致（数据未变化）
	assert.Equal(t, snapshot1.Posture.Familiarity, snapshot2.Posture.Familiarity)
	assert.Equal(t, snapshot1.EphemeralState.Mood, snapshot2.EphemeralState.Mood)
}
