# 架构优化重构计划

基于代码审查，识别出8个优化任务。本文档记录重构计划、进度和实施细节。

## 执行原则

1. **渐进式重构**：每个任务独立完成、测试、提交
2. **向后兼容**：保证每次提交都能编译通过
3. **测试先行**：关键重构前补充测试
4. **文档同步**：更新 CLAUDE.md 和 ARCHITECTURE.md

## 任务列表

### 高优先级（立即执行）

#### ✅ 任务 1: 拆分 admin.go（已部分完成）

**问题**：`internal/app/admin.go` 2016 行，包含 HTTP 路由、数据聚合、查询逻辑和数据结构

**方案**：
```
internal/app/admin/
  ├── models.go       # 数据结构（已完成）
  ├── handler.go      # HTTP 路由（框架已完成）
  ├── snapshot.go     # 数据聚合（已完成）
  └── queries.go      # 数据库查询（待提取）
```

**当前状态**：
- ✅ models.go - 311 行，所有 JSON 结构已提取
- ✅ snapshot.go - 895 行，Dashboard 和聚合逻辑已提取
- 🟡 handler.go - 框架已建立，需要迁移完整的 handler 方法实现
- ❌ queries.go - 待从 snapshot.go 中提取数据库查询

**下一步**：
1. 完整迁移所有 handler 方法到 handler.go
2. 提取数据库查询逻辑到 queries.go
3. 更新 app.go 使用 `admin.NewHandler`
4. 删除原始 admin.go
5. 运行测试：`go test ./internal/app/...`

#### ⏳ 任务 2: 简化适配器层

**问题**：`internal/app/adapters.go` 和 `app.go` 中有 7 个单方法适配器

当前适配器：
- `textComposerAdapter` - 单方法包装
- `llmAdapter` - 单方法包装
- `memoryRetrieverAdapter` - 单方法包装
- `sceneStoreAdapter` - 简单转换
- `relationshipStoreAdapter` - 简单转换
- `factStoreAdapter` - 简单转换
- `eventStoreAdapter` - 带过滤逻辑

**方案**：

1. **统一接口**：让服务直接实现目标接口
   ```go
   // 修改 planning.TextComposer 接口使其与 Composer 兼容
   // 或者在 Composer 上添加适配方法
   ```

2. **合并相似适配器**：
   ```go
   type storeAdapters struct {
       scenes        ports.GroupSceneStore
       relationships ports.RelationshipStore
       facts         ports.PersonaFactStore
       events        ports.MemoryStore
   }
   ```

3. **内联简单适配器**：
   ```go
   // 直接使用匿名函数
   responsePlanner := planning.NewResponsePlanner(
       cfg.Persona,
       func(ctx context.Context, ...) string {
           return composer.Instruction(...)
       },
   )
   ```

**预期收益**：减少 7 个类型定义，降低理解成本

#### ⏳ 任务 3: 补充关键测试

**问题**：测试覆盖率 57%（60/106），关键路径有 TODO 标记

需要测试的模块：
- `internal/application/presence/deliberation/` - deliberator_test.go 标记为 TODO
- `internal/application/presence/group_actor/` - actor_test.go 标记为 TODO
- `internal/application/socialdecision/` - 缺少测试
- `internal/application/persona/` - 部分缺失

**方案**：
1. 为决策引擎编写单元测试（模拟依赖）
2. 为 group_actor 编写集成测试
3. 为 socialdecision 编写场景测试
4. 目标：覆盖率提升到 70%+

### 中优先级（1-2 周内）

#### ⏳ 任务 4: 重构 Tools Runtime

**问题**：`internal/application/tools/runtime.go` 1092 行，职责过载

**方案**：
```
internal/application/tools/
  ├── runtime.go          # 简化的运行时入口（<300 行）
  ├── registry.go         # 工具注册中心
  ├── builtin/            # 内置工具
  │   ├── memory.go
  │   ├── meme.go
  │   └── persona.go
  ├── external/           # 外部工具
  │   ├── mcp.go
  │   └── codex.go
  ├── approval.go         # 审批逻辑
  └── executor.go         # 执行器
```

**预期收益**：每个文件 <300 行，职责单一

#### ⏳ 任务 5: 优化 Ports 接口设计

