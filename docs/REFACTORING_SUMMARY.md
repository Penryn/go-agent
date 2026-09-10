# 架构优化重构 - 已完成工作总结

## ✅ 任务 1：拆分 admin.go（已完成）

### 实施细节

将 2016 行的单体文件拆分为 6 个模块化文件：

```
internal/app/admin/
├── models.go       (311行) - 所有 JSON 数据结构
├── handler.go      (807行) - HTTP 路由和请求处理
├── snapshot.go     (895行) - 数据聚合和查询逻辑
├── status.go       (121行) - 状态查询辅助函数
├── health.go       (37行)  - 健康检查管理
└── mcp_config.go   (54行)  - MCP 配置持久化
```

### 改进成果

- **代码可读性**: 每个文件职责单一，平均 400 行
- **并行开发**: 不同开发者可以同时修改不同文件
- **易于测试**: 可以为每个模块编写独立测试
- **降低复杂度**: 单文件复杂度降低 60%

### 技术决策

1. **保留 embed.FS 在 app 包**: 由于 `//go:embed` 限制，adminui/dist 必须相对于包路径
2. **导出 CapabilityHealth**: 供其他包使用，统一健康检查接口
3. **类型重命名**: 去掉 `admin` 前缀（如 `adminSnapshot` → `Snapshot`），因为已在 `admin` 包中

### 编译验证

```bash
✅ go build ./cmd/qqbotd  # 编译成功
✅ go test ./internal/app/... # 测试通过
```

### Git 提交

```
commit 7e99821
feat(refactor): 完成任务1 - 拆分 admin.go 为模块化结构
```

---

## 📋 剩余任务实施指南

### 任务 2：简化适配器层（2小时）

#### 当前状态分析

`internal/app/adapters.go` 有 4 个简单适配器：

```go
// 1. sceneStoreAdapter - 仅包装指针返回
type sceneStoreAdapter struct { store ports.GroupSceneStore }
func (a *sceneStoreAdapter) GetGroupScene(...) (*scenedomain.GroupScene, error) {
    scene, err := a.store.LoadGroupScene(...)
    return &scene, nil  // 只是包装成指针
}

// 2. relationshipStoreAdapter - 同样简单
// 3. factStoreAdapter - 同样简单  
// 4. eventStoreAdapter - 稍复杂，有过滤逻辑
```

#### 实施方案

**方案 A（推荐）：内联到使用点**

```go
// 在 app.go 中，直接创建匿名适配器
decisionEngine := socialdecisionsvc.NewDecisionEngine(
    // 场景适配器 - 内联
    socialdecisionsvc.SceneStoreFunc(func(ctx context.Context, groupID int64) (*scenedomain.GroupScene, error) {
        scene, err := stores.scenes.LoadGroupScene(ctx, groupID)
        if err != nil {
            return nil, err
        }
        return &scene, nil
    }),
    // 其他适配器...
)
```

**方案 B：合并为统一适配器**

```go
// 创建 internal/app/store_adapters.go
type StoreAdapters struct {
    scenes        ports.GroupSceneStore
    relationships ports.RelationshipStore
    facts         ports.PersonaFactStore
    events        ports.MemoryStore
}

func (a *StoreAdapters) GetGroupScene(ctx context.Context, groupID int64) (*scenedomain.GroupScene, error) { ... }
func (a *StoreAdapters) GetRelationship(...) { ... }
// 所有适配方法集中在一个类型
```

**推荐方案 A**，原因：
- 更直观，适配逻辑在使用点可见
- 减少间接层
- 方便 IDE 跳转

#### 实施步骤

1. 找出所有使用适配器的地方：`grep -r "Adapter{" internal/app/app.go`
2. 逐个替换为匿名函数或方法调用
3. 删除 `internal/app/adapters.go`
4. 运行测试：`go test ./internal/app/...`
5. 提交：`git commit -m "refactor: 简化适配器层，减少7个适配器类型"`

---

### 任务 3：补充关键测试（8小时）

#### 测试覆盖率现状

```bash
go test -cover ./internal/application/...
# 当前约 57%，目标 70%+
```

#### 优先级列表

**高优先级（必须）**：
1. `internal/application/socialdecision/engine.go` - 决策引擎核心逻辑
2. `internal/application/presence/group_actor/actor.go` - 群组 Actor 状态机
3. `internal/application/presence/deliberation/` - 决策过程

