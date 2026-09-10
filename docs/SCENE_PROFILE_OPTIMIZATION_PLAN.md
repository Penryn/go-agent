# 场景和画像指标优化方案

**日期**: 2026-09-10  
**状态**: 方案设计  
**优先级**: 中

---

## 📋 问题分析

### 1. 群场景话题管理

**当前问题**:
```go
// 话题直接用最近一条消息
scene.CurrentTopic = lastMessage.Text
```

**问题**:
- 最后一条可能是"哈哈哈"、"👍"等无意义内容
- 无法反映真正的讨论主题
- 话题变化无记录

### 2. 活跃度无时间衰减

**当前问题**:
```go
// 活跃度简单累加
member.ActivityScore += 1
```

**问题**:
- 一周前的 10 条消息 = 今天的 10 条消息
- 无法区分"曾经活跃"和"当前活跃"
- 长期不活跃的成员分数只增不减

### 3. 成员画像活跃度容易饱和

**当前问题**:
```go
// 10 次消息就达到满分
if messageCount >= 10 {
    activityLevel = 1.0
}
```

**问题**:
- 门槛太低，区分度不足
- 无法识别"偶尔冒泡"和"核心成员"
- 缺少时间维度

### 4. 短消息进入常用语

**当前问题**:
```go
// 所有消息都统计为常用语
commonPhrases = append(commonPhrases, message)
```

**问题**:
- "哈哈"、"好"等进入常用语
- 无重复证据要求
- 维护成本高但价值低

---

## 🎯 优化目标

### 1. 区分"最近文本"和"真正话题"

**最近文本**: 最后一条消息，可能无意义  
**话题摘要**: 基于时间窗口的有意义内容提取

### 2. 引入时间衰减

**公式**: 
```
score(t) = base_score * decay_factor^(days_elapsed)
```

**示例**:
- 今天的 1 条 = 1 分
- 昨天的 1 条 = 0.9 分
- 一周前的 1 条 = 0.5 分
- 一月前的 1 条 = 0.1 分

### 3. 常用语需要重复证据

**规则**:
- 至少出现 3 次
- 至少 2 个不同日期
- 长度 >= 2 字符

### 4. 移除无业务意义的分数

**保留**:
- 活跃度（有时间衰减）
- 最近互动时间
- 发言次数

**移除或降级**:
- "友好度"（难以准确计算）
- "影响力"（缺少证据）
- 过于细分的分类

---

## 🔧 实现方案

### 优化 1: 群场景话题改进

```go
type GroupScene struct {
    // 最近文本（保留原样）
    LastMessageText string
    LastMessageTime time.Time
    
    // 新增：话题摘要
    CurrentTopic    string // 基于时间窗口提取
    TopicStartTime  time.Time
    TopicKeywords   []string
}

// TopicExtractor 话题提取器
type TopicExtractor struct {
    minMessageLength int
    timeWindow       time.Duration
}

func (e *TopicExtractor) ExtractTopic(
    messages []Message,
) string {
    // 1. 过滤短消息和表情
    meaningful := filterMeaningful(messages)
    
    // 2. 提取关键词
    keywords := extractKeywords(meaningful)
    
    // 3. 生成摘要
    if len(keywords) > 0 {
        return strings.Join(keywords[:3], "、")
    }
    
    return "闲聊"
}

func filterMeaningful(messages []Message) []Message {
    var result []Message
    for _, msg := range messages {
        // 跳过短消息
        if len(msg.Text) < 3 {
            continue
        }
        // 跳过纯表情
        if isOnlyEmoji(msg.Text) {
            continue
        }
        result = append(result, msg)
    }
    return result
}
```

### 优化 2: 活跃度时间衰减

```go
type ActivityCalculator struct {
    decayFactor  float64 // 每天的衰减系数，如 0.95
    decayHalfLife int    // 半衰期（天），如 7
}

func NewActivityCalculator() *ActivityCalculator {
    halfLife := 7.0 // 7 天半衰期
    decayFactor := math.Pow(0.5, 1.0/halfLife)
    
    return &ActivityCalculator{
        decayFactor:  decayFactor,
        decayHalfLife: 7,
    }
}

// CalculateActivity 计算时间加权的活跃度
func (c *ActivityCalculator) CalculateActivity(
    events []Event,
    now time.Time,
) float64 {
    var weightedScore float64
    
    for _, event := range events {
        daysElapsed := now.Sub(event.Timestamp).Hours() / 24.0
        weight := math.Pow(c.decayFactor, daysElapsed)
        weightedScore += weight
    }
    
    // 归一化到 0-1
    // 假设"非常活跃"是 30 天内 100 条消息
    normalized := weightedScore / 100.0
    if normalized > 1.0 {
        normalized = 1.0
    }
    
    return normalized
}
```

### 优化 3: 常用语重复证据

