# 社交决策引擎集成指南

本文档说明如何在现有 Runtime 中集成新的社交决策引擎。

## 已完成工作

### 1. 核心模型层 ✅

**Domain 层模型**：
- `PersonaContext`: 三层人格模型（稳定身份/群姿态/即时状态）
- `ParticipationDecision`: 结构化决策记录
- `ResponsePlan`: 结构化回复计划
- `ActionFeedback`: 动作反馈记录
- `FeedbackWindow`: 反馈观察窗口

**Application 层服务**：
- `DecisionEngine`: 五步决策引擎（硬规则/场景/关系/人格/模型）
- `ContextAssembler`: PersonaContext 组装器
- `FeedbackCollector`: 反馈收集器
- `FeedbackClassifier`: 反馈分类器

**数据持久化**：
- 5 张新表：`group_persona_postures`, `group_persona_ephemeral`, `participation_decisions`, `action_feedbacks`, `feedback_windows`
- 完整的 Repository 实现

**测试覆盖**：
- Domain 层单元测试 ✅
- Application 层集成测试 ✅
- 所有测试通过 ✅

## 集成步骤

### 第一阶段：接入决策引擎（替换候选调度）

#### 1.1 在 Dependencies 中注册服务

```go
// internal/app/dependencies.go

// 添加新的依赖
type Dependencies struct {
    // ... 现有字段
    
    // 新增的社交决策服务
    DecisionEngine      *socialdecision.DecisionEngine
    PersonaAssembler    *persona.ContextAssembler
    FeedbackCollector   *reflection.FeedbackCollector
}

// 在 NewDependencies 中初始化
func NewDependencies(...) (*Dependencies, error) {
    // ... 现有初始化
    
    // 创建 Repository
    postureRepo := postgresstore.NewPostureRepository(db)
    ephemeralRepo := postgresstore.NewEphemeralStateRepository(db)
    decisionRepo := postgresstore.NewDecisionRepository(db)
    feedbackRepo := postgresstore.NewFeedbackRepository(db)
    
    // 创建 PersonaAssembler
    personaAssembler := persona.NewContextAssembler(
        postureRepo,
        ephemeralRepo,
        factStore, // 使用现有的 FactStore
    )
    
    // 创建 DecisionEngine
    decisionEngine := socialdecision.NewDecisionEngine(
        sceneStore,        // 使用现有的 SceneStore
        relationshipStore, // 使用现有的 RelationshipStore
        postureRepo,
        ephemeralRepo,
        socialdecision.DefaultDecisionConfig(),
    )
    
    // 创建 FeedbackCollector
    feedbackClassifier := reflection.NewFeedbackClassifier(eventStore)
    feedbackCollector := reflection.NewFeedbackCollector(
        eventStore,
        feedbackClassifier,
    )
    
    deps.DecisionEngine = decisionEngine
    deps.PersonaAssembler = personaAssembler
    deps.FeedbackCollector = feedbackCollector
    
    return deps, nil
}
```

#### 1.2 修改 Group Actor 调用决策引擎

```go
// internal/application/presence/group_actor/actor.go

// 现有的候选调度逻辑：
// func (a *GroupActor) scheduleResponse(...) { ... }

// 改为调用决策引擎：
func (a *GroupActor) decideParticipation(
    ctx context.Context,
    evt *presencedomain.EventRecord,
) (*presencedomain.ParticipationDecision, error) {
    // 构建决策请求
    req := socialdecision.DecisionRequest{
        PersonaID:      a.personaID,
        GroupID:        evt.GroupID,
        TriggerEventID: evt.EventID,
        TargetUserID:   evt.UserID,
        
        // 硬规则检查项（从现有逻辑提取）
        IsSelfMessage:              evt.Origin == presencedomain.OriginOutbound,
        IsBlacklisted:              a.isBlacklisted(evt.UserID),
        SecondsSinceLastBotMessage: a.getSecondsSinceLastBot(),
        ConsecutiveBotMessages:     a.getConsecutiveCount(),
        EventAge:                   time.Since(evt.Timestamp),
        
        // 场景检查项
        IsFastConversation: a.isFastConversation(),
        IsDirectMention:    evt.MentionedBot || evt.NamedBot,
        IsQuestionToBot:    a.isQuestionToBot(evt),
    }
    
    // 调用决策引擎
    decision, err := a.deps.DecisionEngine.DecideParticipation(ctx, req)
    if err != nil {
        return nil, err
    }
    
    // 保存决策记录（用于审计）
    if err := a.deps.DecisionRepo.SaveDecision(ctx, decision); err != nil {
        log.Warn("failed to save decision", "error", err)
    }
    
    return decision, nil
}
```

