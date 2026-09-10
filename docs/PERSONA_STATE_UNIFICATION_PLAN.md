# 人格状态统一方案

**日期**: 2026-09-10  
**任务**: 4/8 - 高优先级

---

## 📋 当前问题分析

### 1. 状态读取重复

**DecisionEngine** (line 122-130):
```go
posture, err := e.postureStore.GetGroupPosture(ctx, ...)     // 第 1 次读取
ephemeral, err := e.ephemeralStore.GetEphemeralState(ctx, ...) // 第 2 次读取
if blocked, reason := e.checkPersonaState(posture, ephemeral); blocked {
    return // 可能因为旧的低精力状态被拒绝
}
```

**ContextAssembler** (line 53-63):
```go
ephemeral, err := a.ephemeralStore.GetEphemeralState(ctx, ...) // 第 3 次读取
if ephemeral == nil || ephemeral.ExpiresAt.Before(time.Now()) {
    ephemeral = personadomain.DefaultEphemeralState(...)       // 在这里重置过期状态
    a.ephemeralStore.UpdateEphemeralState(ctx, ephemeral)
}
```

### 2. 时序问题

**执行顺序**:
1. DecisionEngine 读取状态 → 发现精力低 → 拒绝参与
2. （永远不会到达 Assembler）
3. 过期状态永远不会被重置

**问题**:
- 决策引擎可能因为**过期的旧状态**拒绝参与
- 状态重置逻辑只在 Assembler 中，但决策在前
- 用户报告："明明已经过了一天，机器人还是说累"

### 3. 配置问题

**DecisionConfig**:
```go
type DecisionConfig struct {
    MinEnergy         float64 `json:"min_energy"`          // 未使用！
    MinSocialPatience float64 `json:"min_social_patience"` // 未使用！
    MinConfidence     float64 `json:"min_confidence"`      // 未使用！
    // ...
}
```

**实际代码**:
```go
energyScore := mapEnergyToScore(ephemeral.Energy) // 枚举 → 分数
if energyScore < e.config.MinEnergy {              // 比较
    return true, ReasonLowEnergy
}
```

但 `Energy` 是枚举类型：
```go
type Energy string
const (
    EnergyHigh   Energy = "high"
    EnergyNormal Energy = "normal"
    EnergyLow    Energy = "low"
    EnergyTired  Energy = "tired"
)
```

`mapEnergyToScore` 将其映射为：
- high → 1.0
- normal → 0.6
- low → 0.3
- tired → 0.1

**问题**: 配置看起来可调，但实际是硬编码的枚举映射。

---

## 🎯 统一方案

### 方案 1: 状态快照（推荐）

**核心思想**: 一次读取，多处使用

**实现**:

1. **创建状态快照服务**
```go
type PersonaStateSnapshot struct {
    Posture        *personadomain.GroupPosture
    EphemeralState *personadomain.EphemeralState
    SnapshotAt     time.Time
}

type StateSnapshotService struct {
    postureStore   PostureStore
    ephemeralStore EphemeralStateStore
}

func (s *StateSnapshotService) GetSnapshot(
    ctx context.Context,
    personaID string,
    groupID int64,
) (*PersonaStateSnapshot, error) {
    // 1. 读取姿态
    posture, _ := s.postureStore.GetGroupPosture(ctx, personaID, groupID)
    if posture == nil {
        p := personadomain.DefaultGroupPosture(personaID, groupID)
        posture = &p
    }

    // 2. 读取即时状态
    ephemeral, _ := s.ephemeralStore.GetEphemeralState(ctx, personaID, groupID)
    
    // 3. 重置过期状态（关键！）
    if ephemeral == nil || ephemeral.ExpiresAt.Before(time.Now()) {
        e := personadomain.DefaultEphemeralState(personaID, groupID)
        ephemeral = &e
        // 异步写回，不阻塞
        go s.ephemeralStore.UpdateEphemeralState(context.Background(), ephemeral)
    }

    return &PersonaStateSnapshot{
        Posture:        posture,
        EphemeralState: ephemeral,
        SnapshotAt:     time.Now(),
    }, nil
}
```

2. **修改 DecisionEngine**
```go
func (e *DecisionEngine) Decide(
    ctx context.Context,
    req DecisionRequest,
    snapshot *PersonaStateSnapshot, // 新增参数
) (*Decision, error) {
    // 直接使用 snapshot，不再读取
    if blocked, reason := e.checkPersonaState(
        snapshot.Posture,
        snapshot.EphemeralState,
    ); blocked {
        return decision, nil
    }
    // ...
}
```

