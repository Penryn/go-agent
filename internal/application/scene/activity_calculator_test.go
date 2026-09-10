package scene

import (
	"math"
	"testing"
	"time"
)

func TestActivityCalculator_BasicDecay(t *testing.T) {
	calc := NewActivityCalculator()
	now := time.Now()

	// 测试不同时间的消息权重
	tests := []struct {
		daysAgo  float64
		minWeight float64
		maxWeight float64
	}{
		{0, 0.95, 1.0},   // 今天
		{1, 0.85, 0.95},  // 昨天
		{7, 0.45, 0.55},  // 7天前（半衰期）
		{14, 0.20, 0.30}, // 14天前
		{30, 0.05, 0.15}, // 30天前
	}

	for _, tt := range tests {
		timestamp := now.Add(-time.Duration(tt.daysAgo*24) * time.Hour)
		weight := calc.CalculateWeight(tt.daysAgo)

		if weight < tt.minWeight || weight > tt.maxWeight {
			t.Errorf("daysAgo=%.0f, weight=%.3f, 期望范围 [%.2f, %.2f]",
				tt.daysAgo, weight, tt.minWeight, tt.maxWeight)
		}

		// 验证实际计算
		events := []MessageEvent{{Timestamp: timestamp}}
		activity := calc.CalculateActivity(events, now)

		if activity <= 0 || activity > 1 {
			t.Errorf("activity 应该在 (0, 1] 范围内，实际: %.3f", activity)
		}
	}
}

func TestActivityCalculator_MultipleMessages(t *testing.T) {
	calc := NewActivityCalculator()
	now := time.Now()

	// 10 条今天的消息
	var todayEvents []MessageEvent
	for i := 0; i < 10; i++ {
		todayEvents = append(todayEvents, MessageEvent{
			Timestamp: now.Add(-time.Duration(i) * time.Hour),
		})
	}

	todayActivity := calc.CalculateActivity(todayEvents, now)

	// 10 条一周前的消息
	var weekAgoEvents []MessageEvent
	for i := 0; i < 10; i++ {
		weekAgoEvents = append(weekAgoEvents, MessageEvent{
			Timestamp: now.Add(-7*24*time.Hour - time.Duration(i)*time.Hour),
		})
	}

	weekAgoActivity := calc.CalculateActivity(weekAgoEvents, now)

	// 今天的消息应该比一周前的消息活跃度高
	if todayActivity <= weekAgoActivity {
		t.Errorf("今天的活跃度 (%.3f) 应该 > 一周前 (%.3f)",
			todayActivity, weekAgoActivity)
	}

	// 一周前的活跃度应该约为今天的一半（半衰期）
	ratio := weekAgoActivity / todayActivity
	if ratio < 0.3 || ratio > 0.7 {
		t.Errorf("一周前/今天的比例 %.3f 应该接近 0.5", ratio)
	}
}

func TestActivityCalculator_ZeroEvents(t *testing.T) {
	calc := NewActivityCalculator()
	now := time.Now()

	activity := calc.CalculateActivity([]MessageEvent{}, now)

	if activity != 0.0 {
		t.Errorf("无消息时活跃度应为 0，实际: %.3f", activity)
	}
}

func TestActivityCalculator_Normalization(t *testing.T) {
	calc := NewActivityCalculator()
	now := time.Now()

	// 大量今天的消息（超过归一化阈值）
	var manyEvents []MessageEvent
	for i := 0; i < 200; i++ {
		manyEvents = append(manyEvents, MessageEvent{
			Timestamp: now.Add(-time.Duration(i%24) * time.Hour),
		})
	}

	activity := calc.CalculateActivity(manyEvents, now)

	// 应该被归一化到 1.0
	if activity != 1.0 {
		t.Errorf("大量消息应归一化到 1.0，实际: %.3f", activity)
	}
}

func TestActivityCalculator_UserFilter(t *testing.T) {
	calc := NewActivityCalculator()
	now := time.Now()

	events := []MessageEvent{
		{Timestamp: now, UserID: 100},
		{Timestamp: now, UserID: 100},
		{Timestamp: now, UserID: 200},
		{Timestamp: now, UserID: 200},
		{Timestamp: now, UserID: 200},
	}

	user100Activity := calc.CalculateUserActivity(events, 100, now)
	user200Activity := calc.CalculateUserActivity(events, 200, now)

	// 用户 200 有 3 条消息，用户 100 有 2 条
	if user200Activity <= user100Activity {
		t.Errorf("用户200活跃度 (%.3f) 应 > 用户100 (%.3f)",
			user200Activity, user100Activity)
	}
}

func TestActivityCalculator_CustomHalfLife(t *testing.T) {
	// 3天半衰期
	calc := NewActivityCalculatorWithHalfLife(3)

	if calc.GetHalfLife() != 3 {
		t.Errorf("半衰期应为 3，实际: %d", calc.GetHalfLife())
	}

	// 3天前的权重应该接近 0.5
	weight := calc.CalculateWeight(3.0)
	if math.Abs(weight-0.5) > 0.01 {
		t.Errorf("3天前的权重应接近 0.5，实际: %.3f", weight)
	}
}

func TestActivityCalculator_HalfLifeProperty(t *testing.T) {
	calc := NewActivityCalculator()
	halfLife := float64(calc.GetHalfLife())

	// 验证半衰期性质：decay^halfLife ≈ 0.5
	weight := calc.CalculateWeight(halfLife)

	if math.Abs(weight-0.5) > 0.01 {
		t.Errorf("半衰期=%d天，权重应接近 0.5，实际: %.3f", calc.GetHalfLife(), weight)
	}
}
