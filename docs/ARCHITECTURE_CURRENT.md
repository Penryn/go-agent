# Go-Agent 架构现状文档

**日期**: 2026-09-10  
**版本**: v0.3.0 (架构优化后)

---

## 📐 系统架构

### 整体分层

```
┌─────────────────────────────────────────────┐
│          Bot Protocol (OneBot v11)          │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│        MessageCoordinator (核心协调)        │
│  - 同群串行执行                              │
│  - 上下文生命周期管理                        │
│  - 状态一致性保证                            │
└─────────────────────────────────────────────┘
                      ↓
        ┌─────────────┴─────────────┐
        ↓                           ↓
┌──────────────────┐      ┌──────────────────┐
│  DecisionEngine  │      │ ContextAssembler │
│  (决策：是否参与) │      │ (人格上下文组装)  │
└──────────────────┘      └──────────────────┘
        ↓                           ↓
┌──────────────────┐      ┌──────────────────┐
│ ResponsePlanner  │      │     Composer     │
│  (计划：做什么)   │      │ (生成：说什么)    │
└──────────────────┘      └──────────────────┘
        ↓                           ↓
┌─────────────────────────────────────────────┐
│         ActionExecutor (执行：发送)          │
└─────────────────────────────────────────────┘
```

### 核心改进（v0.3.0）

#### 1. MessageCoordinator (任务1 ✅)
**位置**: `internal/application/presence/coordination/coordinator.go`

**职责**:
- 同群消息串行执行（避免竞态）
- 上下文生命周期管理（GroupContext）
- 决策 → 计划 → 生成 → 执行的完整流程

**关键特性**:
```go
type GroupContext struct {
    GroupID        int64
    LastProcessed  time.Time
    InFlight       bool
    MessageQueue   []Message
}
```

#### 2. 增强的 Composer (任务3 ✅)
**位置**: `internal/application/prompting/composer.go`

**改进**:
- ✅ 接入 PersonaContext（姿态、情绪、精力）
- ✅ 支持群聊历史
- ✅ 友好错误提示（不再返回 "direct_answer"）
- ✅ 5 个测试通过

**Prompt 结构**:
```
# 角色设定
你是 XXX

# 当前状态
状态: 比较活跃，愿意参与
心情: 比较愉快
精力: 充沛，可以多聊聊
与这个群: 很熟悉，可以放松

# 相关记忆
...

# 最近对话
...

# 当前消息
...
```

#### 3. StateSnapshotService (任务4 部分完成 ✅)
**位置**: `internal/application/persona/snapshot.go`

**核心价值**:
- 一次读取，多处使用
- **立即重置过期状态**（解决"过了一天还说累"）
- 决策和生成使用一致状态
- 6 个测试通过

**待集成**:
- DecisionEngine 接受 snapshot 参数
- ContextAssembler 接受 snapshot 参数
- Coordinator 中先获取快照

---

## 🗂️ 目录结构

### 关键目录

```
internal/
├── app/
│   └── app.go                    # 应用初始化，依赖注入
├── application/
│   ├── presence/
│   │   ├── coordination/         # ✅ 消息协调器（新）
│   │   ├── planning/             # 决策和计划
│   │   └── runtime/              # 旧运行时（逐步废弃）
│   ├── prompting/
│   │   ├── composer.go           # ✅ 增强版（PersonaContext）
│   │   └── agent_planner.go      # 工具调用（未接入）
│   ├── persona/
│   │   ├── context.go            # 人格上下文组装
│   │   └── snapshot.go           # ✅ 状态快照服务（新）
│   ├── socialdecision/
│   │   └── engine.go             # 决策引擎
│   └── action/
│       └── executor.go           # 动作执行
├── domain/
│   ├── conversation/             # 对话领域模型
│   ├── persona/                  # 人格领域模型
│   ├── presence/                 # 存在感领域模型
│   └── policy/                   # 策略领域模型
└── infrastructure/
    ├── store/                    # 数据持久化
    └── bot/                      # 机器人协议

web/
├── src/                          # ✅ 只保留 TS 源码
└── dist/                         # 编译产物（.gitignore）
```

