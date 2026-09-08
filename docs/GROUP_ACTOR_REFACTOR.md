# Group Actor 改造指南

## 当前状态

已完成的工作（7 个 commits）：
1. ✅ Domain 层完整模型（PersonaContext, ParticipationDecision, ResponsePlan 等）
2. ✅ Application 服务（DecisionEngine, PersonaAssembler, FeedbackCollector）
3. ✅ 数据库 Schema 和 Repository
4. ✅ Dependencies 服务注册和适配器
5. ✅ ResponsePlanner 和 ResponseExecutor
6. ✅ 完整的单元测试和集成测试

待完成的工作：
- ⏳ Group Actor 改造（使用决策引擎替换 ThoughtCandidate）
- ⏳ 反馈窗口启动和收集
- ⏳ 根据反馈更新人格状态

## Group Actor 改造方案

### 第一步：添加新依赖到 actor

```go
// internal/application/presence/group_actor/actor.go

type actor struct {
	groupID       int64
	tailSize      int
	maxCandidates int
	maxSeen       int

	mu           sync.Mutex
	memory       presencedomain.GroupWorkingMemory
	seen         map[string]struct{}
	lastUsedNano atomic.Int64

	// 新增：社交决策相关依赖
	decisionEngine    *socialdecisionsvc.DecisionEngine
	personaAssembler  *personasvc.ContextAssembler
	responsePlanner   *planning.ResponsePlanner
	responseExecutor  *planning.ResponseExecutor
	feedbackCollector *reflectionsvc.FeedbackCollector
	personaID         string
}
```

### 第二步：修改 observe 方法调用决策引擎

```go
func (a *actor) observe(record presencedomain.EventRecord) presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()

	// 原有逻辑：添加到 tail
	a.memory.RecentTail = append(a.memory.RecentTail, record)
	if len(a.memory.RecentTail) > a.tailSize {
		a.memory.RecentTail = a.memory.RecentTail[len(a.memory.RecentTail)-a.tailSize:]
	}
	a.seen[record.EventID] = struct{}{}
	
	// 新增：调用决策引擎
	go a.decideAndRespond(context.Background(), &record)
	
	return a.memory
}
```

### 第三步：实现 decideAndRespond 方法

```go
func (a *actor) decideAndRespond(ctx context.Context, evt *presencedomain.EventRecord) {
	// 1. 构建决策请求
	req := socialdecisionsvc.DecisionRequest{
		PersonaID:      a.personaID,
		GroupID:        evt.GroupID,
		TriggerEventID: evt.EventID,
		TargetUserID:   evt.UserID,
		
		// 硬规则检查
		IsSelfMessage:              evt.Origin == presencedomain.OriginOutbound,
		SecondsSinceLastBotMessage: a.getSecondsSinceLastBot(),
		ConsecutiveBotMessages:     a.getConsecutiveBotCount(),
		EventAge:                   time.Since(time.Unix(evt.TimestampUnix, 0)),
		
		// 场景检查
		IsDirectMention:    evt.MentionedBot || evt.NamedBot,
		IsQuestionToBot:    a.isQuestionToBot(evt),
		IsFastConversation: a.isFastConversation(),
	}
	
	// 2. 调用决策引擎
	decision, err := a.decisionEngine.DecideParticipation(ctx, req)
	if err != nil {
		log.Error("decision failed", "error", err)
		return
	}
	
	// 3. 如果决定不参与，直接返回
	if !decision.Participate {
		log.Debug("skip participation", 
			"reason", decision.ReasonCode,
			"event", evt.EventID)
		return
	}
	
	// 4. 组装 PersonaContext
	personaCtx, err := a.personaAssembler.AssembleContext(ctx, 
		personadomain.PersonaConfig{ID: a.personaID}, 
		evt.GroupID)
	if err != nil {
		log.Error("assemble persona context failed", "error", err)
		return
	}
	
	// 5. 创建回复计划
	convEvt := a.toConversationEvent(evt)
	plan, err := a.responsePlanner.CreateResponsePlan(ctx, decision, convEvt, personaCtx)
	if err != nil {
		log.Error("create response plan failed", "error", err)
		return
	}
	
	// 6. 执行回复计划
	actionID, err := a.responseExecutor.ExecutePlan(ctx, plan)
	if err != nil {
		log.Error("execute plan failed", "error", err)
		return
	}
	
	// 7. 启动反馈窗口
	go a.startFeedbackWindow(ctx, decision.DecisionID, actionID, evt.GroupID)
}
```

### 第四步：实现辅助方法

