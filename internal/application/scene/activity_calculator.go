package scene

import (
	"math"
	"time"
)

// ActivityCalculator 活跃度计算器（带时间衰减）
type ActivityCalculator struct {
	decayFactor  float64 // 每天的衰减系数
	decayHalfLife int    // 半衰期（天）
}

// NewActivityCalculator 创建活跃度计算器
// 默认使用 7 天半衰期
func NewActivityCalculator() *ActivityCalculator {
	halfLife := 7.0 // 7 天半衰期
	// 计算每天的衰减系数: decay^halfLife = 0.5
	decayFactor := math.Pow(0.5, 1.0/halfLife)

	return &ActivityCalculator{
		decayFactor:  decayFactor,
		decayHalfLife: 7,
	}
}

// NewActivityCalculatorWithHalfLife 创建指定半衰期的计算器
func NewActivityCalculatorWithHalfLife(halfLifeDays int) *ActivityCalculator {
	halfLife := float64(halfLifeDays)
	decayFactor := math.Pow(0.5, 1.0/halfLife)

	return &ActivityCalculator{
		decayFactor:  decayFactor,
		decayHalfLife: halfLifeDays,
	}
}

// MessageEvent 简化的消息事件
type MessageEvent struct {
	Timestamp time.Time
	UserID    int64
}

// CalculateActivity 计算时间加权的活跃度
// 返回 0-1 之间的归一化分数
func (c *ActivityCalculator) CalculateActivity(
	events []MessageEvent,
	now time.Time,
) float64 {
	if len(events) == 0 {
		return 0.0
	}

	var weightedScore float64

	for _, event := range events {
		daysElapsed := now.Sub(event.Timestamp).Hours() / 24.0

		// 时间权重: decay^days
		weight := math.Pow(c.decayFactor, daysElapsed)
		weightedScore += weight
	}

	// 归一化到 0-1
	// 假设"非常活跃"是 30 天内 100 条有效消息
	// 权重累积约为 100 * avg_weight
	// 7天半衰期下，30天内均匀分布的100条消息的平均权重约为 0.35
	// 因此总分约为 35，我们用 50 作为满分阈值
	normalized := weightedScore / 50.0
	if normalized > 1.0 {
		normalized = 1.0
	}

	return normalized
}

// CalculateUserActivity 计算特定用户的活跃度
func (c *ActivityCalculator) CalculateUserActivity(
	events []MessageEvent,
	userID int64,
	now time.Time,
) float64 {
	// 过滤出该用户的消息
	var userEvents []MessageEvent
	for _, event := range events {
		if event.UserID == userID {
			userEvents = append(userEvents, event)
		}
	}

	return c.CalculateActivity(userEvents, now)
}

// GetDecayFactor 获取每天的衰减系数
func (c *ActivityCalculator) GetDecayFactor() float64 {
	return c.decayFactor
}

// GetHalfLife 获取半衰期（天）
func (c *ActivityCalculator) GetHalfLife() int {
	return c.decayHalfLife
}

// CalculateWeight 计算指定天数的权重
func (c *ActivityCalculator) CalculateWeight(daysElapsed float64) float64 {
	return math.Pow(c.decayFactor, daysElapsed)
}