---

## 🔄 消息处理流程

### 完整链路

```
1. Bot Protocol 接收消息
   ↓
2. MessageCoordinator.HandleMessage()
   ├─ 检查群白名单
   ├─ 获取或创建 GroupContext
   ├─ 加入队列（同群串行）
   └─ 启动处理
   ↓
3. makeDecision()
   ├─ 构建 DecisionRequest
   ├─ 调用 DecisionEngine.Decide()
   │   ├─ 检查策略（白名单、冷却、连续）
   │   ├─ 检查场景（话题、快速对话）
   │   ├─ 检查关系（信任、联系）
   │   └─ 检查状态（精力、耐心）
   └─ 返回 Decision (participate: bool)
   ↓
4. 如果 participate = true:
   ├─ PersonaContext = Assembler.Assemble()
   │   ├─ 读取 Identity
   │   ├─ 读取 GroupPosture
   │   ├─ 读取 EphemeralState（检查过期）
   │   └─ 读取 CanonicalFacts
   ├─ Plan = ResponsePlanner.Plan()
   │   └─ 根据 intent 创建计划
   ├─ Text = Composer.ComposeResponse()
   │   ├─ 使用 PersonaContext
   │   ├─ 检索记忆
   │   ├─ 构建 Prompt（含历史）
   │   └─ 调用 LLM
   └─ ActionExecutor.Execute(Plan)
       └─ 发送消息
```

### 状态管理（改进中）

**当前**:
```
DecisionEngine 读取状态 → 检查精力
ContextAssembler 读取状态 → 重置过期
```

**目标** (待集成):
```
Coordinator 获取 Snapshot → 立即重置过期
    ├─ DecisionEngine 使用 Snapshot
    └─ ContextAssembler 使用 Snapshot
```

---

## 📦 核心组件

### 1. MessageCoordinator
**文件**: `coordination/coordinator.go`  
**测试**: `coordination/coordinator_test.go` (6 个测试)

**API**:
```go
func (c *Coordinator) HandleMessage(
    ctx context.Context,
    evt *conversationdomain.ConversationEvent,
) error
```

### 2. DecisionEngine
**文件**: `socialdecision/engine.go`

**判断维度**:
1. 策略检查：白名单、冷却、连续发言
2. 场景检查：话题状态、快速对话
3. 关系检查：信任度、最近联系
4. 状态检查：精力、社交耐心

### 3. Composer
**文件**: `prompting/composer.go`  
**测试**: `prompting/composer_test.go` (5 个测试)

**API**:
```go
func (c *Composer) ComposeResponseWithHistory(
    ctx context.Context,
    personaCtx *personadomain.PersonaContext,
    evt *conversationdomain.ConversationEvent,
    history []conversationdomain.ConversationEvent,
    intent string,
) (string, error)
```

### 4. StateSnapshotService
**文件**: `persona/snapshot.go`  
**测试**: `persona/snapshot_test.go` (6 个测试)

**API**:
```go
func (s *StateSnapshotService) GetSnapshot(
    ctx context.Context,
    personaID string,
    groupID int64,
) (*PersonaStateSnapshot, error)
```

---

## 🧪 测试覆盖

### 单元测试

| 模块 | 测试数 | 状态 |
|------|--------|------|
| MessageCoordinator | 6 | ✅ PASS |
| Composer | 5 | ✅ PASS |
| StateSnapshotService | 6 | ✅ PASS |
| DecisionEngine | - | ⚠️ 待补充 |
| ContextAssembler | 2 | ✅ PASS |

**运行测试**:
```bash
go test ./internal/application/presence/coordination/... -v
go test ./internal/application/prompting/... -v
go test ./internal/application/persona/... -v
```

