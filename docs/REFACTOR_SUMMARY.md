# 记忆学习重构 - 实施总结

## 已完成的核心组件

### 1. 数据库 Schema 设计 ✅
**文件**: `schema/migrations/001_memory_refactor.sql`

核心改进：
- **memories 表重构**：支持主体隔离（subject_kind/subject_id）、状态管理、时间契约
- **memory_evidence 表**：独立的证据追踪，支持多证据来源
- **memory_changes 表**：完整的变更历史记录
- **learning_event_progress 表**：替代旧的 watermarks，支持可靠追赶
- **索引优化**：主体查询、事实键唯一约束、参与者 GIN 索引

### 2. Domain 层模型 ✅
**文件**: `internal/domain/memory/types.go`, `interfaces.go`

定义了完整的类型系统：
- `Memory`: 权威记忆模型（包含主体、内容、状态、时间、来源）
- `Evidence`: 证据关联
- `Change`: 变更记录
- `MemoryCandidate`: 待校验的候选
- `MemoryConstraint`: 行动约束
- `MemoryContext`: 本轮上下文
- `Store` 和 `Service` 接口定义

### 3. Memory Service 实现 ✅
**文件**: `internal/application/memory/service_new.go`

核心功能：
- **ApplyCandidates**: 校验并应用记忆候选
  - `new`: 创建新记忆（含事实键冲突检查）
  - `update`: 补充证据
  - `correct`: 创建新版本并替代旧版本
  - `revoke`: 明确遗忘
- **GetConstraints**: 精确读取称呼和互动边界
- **ForgetMemory**: 遗忘机制（标记 revoked + 提升 revision）
- **GetMemoryWithEvidence**: 查询记忆及其证据

### 4. PostgreSQL Store 实现 ✅
**文件**: `internal/adapters/storage/postgres/memory_store.go`

数据访问层：
- **事务性 Save**: 同时保存 memory + evidence + change
- **查询方法**: 
  - `ListBySubject`: 按主体查询
  - `ListByFactKey`: 精确查询结构化事实
  - `ListByParticipant`: 按参与者查询情景记忆
- **证据和历史**: `GetEvidence`, `GetChanges`
- **学习进度**: `MarkProgress`, `GetProgress`, `ListUnprocessedEvents`
- **并发控制**: `LockFactKey`（使用 FOR UPDATE NOWAIT）

## 架构亮点

### 主体隔离
```go
// 不同用户的偏好不会混淆
scope: "group_123456"
subject_kind: "user"
subject_id: "user_789"
```

### 证据追踪
```go
// 每条记忆都有可验证的证据
memory_evidence:
  - event_id: "msg_001"
    source_role: "primary"
  - event_id: "msg_002"
    source_role: "context"
```

### 版本管理
```go
// 防止并发冲突
memory.Revision++
// 更正时创建新版本
newMemory.SupersedesID = oldMemory.MemoryID
oldMemory.Status = "superseded"
```

### 事务一致性
```go
// 一次事务完成所有操作
tx.Begin()
  INSERT INTO memories ...
  INSERT INTO memory_evidence ...
  INSERT INTO memory_changes ...
tx.Commit()
```

## 待完成的关键组件

### 高优先级（阻塞首版）

1. **学习服务改造** 🔴
   - 窗口提炼逻辑（替代 n-gram）
   - 结构化候选生成
   - 批量任务处理
   - Outbox 集成

2. **检索服务适配** 🔴
   - 适配新 Memory 结构
   - 主体过滤支持
   - 有效性检查（status + valid_until）
   - Trace 扩展（included_ids + revisions）

3. **Composer 集成** 🔴
   - 读取约束并注入上下文
   - 完整 PersonaContext 组装
   - 发送前校验（版本 + 有效性）

4. **证据校验器** 🔴
   - 验证 event_id 在 messages 表中存在
   - 验证发言人和群归属
   - 验证引用关系

### 中优先级（完善功能）

5. **向量索引适配** 🟡
   - 从 memories 表生成索引任务
   - revision 检查
   - 遗忘后清理

6. **工具接口调整** 🟡
   - 删除旧记忆声明暂存工具，改为 `remember_memory` 直写
   - 可选：添加 `query_memory` 工具

7. **Store 批量操作** 🟡
   - `SaveBatch`: 支持 correct 操作的原子性
   - 事务优化

### 低优先级（后续优化）

8. **测试覆盖** 🟢
   - 单元测试（domain/application）
   - 集成测试
   - 回放测试

9. **数据迁移** 🟢
   - 旧 memories 表备份
   - 选择性迁移（如需要）

10. **旧代码清理** 🟢
    - 删除旧记忆声明表相关
    - 删除 learning_candidates 相关
    - 删除旧学习逻辑

## 如何继续实施