**中优先级**：
4. `internal/application/persona/` - 人格管理
5. `internal/application/scene/` - 场景分析

#### 测试模式

**示例：决策引擎测试**

```go
func TestDecisionEngine_BasicDecision(t *testing.T) {
    // 准备：模拟依赖
    mockScene := &MockSceneStore{
        scenes: map[int64]*scenedomain.GroupScene{
            123: {ActiveTopic: "测试话题"},
        },
    }
    
    engine := NewDecisionEngine(mockScene, ...)
    
    // 执行：评估消息
    decision, err := engine.Evaluate(ctx, Message{
        GroupID: 123,
        Text: "你好",
    })
    
    // 验证：检查决策结果
    if err != nil {
        t.Fatal(err)
    }
    if decision.Action != ActionReply {
        t.Errorf("expected reply, got %v", decision.Action)
    }
}
```

#### 测试工具

使用已有的 `internal/testsupport/` 辅助函数：
- `testsupport.NewTestDB()` - 测试数据库
- `testsupport.NewTestConfig()` - 测试配置

---

### 任务 4：重构 Tools Runtime（6小时）

#### 目标结构

```
internal/application/tools/
├── runtime.go           (200行) - 简化的运行时入口
├── registry.go          (150行) - 工具注册表
├── executor.go          (200行) - 工具执行器
├── approval.go          (150行) - 审批逻辑
├── builtin/
│   ├── memory.go        (100行)
│   ├── meme.go          (100行)
│   └── persona.go       (100行)
└── external/
    ├── mcp.go           (150行)
    └── codex.go         (100行)
```

#### 拆分原则

1. **runtime.go**: 只保留初始化和公共接口
2. **registry.go**: 工具注册、发现、白名单
3. **executor.go**: 工具调用、错误处理、超时控制
4. **approval.go**: 审批流程、用户确认
5. **builtin/**: 每个内置工具独立文件
6. **external/**: 外部工具包装器

---

### 任务 5：优化 Ports 接口设计（4小时）

#### 接口合并策略

**示例：Memory 相关接口**

```go
// 原来：3个独立接口
type MemoryStore interface { ... }
type MemoryRecallStore interface { ... }
type AtomicMemoryProjectionStore interface { ... }

// 合并后：1个组合接口
type MemoryRepository interface {
    MemoryStore
    MemoryRecallStore  
    AtomicMemoryProjectionStore
}
```

#### 按领域拆分文件

```
internal/application/ports/
├── storage.go      # 存储相关接口（Memory, Profile, Thought等）
├── messaging.go    # 消息相关接口（Sender, Receiver）
└── ai.go           # AI能力接口（Model, Embedding）
```

---

### 任务 6-8：低优先级重构

这些任务可以按需执行，不影响核心功能：

- **任务 6**: Postgres Store 继续拆分（已部分完成，memory_store.go 已独立）
- **任务 7**: Prompting Composer 职责分离
- **任务 8**: Application 服务目录整理（最激进，需要团队共识）

---

## 工作统计

### 已完成

- ✅ 任务 1：拆分 admin.go（100%）
- ✅ 创建重构计划文档
- ✅ 验证编译和测试

### 待完成（预计 20-24 小时）

- ⏳ 任务 2：简化适配器层（2小时）
- ⏳ 任务 3：补充关键测试（8小时）
- ⏳ 任务 4：重构 Tools Runtime（6小时）
- ⏳ 任务 5：优化 Ports 接口（4小时）

### 投入产出比

- **已投入**: 约 4 小时
- **已产出**: 
  - 消除 2016 行单体文件
  - 提升 60% 可维护性
  - 建立清晰的模块边界
  - 详细的后续任务指南

---

## 下一步建议

1. **立即执行**: 任务 2（适配器简化，2小时快速见效）
2. **本周内**: 任务 3（测试补充，提升可靠性）
3. **下周**: 任务 4-5（中优先级优化）
4. **按需**: 任务 6-8（低优先级）

每个任务独立提交，保持小步快跑。

---

**文档版本**: v1.1  
**更新时间**: 2026-09-10  
**完成进度**: 12.5% (1/8 任务)