#### 1.3 根据决策结果执行回复

```go
// 在 Group Actor 的事件处理中：
func (a *GroupActor) handleInboundEvent(ctx context.Context, evt *presencedomain.EventRecord) {
    // 1. 调用决策引擎
    decision, err := a.decideParticipation(ctx, evt)
    if err != nil {
        log.Error("decision failed", "error", err)
        return
    }
    
    // 2. 如果决定不参与，记录原因并返回
    if !decision.Participate {
        log.Debug("skip participation", 
            "reason", decision.ReasonCode,
            "event", evt.EventID)
        return
    }
    
    // 3. 如果决定参与，继续现有的回复流程
    log.Info("participate", 
        "reason", decision.ReasonCode,
        "intent", decision.Intent,
        "event", evt.EventID)
    
    // 调用现有的 planAndExecute 或创建新的 ResponsePlanner
    a.planAndExecute(ctx, evt, decision)
}
```

### 第二阶段：实现 ResponsePlanner（替换工具编排）

#### 2.1 创建 ResponsePlanner 服务

```go
// internal/application/presence/response_planner.go

type ResponsePlanner struct {
    personaService *persona.Service
    contextService *context.Service
    // ... 其他依赖
}

func (p *ResponsePlanner) CreateResponsePlan(
    ctx context.Context,
    decision *presencedomain.ParticipationDecision,
    evt *presencedomain.EventRecord,
) (*presencedomain.ResponsePlan, error) {
    // 1. 组装 PersonaContext
    personaCtx, err := p.assemblePersonaContext(ctx, decision.GroupID)
    if err != nil {
        return nil, err
    }
    
    // 2. 根据 decision.Intent 选择回复策略
    switch decision.Intent {
    case "respond":
        return p.createDirectResponse(ctx, personaCtx, evt, decision)
    case "continue":
        return p.createContinuation(ctx, personaCtx, evt, decision)
    case "moderate":
        return p.createModeration(ctx, personaCtx, evt, decision)
    default:
        return nil, fmt.Errorf("unknown intent: %s", decision.Intent)
    }
}

func (p *ResponsePlanner) createDirectResponse(
    ctx context.Context,
    personaCtx *personadomain.PersonaContext,
    evt *presencedomain.EventRecord,
    decision *presencedomain.ParticipationDecision,
) (*presencedomain.ResponsePlan, error) {
    // 生成回复文本（调用现有的 Composer）
    text, err := p.generateResponseText(ctx, personaCtx, evt)
    if err != nil {
        return nil, err
    }
    
    plan := &presencedomain.ResponsePlan{
        PlanID:       generatePlanID(),
        DecisionID:   decision.DecisionID,
        GroupID:      decision.GroupID,
        TargetUserID: decision.TargetUserID,
        CreatedAt:    time.Now(),
        PrimaryAction: presencedomain.ActionPlan{
            ActionType: "speak",
            Text:       text,
            Intent:     "direct_answer",
        },
        SocialGoal:      "provide_help",
        ExpectedOutcome: "question_answered",
        RiskLevel:       "low",
    }
    
    return plan, nil
}
```

#### 2.2 执行 ResponsePlan