### 立即可做（不依赖其他组件）

1. **执行 Schema 迁移**
```bash
# 备份现有数据
pg_dump -t memories -t learning_candidates > backup.sql

# 执行迁移
psql -d qqbot < schema/migrations/001_memory_refactor.sql
```

2. **单元测试**
```bash
# 测试 domain 层
go test ./internal/domain/memory/...

# 测试 service 层
go test ./internal/application/memory/...
```

3. **Store 集成测试**
```bash
# 需要 PostgreSQL 运行
go test ./internal/adapters/storage/postgres/... -tags=integration
```

### 按顺序实施（有依赖）

**阶段 1: 学习服务** （1-2天）
- 实现窗口提炼逻辑
- 集成 MemoryService.ApplyCandidates
- Outbox 任务投递

**阶段 2: 检索适配** （1天）
- 修改检索查询以支持新结构
- 添加主体过滤
- Trace 字段扩展

**阶段 3: 上下文集成** （1-2天）
- Composer 读取约束
- 完整上下文组装
- 发送前校验

**阶段 4: 测试验证** （2-3天）
- 回放脱敏数据
- 验收测试用例
- 性能测试

**阶段 5: 清理上线** （1天）
- 删除旧代码
- 更新配置
- 部署

## 设计决策记录

### 为什么不使用独立的 Claim 表？
- 简化生命周期：候选直接变为 active 或 pending
- 减少状态同步：不需要 ClaimID → MemoryID 映射
- 性能优化：减少表连接

### 为什么 revision 检查很重要？
- 防止并发覆盖（A 和 B 同时更新同一记忆）
- 遗忘后防止旧任务恢复内容
- 发送前检查记忆是否仍然有效

### 为什么分离 evidence 表？
- 避免 JSONB 数组膨胀
- 支持高效的证据去重
- 方便按事件查询相关记忆

### 为什么需要 changes 表？
- 审计：谁在何时为何修改
- 调试：追溯记忆演变过程
- 更正：展示被更正的内容

## 验收标准（阶段 A）

根据设计文档第 10 节，首版需要通过：

✅ **已实现的基础**
- [x] 主体隔离（不同用户不共用记忆）
- [x] 证据追踪（所有记忆有证据）
- [x] 版本管理（revision + supersedes）
- [x] 事务一致性（atomic save）
- [x] 遗忘机制（revoke + revision++)

⏳ **待验证的行为**
- [ ] 本人偏好能被正确记住和使用
- [ ] 沉默期间也能学习
- [ ] 更正和遗忘及时生效
- [ ] 称呼和边界在发送前检查
- [ ] 重复证据不累计
- [ ] Bot 参与身份需要成功发送支持

## 风险与缓解

### 风险 1: 批量操作事务性不足
**问题**: 当前 Store.Save 只能处理单个记忆，`correct` 操作需要原子更新两条记忆
**缓解**: 
- 短期：先更新旧记忆，再创建新记忆（两次调用）
- 长期：实现 `SaveBatch` 方法

### 风险 2: 证据校验未实现
**问题**: 目前不验证 event_id 是否真实存在
**缓解**:
- 在 Service 层添加证据验证器
- 查询 messages 表确认事件存在

### 风险 3: 向量索引未适配
**问题**: 遗忘后向量索引不会自动清理
**缓解**:
- Outbox 任务投递清理任务
- revision 检查避免陈旧索引

## 参考资源

- **设计文档**: `docs/MEMORY_LEARNING_REFACTOR.md`
- **实施状态**: `docs/IMPLEMENTATION_STATUS.md`
- **Schema**: `schema/migrations/001_memory_refactor.sql`
- **Domain**: `internal/domain/memory/`
- **Service**: `internal/application/memory/service_new.go`
- **Store**: `internal/adapters/storage/postgres/memory_store.go`

## 下一步行动

**推荐优先级**：

1. ⚡ **执行 Schema 迁移**（15分钟）
   - 备份 + 执行 SQL
   - 验证表结构

2. ⚡ **实现学习服务窗口提炼**（4-8小时）
   - 这是激活整个系统的关键
   - 阻塞其他功能测试

3. ⚡ **适配检索服务**（2-4小时）
   - Composer 依赖检索
   - 相对独立，可并行

4. ⚡ **集成到 Composer**（2-4小时）
   - 完整上下文组装
   - 发送前约束检查

5. ⚡ **端到端测试**（4-8小时）
   - 回放测试用例
   - 验证完整流程

**预计总时间**: 2-3天全职开发

---

**实施者注意**：这是一个**不兼容的破坏性重构**。上线时需要：
1. 停止旧 learning worker
2. 清空或转换旧数据
3. 启动新系统
4. 不支持运行时回退

请在测试环境充分验证后再部署生产环境。