---

## ⚙️ 配置

### Persona 配置

**文件**: `config.yaml` → `persona` 部分

```yaml
persona:
  name: "小助手"
  description: "一个友好的助手"
  traits:
    - "友好"
    - "耐心"
    - "幽默"
  reply_max_chars: 200        # 回复长度限制
  reply_max_sentences: 3      # 回复句子数限制
  prefer_memes: true          # 是否偏好表情包
  aliases:                    # 别名
    - "助手"
    - "小助"
```

### 决策配置

**文件**: DecisionConfig

```go
type DecisionConfig struct {
    CooldownSeconds      int     // 冷却时间（秒）
    ConsecutiveLimit     int     // 连续发言限制
    EventExpirySeconds   int     // 事件过期时间
    MinTrust             float64 // 最低信任度
    MinContactRecency    int64   // 最近联系时间
    // 注意：MinEnergy、MinSocialPatience 是观测值，不影响判断
}
```

---

## 🚧 已知问题

### 1. 工具调用未接入
**问题**: AgentPlanner 创建后被丢弃（`_ = NewAgentPlanner(...)`）  
**状态**: 待决策是否需要工具调用  
**位置**: `app/app.go:300`

### 2. 状态快照未完全集成
**问题**: StateSnapshotService 已创建但未接入主流程  
**状态**: 核心逻辑完成，待集成  
**影响**: 仍可能遇到过期状态问题

### 3. 反馈机制重复
**问题**: 两套反馈机制同时运行  
**状态**: 待简化  
**优先级**: 中

### 4. 测试覆盖不完整
**问题**: DecisionEngine、ResponsePlanner 缺少测试  
**状态**: 待补充  
**优先级**: 中

---

## 📝 维护指南

### 添加新功能

1. **确定层级**:
   - Domain: 纯业务逻辑，无依赖
   - Application: 业务服务，协调 Domain
   - Infrastructure: 外部依赖（DB、API）

2. **遵循流程**:
   - 消息 → Coordinator → Decision → Plan → Generate → Execute

3. **使用现有服务**:
   - 决策：DecisionEngine
   - 人格：ContextAssembler + StateSnapshotService
   - 生成：Composer
   - 执行：ActionExecutor

### 修改决策逻辑

**文件**: `socialdecision/engine.go`

添加新的判断维度：
```go
func (e *DecisionEngine) checkNewDimension(...) (bool, string) {
    if shouldBlock {
        return true, presencedomain.ReasonNewBlock
    }
    return false, ""
}
```

### 修改生成逻辑

**文件**: `prompting/composer.go`

修改 Prompt 构建：
```go
func (c *Composer) buildEnhancedResponsePrompt(...) string {
    // 添加新的上下文部分
    sb.WriteString("# 新部分\n")
    sb.WriteString("内容...\n\n")
    return sb.String()
}
```

---

## 🔗 相关文档

- **优化进度**: `ARCHITECTURE_OPTIMIZATION_PROGRESS.md`
- **生成系统方案**: `GENERATION_SYSTEM_UNIFICATION_PLAN.md`
- **状态统一方案**: `PERSONA_STATE_UNIFICATION_PLAN.md`
- **API 文档**: (待补充)
- **部署文档**: (待补充)

---

## 📊 版本历史

### v0.3.0 (2026-09-10)
- ✅ 新增 MessageCoordinator 统一消息处理
- ✅ 增强 Composer 支持 PersonaContext 和历史
- ✅ 创建 StateSnapshotService 解决时序问题
- ✅ 清理前端重复代码
- ✅ 17 个新测试

### v0.2.x (历史版本)
- 基础决策引擎
- 人格系统框架
- 记忆检索
- 表情包支持

---

**维护者**: 架构优化团队  
**最后更新**: 2026-09-10  
**状态**: 活跃开发中