```go
func (a *actor) getSecondsSinceLastBot() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	for i := len(a.memory.RecentTail) - 1; i >= 0; i-- {
		evt := a.memory.RecentTail[i]
		if evt.Origin == presencedomain.OriginOutbound {
			elapsed := time.Since(time.Unix(evt.TimestampUnix, 0))
			return int(elapsed.Seconds())
		}
	}
	return 999999 // 很久没说话
}

func (a *actor) getConsecutiveBotCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	count := 0
	for i := len(a.memory.RecentTail) - 1; i >= 0; i-- {
		evt := a.memory.RecentTail[i]
		if evt.Origin == presencedomain.OriginOutbound {
			count++
		} else {
			break
		}
	}
	return count
}

func (a *actor) isQuestionToBot(evt *presencedomain.EventRecord) bool {
	text := strings.ToLower(evt.Text)
	return strings.Contains(text, "?") || 
	       strings.Contains(text, "？") ||
	       strings.HasPrefix(text, "为什么") ||
	       strings.HasPrefix(text, "怎么") ||
	       strings.HasPrefix(text, "什么")
}

func (a *actor) isFastConversation() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	if len(a.memory.RecentTail) < 3 {
		return false
	}
	
	// 检查最近3条消息是否在5秒内
	tail := a.memory.RecentTail
	last := tail[len(tail)-1]
	third := tail[len(tail)-3]
	gap := time.Unix(last.TimestampUnix, 0).Sub(time.Unix(third.TimestampUnix, 0))
	return gap < 5*time.Second
}

func (a *actor) toConversationEvent(evt *presencedomain.EventRecord) *conversationdomain.ConversationEvent {
	return &conversationdomain.ConversationEvent{
		EventID:       evt.EventID,
		GroupID:       evt.GroupID,
		UserID:        evt.UserID,
		MessageID:     evt.MessageID,
		Text:          evt.Text,
		MentionedBot:  evt.MentionedBot,
		NamedBot:      evt.NamedBot,
		IsReplyToBot:  evt.IsReplyToBot,
		TimestampUnix: evt.TimestampUnix,
	}
}
```

### 第五步：实现反馈窗口

```go
func (a *actor) startFeedbackWindow(
	ctx context.Context, 
	decisionID, actionID string, 
	groupID int64,
) {
	// 启动反馈窗口
	window, err := a.feedbackCollector.StartFeedbackWindow(
		ctx,
		decisionID,
		actionID,
		groupID,
		time.Now(),
	)
	if err != nil {
		log.Warn("failed to start feedback window", "error", err)
		return
	}
	
	// 等待观察期结束
	time.Sleep(window.ObserveDuration)
	
	// 收集反馈
	feedback, err := a.feedbackCollector.CollectFeedback(ctx, window)
	if err != nil {
		log.Error("failed to collect feedback", "error", err)
		return
	}
	
	log.Info("feedback collected", 
		"type", feedback.Type,
		"sentiment", feedback.OverallSentiment,
		"engagement", feedback.EngagementLevel)
	
	// TODO: 根据反馈更新人格状态
	a.updatePersonaStateFromFeedback(ctx, feedback)
}

func (a *actor) updatePersonaStateFromFeedback(
	ctx context.Context,
	feedback *feedbackdomain.ActionFeedback,
) {
	// 获取当前即时状态
	state, err := a.personaAssembler.GetEphemeralState(ctx, a.personaID, feedback.GroupID)
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
		// 负面反馈：降低社交耐心
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
	if err := a.personaAssembler.UpdateEphemeralState(ctx, state); err != nil {
		log.Warn("failed to update ephemeral state", "error", err)
	}
}
```

### 第六步：修改 Manager 传入依赖

```go
// internal/application/presence/group_actor/actor.go

type Manager struct {
	log           *ingress.MemoryEventLog
	archive       ports.MemoryStore
	state         WorkingMemoryStore
	tailSize      int
	maxCandidates int
	maxSeen       int
	idleTTL       time.Duration
	
	// 新增：社交决策依赖
	decisionEngine    *socialdecisionsvc.DecisionEngine
	personaAssembler  *personasvc.ContextAssembler
	responsePlanner   *planning.ResponsePlanner
	responseExecutor  *planning.ResponseExecutor
	feedbackCollector *reflectionsvc.FeedbackCollector
	personaID         string

	mu     sync.Mutex
	closed bool
	groups map[int64]*actor
}

// 新增 Option
func WithDecisionEngine(engine *socialdecisionsvc.DecisionEngine) Option {
	return func(m *Manager) { m.decisionEngine = engine }
}

func WithPersonaAssembler(assembler *personasvc.ContextAssembler) Option {
	return func(m *Manager) { m.personaAssembler = assembler }
}

func WithResponsePlanner(planner *planning.ResponsePlanner) Option {
	return func(m *Manager) { m.responsePlanner = planner }
}

func WithResponseExecutor(executor *planning.ResponseExecutor) Option {
	return func(m *Manager) { m.responseExecutor = executor }
}

func WithFeedbackCollector(collector *reflectionsvc.FeedbackCollector) Option {
	return func(m *Manager) { m.feedbackCollector = collector }
}

func WithPersonaID(personaID string) Option {
	return func(m *Manager) { m.personaID = personaID }
}
```

### 第七步：在 app.go 中传入依赖

