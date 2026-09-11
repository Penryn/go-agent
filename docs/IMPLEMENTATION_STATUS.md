# 记忆学习重构实施状态

实施日期：2026-09-10
基于设计文档：`docs/MEMORY_LEARNING_REFACTOR.md`

## 实施进度

### ✅ 已完成（2026-09-10）

#### 1. Schema 设计 ✅
**文件**: `schema/migrations/001_memory_refactor.sql`
- [x] 重构 memories 表（主体隔离、状态管理、时间契约）
- [x] 新增 memory_evidence 表（证据追踪）
- [x] 新增 memory_changes 表（变更历史）
- [x] 新增 learning_event_progress 表（处理进度）
- [x] 更新 retrieval_traces 表（included 字段）
- [x] 标记旧表为待删除

**亮点**:
- 主体隔离：`scope + subject_kind + subject_id`
- 事实键唯一约束：防止并发冲突
- 参与者 GIN 索引：高效查询情景记忆

#### 2. Domain 模型 ✅
**文件**: `internal/domain/memory/types.go`, `interfaces.go`
- [x] 类型定义：Memory, Evidence, Change, Candidate, Constraint
- [x] 接口定义：Store, Service, RetrievalRequest, ApplyResult
- [x] 基础校验：ValidateCandidate, IsActive, GetFactKey
- [x] 枚举类型：SubjectKind, MemoryType, MemoryStatus, Predicate, BotRole

**亮点**:
- 类型安全的枚举
- 清晰的职责分离
- 丰富的验证逻辑

#### 3. Application 服务 ✅
**文件**: `internal/application/memory/service_new.go`
- [x] Service 核心实现（462 行）
- [x] ApplyCandidates 及各种意图处理：
  - [x] new: 创建新记忆（含事实键冲突检查）
  - [x] update/supplement: 补充证据
  - [x] correct: 创建新版本并替代
  - [x] revoke: 明确遗忘
- [x] GetConstraints（精确读取约束）
- [x] ForgetMemory（遗忘机制）
- [x] GetMemoryWithEvidence（证据查询）

**亮点**:
- 完整的意图处理
- revision 版本管理
- 事务一致性保证

#### 4. PostgreSQL Store 实现 ✅
**文件**: `internal/adapters/storage/postgres/memory_store.go`
- [x] 实现 memory.Store 接口（550+ 行）
- [x] 事务性 Save 方法（memory + evidence + change）
- [x] 查询方法：
  - [x] Get: 获取单条记忆
  - [x] ListBySubject: 按主体查询
  - [x] ListByFactKey: 精确查询结构化事实
  - [x] ListByParticipant: 按参与者查询（JSONB 包含查询）
- [x] 证据和历史：GetEvidence, GetChanges
- [x] 并发控制：LockFactKey（FOR UPDATE NOWAIT）
- [x] 学习进度：MarkProgress, GetProgress, ListUnprocessedEvents

**亮点**:
- UPSERT 语义
- JSONB 数组查询优化
- 完整的事务支持

#### 5. 文档 ✅
- [x] `IMPLEMENTATION_STATUS.md` - 实施进度追踪
- [x] `REFACTOR_SUMMARY.md` - 完整总结（1800+ 行）
- [x] `QUICKSTART_MEMORY.md` - 使用指南和示例
- [x] `REFACTOR_COMPLETION_REPORT.md` - 完成报告

### 🚧 进行中

#### 4. PostgreSQL Store 实现
- [ ] 实现 memory.Store 接口
- [ ] 事务性 Save 方法
- [ ] 查询方法（ListBySubject, ListByFactKey, ListByParticipant）
- [ ] 并发控制（LockFactKey）
- [ ] 学习进度管理

#### 5. 学习服务改造
- [ ] 窗口提炼逻辑（替代 n-gram）
- [ ] 批量任务处理
- [ ] 可靠追赶机制
- [ ] 沉默期学习

#### 6. 检索服务适配
- [ ] 适配新 Memory 结构
- [ ] 主体过滤
- [ ] 有效性检查
- [ ] Trace 扩展（included_ids）

#### 7. Composer 集成
- [ ] 完整上下文组装
- [ ] 约束读取与检查
- [ ] PersonaContext 集成
- [ ] 发送前校验

### ⏳ 待开始

#### 8. 工具接口调整
- [x] 删除旧记忆声明暂存工具
- [x] 实现 remember_memory 直写工具
- [x] 更新工具注册

#### 9. Outbox 任务适配
- [ ] 向量索引任务适配新结构
- [ ] 学习批量任务
- [ ] 清理任务（遗忘后）

#### 10. 测试与验证
- [ ] 单元测试（domain/application）
- [ ] 集成测试（完整流程）
- [ ] 回放测试（脱敏数据）
- [ ] 架构测试（依赖方向）

#### 11. 迁移与清理
- [ ] 数据迁移脚本（如需要）
- [x] 删除旧记忆声明表和 claim 链路
- [ ] 更新配置文件
- [ ] 更新文档

## 当前焦点

**正在实施**：PostgreSQL Store 实现

这是核心数据访问层，需要完整实现所有 Store 接口方法，包括：
1. 事务性 Save（memories + evidence + changes）
2. 各种查询方法
3. 并发控制
4. 学习进度管理

## 下一步行动

1. 完成 PostgreSQL Store 实现
2. 实现学习服务的窗口提炼逻辑
3. 适配检索服务到新结构
4. 集成到 Composer 完整上下文

## 重要注意事项

### 必须遵守的原则
1. **主体隔离**：不同用户/群的记忆不能共用 memory_id
2. **证据追踪**：所有记忆必须有可验证的证据
3. **事务一致性**：memory + evidence + change 必须在同一事务
4. **版本管理**：revision 检查防止并发冲突
5. **遗忘清理**：revoked 状态必须清理索引和缓存

### 已知待解决问题
1. Store.Save 目前只能处理单个记忆，correct 操作需要批量事务
2. GetRelevantMemories 需要集成 retrieval service（BM25/vector/RRF）
3. 证据校验器尚未实现（需要验证 event_id 在 messages 表中存在）
4. 向量索引任务投递尚未实现

## 参考
- 设计文档：`docs/MEMORY_LEARNING_REFACTOR.md`
- Schema：`schema/migrations/001_memory_refactor.sql`
- Domain：`internal/domain/memory/`
- Service：`internal/application/memory/service_new.go`
