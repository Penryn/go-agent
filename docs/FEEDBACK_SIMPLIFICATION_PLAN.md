# 反馈机制简化方案

**日期**: 2026-09-10  
**状态**: 方案设计  
**优先级**: 中

---

## 📋 问题分析

### 当前状态

```
旧机制 (reflection/feedback.go)
  ├─ FeedbackCollector.CollectFeedback()
  ├─ 30 秒观察窗口
  ├─ 简单规则分类
  └─ 结果被丢弃（_ = feedback）
  
新机制 (presence/feedback/llm_sentiment.go)
  ├─ LLMSentimentAnalyzer.AnalyzeSentiment()
  ├─ 30 秒内所有消息
  └─ LLM 情绪分析
```

### 核心问题

1. **两套机制并存**
   - 旧机制：收集后丢弃，浪费资源
   - 新机制：单独运行，可能重复工作

2. **时间接近≠反馈**
   - 30 秒内的消息不一定是对机器人的回应
   - 可能是用户之间的对话
   - 缺少因果关系判断

3. **过度解读**
   - 所有 30 秒内消息都被分析
   - 缺少"明确引用"、"纠正"、"赞同"等强信号
   - 没有回应时应保持未知，而非假设

4. **维护成本高**
   - 两套代码需要同步维护
   - LLM 调用成本高
   - 分类规则复杂但准确性低

---

## 🎯 简化目标

### 保留一套机制

**选择**: 基于 LLM 的反馈分析（更灵活）  
**废弃**: 简单规则分类（准确性低）

### 只处理明确信号

**明确反馈**:
- 直接引用（@机器人、回复消息）
- 明确纠正（"不对"、"错了"）
- 明确赞同（"对"、"没错"、"好"）
- 明确拒绝（"别说了"、"闭嘴"）

**不是反馈**:
- 时间接近但无引用
- 与其他人对话
- 话题转移

### 保持未知

**原则**: 没有明确回应 = 未知，不等于忽略或负面

---

## 🔧 实现方案

### 阶段 1: 清理旧机制

```go
// 删除或标记为 deprecated
type FeedbackCollectorImpl struct {
    // @deprecated 使用 LLMSentimentAnalyzer 代替
}
```

### 阶段 2: 增强信号过滤

```go
type FeedbackFilter struct {
    botMessageID string
}

// FilterRelevantMessages 过滤明确相关的消息
func (f *FeedbackFilter) FilterRelevantMessages(
    messages []Message,
) []Message {
    var relevant []Message
    
    for _, msg := range messages {
        if f.isExplicitFeedback(msg) {
            relevant = append(relevant, msg)
        }
    }
    
    return relevant
}

func (f *FeedbackFilter) isExplicitFeedback(msg Message) bool {
    // 1. 直接引用机器人消息
    if msg.ReplyTo == f.botMessageID {
        return true
    }
    
    // 2. @提及机器人
    if msg.MentionsBotID != "" {
        return true
    }
    
    // 3. 明确的纠正/赞同关键词
    if containsExplicitSignal(msg.Text) {
        return true
    }
    
    return false
}

func containsExplicitSignal(text string) bool {
    // 纠正信号
    corrections := []string{"不对", "错了", "不是", "应该是"}
    
    // 赞同信号
    agreements := []string{"对", "没错", "是的", "好", "👍"}
    
    // 拒绝信号
    rejections := []string{"别说", "闭嘴", "够了", "停"}
    
    // ... 检查逻辑
}
```

### 阶段 3: 简化分类

```go
type FeedbackType string

const (
    // 明确反馈
    TypeCorrection  FeedbackType = "correction"   // 纠正
    TypeAgreement   FeedbackType = "agreement"    // 赞同
    TypeRejection   FeedbackType = "rejection"    // 拒绝
    TypeEngagement  FeedbackType = "engagement"   // 继续互动
    
    // 无明确反馈
    TypeUnknown     FeedbackType = "unknown"      // 未知（默认）
)

// SimplifiedFeedback 简化的反馈结构
type SimplifiedFeedback struct {
    Type         FeedbackType
    Confidence   float64      // 0-1
    KeyMessages  []string     // 关键消息文本
    Reasoning    string       // LLM 的判断理由
}
```

### 阶段 4: 延后通用情感评价

```go
// 当前不实现：对所有消息的通用情感分析
// 原因：
// 1. 成本高（每次都调用 LLM）
// 2. 准确性低（时间接近不等于反馈）
// 3. 业务价值不明确

// 未来可选：
// - 基于用户主动反馈（点赞/点踩）
// - 基于长期互动频率变化
// - 基于明确的情绪词汇统计
```