```go
type CommonPhraseDetector struct {
    minOccurrences int       // 最少出现次数
    minDays        int       // 最少不同天数
    minLength      int       // 最小长度
    maxLength      int       // 最大长度
}

func NewCommonPhraseDetector() *CommonPhraseDetector {
    return &CommonPhraseDetector{
        minOccurrences: 3,
        minDays:        2,
        minLength:      2,
        maxLength:      20,
    }
}

type PhraseEvidence struct {
    Phrase      string
    Occurrences []time.Time
}

func (d *CommonPhraseDetector) DetectCommonPhrases(
    messages []Message,
) []string {
    // 1. 统计短语出现次数和日期
    evidence := make(map[string]*PhraseEvidence)
    
    for _, msg := range messages {
        phrases := extractPhrases(msg.Text, d.minLength, d.maxLength)
        
        for _, phrase := range phrases {
            if _, exists := evidence[phrase]; !exists {
                evidence[phrase] = &PhraseEvidence{
                    Phrase:      phrase,
                    Occurrences: []time.Time{},
                }
            }
            evidence[phrase].Occurrences = append(
                evidence[phrase].Occurrences,
                msg.Timestamp,
            )
        }
    }
    
    // 2. 过滤符合条件的
    var commonPhrases []string
    
    for phrase, ev := range evidence {
        // 出现次数检查
        if len(ev.Occurrences) < d.minOccurrences {
            continue
        }
        
        // 不同天数检查
        uniqueDays := countUniqueDays(ev.Occurrences)
        if uniqueDays < d.minDays {
            continue
        }
        
        commonPhrases = append(commonPhrases, phrase)
    }
    
    return commonPhrases
}

func extractPhrases(text string, minLen, maxLen int) []string {
    // 分词或 n-gram
    // 简化实现：按标点分割
    parts := strings.FieldsFunc(text, func(r rune) bool {
        return r == '，' || r == '。' || r == '！' || r == '？'
    })
    
    var phrases []string
    for _, part := range parts {
        part = strings.TrimSpace(part)
        if len(part) >= minLen && len(part) <= maxLen {
            phrases = append(phrases, part)
        }
    }
    
    return phrases
}

func countUniqueDays(times []time.Time) int {
    days := make(map[string]bool)
    for _, t := range times {
        day := t.Format("2006-01-02")
        days[day] = true
    }
    return len(days)
}
```

### 优化 4: 简化成员画像

```go
// 简化前
type MemberProfile struct {
    ActivityLevel    float64 // 0-1
    FriendlinessLevel float64 // 0-1
    InfluenceScore   float64 // 0-1
    ResponseRate     float64 // 0-1
    CommonPhrases    []string
    PreferredTopics  []string
    // ... 更多
}

// 简化后
type MemberProfile struct {
    // 核心指标（保留）
    ActivityScore    float64   // 时间加权活跃度
    LastActiveAt     time.Time // 最后活跃时间
    MessageCount     int       // 总消息数
    
    // 可选指标（降低优先级）
    CommonPhrases    []string  // 需要重复证据
    
    // 移除
    // FriendlinessLevel - 难以准确计算
    // InfluenceScore - 缺少证据
}

// UpdateProfile 更新成员画像
func (s *MemberProfileService) UpdateProfile(
    ctx context.Context,
    userID int64,
    groupID int64,
) error {
    // 获取最近 30 天的消息
    messages := s.getRecentMessages(ctx, userID, groupID, 30)
    
    // 计算时间加权活跃度
    activityCalc := NewActivityCalculator()
    activityScore := activityCalc.CalculateActivity(messages, time.Now())
    
    // 检测常用语（可选）
    var commonPhrases []string
    if len(messages) > 50 { // 只有足够数据才检测
        detector := NewCommonPhraseDetector()
        commonPhrases = detector.DetectCommonPhrases(messages)
    }
    
    // 更新画像
    profile := &MemberProfile{
        UserID:        userID,
        GroupID:       groupID,
        ActivityScore: activityScore,
        LastActiveAt:  messages[0].Timestamp, // 最新消息时间
        MessageCount:  len(messages),
        CommonPhrases: commonPhrases,
        UpdatedAt:     time.Now(),
    }
    
    return s.store.UpdateProfile(ctx, profile)
}
```

---

## 📝 实现步骤

### Step 1: 实现时间衰减活跃度（2h）

- [ ] 创建 ActivityCalculator
- [ ] 实现衰减计算
- [ ] 编写测试
- [ ] 更新调用处

### Step 2: 实现话题提取（1.5h）

- [ ] 创建 TopicExtractor
- [ ] 过滤有意义消息
- [ ] 提取关键词
- [ ] 更新 GroupScene 结构

### Step 3: 实现常用语检测（1h）

- [ ] 创建 CommonPhraseDetector
- [ ] 实现重复证据检查
- [ ] 编写测试

### Step 4: 简化成员画像（0.5h）

- [ ] 移除低价值字段
- [ ] 更新数据结构
- [ ] 更新文档

### Step 5: 测试和验证（1h）

- [ ] 单元测试
- [ ] 集成测试
- [ ] 验证效果

---

## 📊 效果预期

### 活跃度对比

| 场景 | 简化前 | 简化后 |
|------|--------|--------|
| 今天 10 条 | 10 分 | 10 分 |
| 一周前 10 条 | 10 分 | 5 分 |
| 一月前 10 条 | 10 分 | 1 分 |

### 话题准确性

| 场景 | 简化前 | 简化后 |
|------|--------|--------|
| 最后"哈哈" | 话题="哈哈" | 话题="游戏攻略" |
| 最后"👍" | 话题="👍" | 话题="周末计划" |

### 常用语质量

| 简化前 | 简化后 |
|--------|--------|
| "哈哈" | (过滤) |
| "好" | (过滤) |
| "确实如此" (只说 1 次) | (过滤) |
| "确实如此" (说了 5 次) | ✓ 保留 |

---

## 📋 检查清单

- [ ] 实现 ActivityCalculator
- [ ] 实现 TopicExtractor
- [ ] 实现 CommonPhraseDetector
- [ ] 简化 MemberProfile
- [ ] 编写测试
- [ ] 更新文档
- [ ] 验证效果提升

---

**预计工作量**: 4-6 小时  
**优先级**: 中  
**依赖**: 无

---

**作者**: 架构优化团队  
**最后更新**: 2026-09-10