```go
// internal/application/presence/response_executor.go

type ResponseExecutor struct {
    outbound OutboundAdapter
    // ... 其他依赖
}

func (e *ResponseExecutor) ExecutePlan(
    ctx context.Context,
    plan *presencedomain.ResponsePlan,
) (actionID string, err error) {
    // 1. 执行主要动作
    actionID, err = e.executeAction(ctx, plan.PrimaryAction, plan)
    if err != nil {
        return "", err
    }
    
    // 2. 执行次要动作（如果有）
    for _, action := range plan.SecondaryActions {
        if _, err := e.executeAction(ctx, action, plan); err != nil {
            log.Warn("secondary action failed", "error", err)
        }
    }
    
    return actionID, nil
}

func (e *ResponseExecutor) executeAction(
    ctx context.Context,
    action presencedomain.ActionPlan,
    plan *presencedomain.ResponsePlan,
) (string, error) {
    switch action.ActionType {
    case "speak":
        return e.sendTextMessage(ctx, plan.GroupID, action.Text)
    case "react":
        return e.sendReaction(ctx, plan.GroupID, action.Emoji)
    case "meme":
        return e.sendMeme(ctx, plan.GroupID, action.MemeAssetID)
    // ... 其他动作类型
    default:
        return "", fmt.Errorf("unknown action type: %s", action.ActionType)
    }
}
```

### 第三阶段：启动反馈窗口

#### 3.1 发送后启动反馈观察

```go
// 在 ResponseExecutor.ExecutePlan 完成后：
func (a *GroupActor) executeAndObserveFeedback(
    ctx context.Context,
    plan *presencedomain.ResponsePlan,
) error {
    // 1. 执行计划
    actionID, err := a.executor.ExecutePlan(ctx, plan)
    if err != nil {
        return err
    }
    
    // 2. 启动反馈窗口
    window, err := a.deps.FeedbackCollector.StartFeedbackWindow(
        ctx,
        plan.DecisionID,
        actionID,
        plan.GroupID,
        time.Now(),
    )
    if err != nil {
        log.Warn("failed to start feedback window", "error", err)
        return nil // 不阻塞主流程
    }
    
    // 3. 异步收集反馈（30 秒后）
    go a.collectFeedbackAsync(window)
    
    return nil
}

func (a *GroupActor) collectFeedbackAsync(window *presencedomain.FeedbackWindow) {
    time.Sleep(window.ObserveDuration)
    
    ctx := context.Background()
    feedback, err := a.deps.FeedbackCollector.CollectFeedback(ctx, window)
    if err != nil {
        log.Error("failed to collect feedback", "error", err)
        return
    }
    
    // 保存反馈记录
    if err := a.deps.FeedbackRepo.SaveFeedback(ctx, feedback); err != nil {
        log.Warn("failed to save feedback", "error", err)
    }
    
    // 根据反馈更新人格状态
    a.updatePersonaStateFromFeedback(ctx, feedback)
}
```

#### 3.2 根据反馈更新人格状态

```go
func (a *GroupActor) updatePersonaStateFromFeedback(
    ctx context.Context,
    feedback *feedbackdomain.ActionFeedback,
) {
    // 获取当前即时状态
    state, err := a.deps.EphemeralRepo.GetEphemeralState(ctx, a.personaID, feedback.GroupID)
    if err != nil || state == nil {
        return
    }
    
    // 根据反馈类型调整状态
    switch feedback.Type {
    case feedbackdomain.TypePositive:
        // 正面反馈：提升精力和社交耐心
        if state.Energy == personadomain.EnergyLow {
            state.Energy = personadomain.EnergyNormal
        }
        state.SocialPatience = min(1.0, state.SocialPatience + 0.1)
        state.Mood = personadomain.MoodHappy
        
    case feedbackdomain.TypeNegative:
        // 负面反馈：降低精力和社交耐心
        state.SocialPatience = max(0.0, state.SocialPatience - 0.2)
        if state.SocialPatience < 0.3 {
            state.Mood = personadomain.MoodWithdrawn
        }
        
    case feedbackdomain.TypeIgnored:
        // 被忽视：略微降低社交耐心
        state.SocialPatience = max(0.0, state.SocialPatience - 0.05)
    }
    
    // 延长过期时间
    state.ExpiresAt = time.Now().Add(24 * time.Hour)
    
    // 保存更新
    if err := a.deps.PersonaAssembler.UpdateEphemeralState(ctx, state); err != nil {
        log.Warn("failed to update ephemeral state", "error", err)
    }
}
```