---

## 📝 实现步骤

### Step 1: 标记旧机制为废弃（1h）

```go
// internal/application/reflection/feedback.go

// Deprecated: 使用 presence/feedback/llm_sentiment.go 代替
// 该实现将在 v0.4.0 移除
type FeedbackCollector struct {
    // ...
}
```

### Step 2: 实现过滤器（2h）

创建 `internal/application/presence/feedback/filter.go`

```go
package feedback

type RelevanceFilter struct {
    botUserID    int64
    botMessageID string
}

func NewRelevanceFilter(botUserID int64, botMessageID string) *RelevanceFilter {
    return &RelevanceFilter{
        botUserID:    botUserID,
        botMessageID: botMessageID,
    }
}

func (f *RelevanceFilter) FilterExplicitFeedback(
    events []ConversationEvent,
) []ConversationEvent {
    // 实现过滤逻辑
}
```

### Step 3: 简化 LLM Prompt（1h）

修改 `buildSentimentPrompt()` 强调只分析明确反馈：

```
# 任务

分析以下消息是否是对机器人的**明确反馈**。

## 明确反馈的标准

1. 直接回复或引用机器人消息
2. @提及机器人
3. 包含明确的纠正/赞同/拒绝词汇

## 不是反馈

- 时间接近但无引用
- 用户之间的对话
- 话题转移

## 输出

如果是明确反馈，返回类型和理由。
如果不是明确反馈，返回 "unknown"。

{
  "type": "correction|agreement|rejection|engagement|unknown",
  "confidence": 0.8,
  "reasoning": "用户直接回复说'不对'，是明确的纠正"
}
```

### Step 4: 修改调用处（1h）

在 `group_actor/actor.go` 中：

```go
// 收集窗口内的事件
events := getEventsInWindow(...)

// 过滤明确相关的
filter := feedback.NewRelevanceFilter(botUserID, sentMessageID)
relevantEvents := filter.FilterExplicitFeedback(events)

if len(relevantEvents) == 0 {
    // 无明确反馈，保持未知
    return
}

// 只对明确相关的进行 LLM 分析
feedback, _ := llmAnalyzer.AnalyzeFeedback(ctx, relevantEvents)

// 根据反馈类型更新状态
switch feedback.Type {
case TypeCorrection:
    // 降低信任度
case TypeAgreement:
    // 提升信任度
case TypeRejection:
    // 标记为不欢迎
case TypeEngagement:
    // 提升活跃度
case TypeUnknown:
    // 保持不变
}
```

### Step 5: 测试（1-2h）

```go
func TestFilterExplicitFeedback(t *testing.T) {
    tests := []struct {
        name     string
        events   []Event
        expected int // 期望过滤出的数量
    }{
        {
            name: "直接回复",
            events: []Event{
                {ReplyTo: "bot_msg_123", Text: "不对"},
            },
            expected: 1,
        },
        {
            name: "无关对话",
            events: []Event{
                {ReplyTo: "", Text: "你好啊"},
            },
            expected: 0,
        },
        // ... 更多测试
    }
}
```

---

## 📊 效果预期

### 前后对比

| 指标 | 简化前 | 简化后 |
|------|--------|--------|
| 代码行数 | ~500 | ~200 |
| LLM 调用 | 每次都调用 | 仅明确反馈 |
| 准确性 | 低（时间启发式） | 高（明确信号） |
| 假阳性率 | 高 | 低 |
| 维护成本 | 高（两套机制） | 低（一套） |

### 业务价值

- ✅ 减少无效分析
- ✅ 降低 LLM 成本
- ✅ 提升反馈准确性
- ✅ 代码更清晰

---

## 🔗 相关文件

- `internal/application/reflection/feedback.go` - 旧机制（待废弃）
- `internal/application/presence/feedback/llm_sentiment.go` - LLM 分析
- `internal/application/presence/feedback/window.go` - 窗口管理
- `internal/application/presence/group_actor/actor.go` - 调用处

---

## 📋 检查清单

- [ ] 标记旧 FeedbackCollector 为废弃
- [ ] 实现 RelevanceFilter
- [ ] 修改 LLM Prompt
- [ ] 更新调用逻辑
- [ ] 编写测试
- [ ] 更新文档
- [ ] 验证成本降低

---

**预计工作量**: 4-6 小时  
**优先级**: 中  
**依赖**: 无

---

**作者**: 架构优化团队  
**最后更新**: 2026-09-10