**问题**：26 个接口，部分粒度过细

**方案**：

1. **合并相关接口**：
   ```go
   // 原来的 3 个接口
   type MemoryStore interface {...}
   type MemoryRecallStore interface {...}
   type AtomicMemoryProjectionStore interface {...}
   
   // 合并为
   type MemoryRepository interface {
       MemoryStore
       MemoryRecallStore
       AtomicMemoryProjectionStore
   }
   ```

2. **可选能力用类型断言**：
   ```go
   // ReadAckingSender 不需要独立接口
   if acker, ok := sender.(interface {
       MarkRead(context.Context, int64, string) error
   }); ok {
       acker.MarkRead(ctx, groupID, msgID)
   }
   ```

3. **按领域分包**：
   ```
   internal/application/ports/
     ├── storage.go      # 存储接口
     ├── messaging.go    # 消息接口
     └── ai.go           # AI 能力接口
   ```

**预期收益**：接口数量从 26 减少到 15-18

### 低优先级（按需执行）

#### ⏳ 任务 6: Postgres Store 继续拆分

**当前状态**：
- ✅ memory_store.go - 已拆分
- ⏳ 其他 Store 方法仍在 store.go（1012 行）

**方案**：继续按领域拆分
```
internal/adapters/storage/postgres/
  ├── store.go           # 连接管理（<200 行）
  ├── memory_store.go    # 已有
  ├── meme_store.go      # 表情包
  ├── outbox_store.go    # 异步任务
  ├── profile_store.go   # 用户资料
  ├── thought_store.go   # 思考记录
  └── persona_store.go   # 人格事实
```

#### ⏳ 任务 7: Prompting Composer 职责分离

**问题**：`internal/application/prompting/composer.go` 824 行

**方案**：
```
internal/application/prompting/
  ├── composer.go         # 组装器入口（<200 行）
  ├── templates.go        # 静态模板
  ├── context_builder.go  # 上下文构建
  └── constraints.go      # 约束处理（已有部分）
```

#### ⏳ 任务 8: Application 服务目录整理

**问题**：24 个子目录，部分过于细分

**方案**：

1. **合并工具包**：
   - `textutil` → 移到使用它的包内部
   - `normalizer` → `presence/ingress`
   - `outputguard` → `action`

2. **建立能力边界**：
   ```
   internal/application/
     ├── conversation/     # 对话管理（presence + context + action）
     ├── knowledge/        # 知识管理（memory + retrieval + learning）
     ├── content/          # 内容生成（prompting + tools + meme）
     └── social/           # 社交认知（relationship + scene + socialdecision）
   ```

**注意**：这是最激进的重构，需要大量测试和团队共识

## 实施时间表

| 任务 | 预计工作量 | 目标完成时间 | 状态 |
|------|-----------|-------------|------|
| 任务 1 | 4 小时 | 立即 | 🟡 60% |
| 任务 2 | 2 小时 | 立即 | ⏳ 0% |
| 任务 3 | 8 小时 | 本周 | ⏳ 0% |
| 任务 4 | 6 小时 | 下周 | ⏳ 0% |
| 任务 5 | 4 小时 | 下周 | ⏳ 0% |
| 任务 6 | 3 小时 | 按需 | ⏳ 0% |
| 任务 7 | 3 小时 | 按需 | ⏳ 0% |
| 任务 8 | 12 小时 | 待定 | ⏳ 0% |

**总计**：约 42 小时（不含任务 8）

## 风险控制

1. **每个任务独立分支**：使用 `git checkout -b refactor/task-N` 
2. **测试门禁**：`make test` 必须通过
3. **增量合并**：小步快跑，避免大 PR
4. **回滚预案**：保留原始代码作为注释，确认稳定后删除

## 已知依赖

- 任务 2 依赖任务 1（需要稳定的 admin 结构）
- 任务 5 影响所有 application 层（需要全量测试）
- 任务 8 依赖任务 4-7（需要清晰的职责边界）

## 下一步行动

1. 完成任务 1：拆分 admin.go
2. 执行任务 2：简化适配器层
3. 执行任务 3：补充关键测试
4. 代码审查和合并
5. 继续中优先级任务

---

**更新日期**：2026-09-10  
**负责人**：Refactoring Team  
**文档版本**：v1.0