## 配置调整

### 决策引擎配置

```yaml
# config.yaml

social_decision:
  # 硬规则阈值
  cooldown_seconds: 10
  consecutive_limit: 3
  event_expiry_seconds: 300
  
  # 场景判断阈值
  fast_conversation_gap: 5
  min_topic_relevance: 0.3
  
  # 关系判断阈值
  min_familiarity: 0.2
  max_friction: 0.7
  min_trust: 0.3
  
  # 人格判断阈值
  min_energy: 0.3
  min_social_patience: 0.2
  
  # 模型判断阈值
  min_confidence: 0.6

feedback:
  # 反馈窗口配置
  observe_duration_seconds: 30
  max_events: 10
```

## 迁移策略

### 渐进式迁移

1. **阶段 1**（本次完成）：核心模型和服务层
   - ✅ Domain 模型定义
   - ✅ Application 服务实现
   - ✅ 数据库 Schema 和 Repository
   - ✅ 单元测试和集成测试

2. **阶段 2**（下一步）：Runtime 集成
   - [ ] 在 Dependencies 中注册服务
   - [ ] 修改 Group Actor 调用决策引擎
   - [ ] 实现 ResponsePlanner
   - [ ] 实现 ResponseExecutor

3. **阶段 3**（后续）：反馈闭环
   - [ ] 启动反馈窗口
   - [ ] 收集和分类反馈
   - [ ] 根据反馈更新人格状态
   - [ ] 管理后台展示决策和反馈数据

4. **阶段 4**（清理）：移除旧代码
   - [ ] 移除 ThoughtCandidate 相关逻辑
   - [ ] 移除终结工具编排
   - [ ] 迁移历史数据

### 兼容性保证

在集成过程中：
- 新旧逻辑可以并存（通过 feature flag 控制）
- 数据库 Schema 向后兼容
- 现有 API 不变

## 验证方法

### 单元测试

```bash
go test ./internal/domain/persona/ -v
go test ./internal/domain/presence/ -v
go test ./internal/domain/feedback/ -v
go test ./internal/application/socialdecision/ -v
go test ./internal/application/persona/ -v
go test ./internal/application/reflection/ -v
```

### 集成测试

创建端到端测试：
1. 模拟入站事件
2. 调用决策引擎
3. 验证决策结果
4. 执行 ResponsePlan
5. 启动反馈窗口
6. 收集反馈
7. 验证状态更新

### 性能测试

- 决策延迟应 < 50ms
- 反馈收集应异步，不阻塞主流程
- 数据库查询应有索引支持

## 监控指标

### 决策指标
- `decision_rate`: 决策速率
- `participation_rate`: 参与率
- `block_reason_distribution`: 阻塞原因分布

### 反馈指标
- `feedback_type_distribution`: 反馈类型分布
- `engagement_level_avg`: 平均参与度
- `sentiment_avg`: 平均情感倾向

### 人格状态指标
- `mood_distribution`: 情绪分布
- `energy_distribution`: 精力分布
- `social_patience_avg`: 平均社交耐心

## 故障排查

### 常见问题

1. **决策总是被阻塞**
   - 检查配置阈值是否过严
   - 查看决策记录的 `reason_code`
   - 验证场景和关系数据是否正确

2. **反馈窗口未触发**
   - 确认异步任务正常运行
   - 检查事件存储查询是否正常
   - 验证时间范围计算

3. **人格状态未更新**
   - 检查反馈分类是否正确
   - 验证 Repository 更新逻辑
   - 查看日志中的错误信息

## 下一步行动

1. **立即执行**：
   - 在 Dependencies 中注册新服务
   - 修改 Group Actor 集成决策引擎
   - 创建端到端集成测试

2. **本周完成**：
   - 实现 ResponsePlanner
   - 实现 ResponseExecutor
   - 启动反馈窗口

3. **下周完成**：
   - 完善反馈分类器（引用检测、情感分析）
   - 管理后台展示决策数据
   - 性能测试和优化

---

文档版本：v1.0
最后更新：2026-09-08
作者：AI-assisted refactor