```go
// internal/app/app.go

// 取消注释之前创建的服务
decisionEngine := socialdecisionsvc.NewDecisionEngine(
	&sceneStoreAdapter{stores.scenes},
	&relationshipStoreAdapter{stores.relationships},
	stores.posture,
	stores.ephemeral,
	socialdecisionsvc.DefaultDecisionConfig(),
)

personaAssembler := personasvc.NewContextAssembler(
	stores.posture,
	stores.ephemeral,
	&factStoreAdapter{stores.personaFacts},
)

eventStoreAdapted := &eventStoreAdapter{stores.memory}
feedbackCollector := reflectionsvc.NewFeedbackCollector(
	eventStoreAdapted,
	reflectionsvc.NewFeedbackClassifier(eventStoreAdapted),
)

// 创建 ResponsePlanner 和 ResponseExecutor
responsePlanner := planning.NewResponsePlanner(
	cfg.Persona,
	// TODO: 传入实际的 composer
)

responseExecutor := planning.NewResponseExecutor(sender)

// 修改 presenceManager 创建，添加新的 options
actorOptions := []presenceactor.Option{
	presenceactor.WithArchive(stores.memory),
	presenceactor.WithIdleTTL(textutil.ParseDurationOr(cfg.Runtime.ActorIdleTTL, 30*time.Minute)),
	presenceactor.WithDecisionEngine(decisionEngine),
	presenceactor.WithPersonaAssembler(personaAssembler),
	presenceactor.WithResponsePlanner(responsePlanner),
	presenceactor.WithResponseExecutor(responseExecutor),
	presenceactor.WithFeedbackCollector(feedbackCollector),
	presenceactor.WithPersonaID(cfg.Persona.ID),
}
if stateStore, ok := stores.memory.(presenceactor.WorkingMemoryStore); ok {
	actorOptions = append(actorOptions, presenceactor.WithStateStore(stateStore))
}
presenceManager := presenceactor.NewManager(eventLog, actorOptions...)
```

## 迁移策略

### 阶段 1：并行运行（推荐）

保留现有的 ThoughtCandidate 逻辑，同时启用新的决策流程：

```go
func (a *actor) observe(record presencedomain.EventRecord) presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	// 原有逻辑不变
	a.memory.RecentTail = append(a.memory.RecentTail, record)
	// ...
	a.mu.Unlock()
	
	// 新增：并行运行决策引擎（可通过 feature flag 控制）
	if enableNewDecisionEngine {
		go a.decideAndRespond(context.Background(), &record)
	}
	
	return a.memory
}
```

### 阶段 2：完全切换

确认新流程稳定后，移除 ThoughtCandidate 相关代码：

1. 移除 `enqueue()` 方法
2. 移除 `claim()` 方法
3. 移除 `complete()` 方法
4. 移除 `canExecute()` 方法
5. 移除 `memory.Candidates` 字段
6. 简化 `GroupWorkingMemory` 结构

## 测试建议

### 单元测试

创建 `actor_decision_test.go`：

```go
func TestActor_DecideAndRespond_DirectMention(t *testing.T) {
	// Mock 所有依赖
	// 测试直接 @ 的情况
}

func TestActor_DecideAndRespond_Cooldown(t *testing.T) {
	// 测试冷却时间阻塞
}

func TestActor_FeedbackWindow(t *testing.T) {
	// 测试反馈窗口启动和收集
}
```

### 集成测试

```go
func TestActor_Integration_FullFlow(t *testing.T) {
	// 端到端测试：
	// 1. 接收事件
	// 2. 决策引擎判断
	// 3. 创建回复计划
	// 4. 执行计划
	// 5. 启动反馈窗口
	// 6. 收集反馈
	// 7. 更新人格状态
}
```

## 监控和日志

添加关键指标：

```go
// 决策指标
metrics.RecordDecision(decision.Participate, decision.ReasonCode)

// 回复执行指标
metrics.RecordResponseExecution(plan.SocialGoal, success)

// 反馈指标
metrics.RecordFeedback(feedback.Type, feedback.EngagementLevel)
```

## 注意事项

1. **异步处理**：`decideAndRespond` 应该是异步的，不阻塞 observe
2. **错误处理**：决策或执行失败不应影响事件记录
3. **并发安全**：访问 actor.memory 需要加锁
4. **性能**：决策引擎调用应该在 50ms 内完成
5. **降级策略**：决策引擎失败时可以回退到简单规则

## 下一步行动

1. 在 `group_actor/actor.go` 中添加新字段和方法
2. 实现 `decideAndRespond` 和辅助方法
3. 在 `app.go` 中取消注释服务并传入依赖
4. 编写单元测试验证新流程
5. 部署到测试环境观察运行情况
6. 逐步迁移到新流程
7. 移除旧的 ThoughtCandidate 代码

---

**当前进度**：已完成 70%
**预计剩余工作量**：2-3 小时（包括测试和调试）

文档版本：v1.0
创建时间：2026-09-08