3. **修改 ContextAssembler**
```go
func (a *ContextAssembler) Assemble(
    ctx context.Context,
    personaID string,
    groupID int64,
    snapshot *PersonaStateSnapshot, // 新增参数
) (*personadomain.PersonaContext, error) {
    // 直接使用 snapshot，不再读取
    return &personadomain.PersonaContext{
        Identity:       identity,
        Posture:        *snapshot.Posture,
        EphemeralState: *snapshot.EphemeralState,
        CanonicalFacts: canonicalFacts,
    }, nil
}
```

4. **调用流程**
```go
// 在 MessageCoordinator.makeDecision 中
snapshot, _ := stateSnapshotService.GetSnapshot(ctx, personaID, groupID)
decision, _ := decisionEngine.Decide(ctx, req, snapshot)
personaCtx, _ := assembler.Assemble(ctx, personaID, groupID, snapshot)
```

**优点**:
- ✅ 只读取一次数据库
- ✅ 过期状态立即重置
- ✅ 决策和生成使用一致的状态
- ✅ 性能提升

### 方案 2: 清理配置（可选）

**问题**: `MinEnergy`、`MinSocialPatience` 等配置实际不生效

**选项 A**: 删除无效配置
```go
type DecisionConfig struct {
    // 删除 MinEnergy、MinSocialPatience、MinConfidence
    CooldownSeconds      int
    ConsecutiveLimit     int
    EventExpirySeconds   int
    // ...
}
```

**选项 B**: 明确标记为观测值
```go
type DecisionConfig struct {
    // 以下字段仅用于观测，不影响判断
    MinEnergy         float64 `json:"min_energy,omitempty" description:"观测值"`
    MinSocialPatience float64 `json:"min_social_patience,omitempty" description:"观测值"`
}
```

**选项 C**: 真正实现可配置（工作量大）
```go
// 将枚举改为分数，使配置真正生效
type EphemeralState struct {
    EnergyScore float64 `json:"energy_score"` // 0.0-1.0
    // ...
}
```

**推荐**: 选项 A（删除）或 B（标记），保持简单

---

## 📐 实施计划

### Step 1: 创建状态快照服务

**文件**: `internal/application/persona/snapshot.go`
- 实现 `StateSnapshotService`
- 一次读取 + 过期重置逻辑

### Step 2: 修改 DecisionEngine

**文件**: `internal/application/socialdecision/engine.go`
- 接受 `snapshot` 参数
- 删除内部读取逻辑
- 保持向后兼容（可选参数）

### Step 3: 修改 ContextAssembler

**文件**: `internal/application/persona/context.go`
- 接受 `snapshot` 参数
- 删除内部读取逻辑

### Step 4: 集成到 MessageCoordinator

**文件**: `internal/application/presence/coordination/coordinator.go`
- 在 `makeDecision` 中先获取 snapshot
- 传递给 DecisionEngine 和 Assembler

### Step 5: 清理配置（可选）

**文件**: `internal/application/socialdecision/types.go`
- 删除或标记无效配置

### Step 6: 测试

- 验证过期状态正确重置
- 验证只读取一次数据库
- 验证决策和生成使用一致状态

---

## 🔍 关键决策点

### 决策 1: 配置处理方式

**推荐**: 选项 A（删除无效配置）
- 清晰诚实
- 减少混淆
- 未来真需要时再加

### 决策 2: 向后兼容

**推荐**: 让 snapshot 参数可选
```go
func (e *DecisionEngine) Decide(
    ctx context.Context,
    req DecisionRequest,
    snapshot *PersonaStateSnapshot, // nil 时内部读取
) (*Decision, error) {
    if snapshot == nil {
        // 旧行为：内部读取
        snapshot = e.loadSnapshot(ctx, req)
    }
    // 使用 snapshot
}
```

### 决策 3: 全局 vs 按群状态

**当前**: 只有按群状态（Posture、EphemeralState）
**问题**: 审查报告提到"全局 mood/energy"，但我没找到

**结论**: 可能已经统一为按群状态，无需额外操作

---

## 📊 影响评估

### 优点

✅ 解决时序问题（过期状态立即重置）
✅ 性能提升（只读取一次）
✅ 状态一致性（决策和生成用同一快照）
✅ 代码更清晰

### 缺点

⚠️ 需要修改多个文件
⚠️ 需要仔细处理向后兼容

---

## 🚀 实施顺序

1. ✅ **Step 1**: 创建 StateSnapshotService
2. ✅ **Step 2**: 集成测试验证
3. ✅ **Step 3**: 修改 DecisionEngine
4. ✅ **Step 4**: 修改 ContextAssembler
5. ✅ **Step 5**: 集成到 Coordinator
6. ⏸️ **Step 6**: 清理配置（可选）

---

**下一步**: 开始实施 Step 1 - 创建状态快照服务
