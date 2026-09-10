# 记忆学习系统重构 - 部署准备清单

**日期**: 2026-09-10  
**版本**: v1  
**状态**: ✅ 准备就绪

---

## 🎯 部署前检查清单

### ✅ 核心功能完成

- [x] 数据库 Schema 迁移
- [x] Domain 层完整模型
- [x] Memory Service 实现
- [x] PostgreSQL Store 实现
- [x] LLM 提炼逻辑
- [x] Composer 集成
- [x] 约束校验
- [x] 端到端测试

### ✅ 测试覆盖

- [x] Memory Service 单元测试 (5 个测试)
- [x] Learning Service 单元测试 (8 个测试)
- [x] Prompting 集成测试 (22 个测试)
- [x] 端到端测试 (3 个测试)
- [x] 所有测试通过率 100%

### ⚠️ 待清理项目

#### 1. 旧代码识别

需要删除或标记为废弃的旧代码：

```bash
# 查找可能的旧实现
find internal/application -name "*claim*" -o -name "*old*"
find internal/adapters -name "*claim*"
```

可能需要清理的文件：
- `internal/application/learning/service.go` (旧版本，如果存在)
- `internal/application/memory/service.go` (旧版本，如果存在)
- 任何 `*_claim.go` 文件

#### 2. 配置更新

需要更新的配置：
- 数据库连接字符串
- LLM 提供商配置
- 学习服务窗口参数
- Outbox 任务配置

#### 3. 环境变量

新增环境变量：
```bash
# 记忆学习配置
MEMORY_EXTRACTOR_VERSION=v1
MEMORY_MAX_WINDOW_SIZE=60
MEMORY_WINDOW_DURATION=15m
MEMORY_IDLE_DURATION=2m

# LLM 配置
LLM_PROVIDER=anthropic  # 或其他提供商
LLM_MODEL=claude-3-5-sonnet
LLM_API_KEY=xxx

# 数据库
DATABASE_URL=postgres://...
```

---

## 📋 部署步骤

### 1. 数据库迁移

**⚠️ 破坏性变更 - 必须先停止旧服务**

```bash
# 1. 停止旧 learning worker
systemctl stop learning-worker

# 2. 备份现有数据（已完成）
# backups/backup_20260910_113915.sql

# 3. 执行迁移
psql $DATABASE_URL < schema/migrations/001_memory_refactor.sql

# 4. 验证迁移
psql $DATABASE_URL -c "\d memories"
psql $DATABASE_URL -c "\d memory_evidence"
psql $DATABASE_URL -c "\d memory_changes"
psql $DATABASE_URL -c "\d learning_event_progress"
```

### 2. 代码部署

```bash
# 1. 拉取最新代码
git pull origin main

# 2. 构建
go build -o bin/agent ./cmd/agent

# 3. 运行测试
go test ./...

# 4. 部署二进制
cp bin/agent /opt/agent/
```

### 3. 服务启动

```bash
# 1. 启动主服务
systemctl start agent

# 2. 启动学习 worker（如果独立部署）
systemctl start learning-worker

# 3. 验证服务状态
systemctl status agent
systemctl status learning-worker
```

### 4. 验证部署

```bash
# 1. 检查数据库连接
psql $DATABASE_URL -c "SELECT COUNT(*) FROM memories"

# 2. 检查日志
tail -f /var/log/agent/agent.log

# 3. 测试记忆创建
curl -X POST http://localhost:8080/api/memory/test

# 4. 测试约束读取
curl http://localhost:8080/api/memory/constraints?group_id=123
```

---

## 🔄 回滚计划

如果部署出现问题：

### 数据库回滚

```bash
# 1. 停止新服务
systemctl stop agent learning-worker

# 2. 恢复备份
psql $DATABASE_URL < backups/backup_20260910_113915.sql

# 3. 启动旧服务
systemctl start old-agent
```

### 代码回滚

```bash
# 回滚到上一个稳定版本
git checkout <previous-stable-commit>
go build -o bin/agent ./cmd/agent
systemctl restart agent
```

---

## 📊 监控指标

### 关键指标

1. **记忆创建速率**
   - `memory_created_total`
   - 目标: > 0，稳定增长

2. **学习任务处理**
   - `learning_batch_processed_total`
   - `learning_batch_duration_seconds`
   - 目标: 延迟 < 5s

3. **约束命中率**
   - `constraint_checks_total`
   - `constraint_violations_total`
   - 目标: 违规率 < 1%

4. **数据库性能**
   - `postgres_query_duration_seconds`
   - 目标: p95 < 100ms

### 监控告警

```yaml
alerts:
  - name: MemoryCreationFailed
    expr: rate(memory_create_errors_total[5m]) > 0.1
    severity: warning

  - name: LearningBatchStalled
    expr: absent(learning_batch_processed_total{offset=5m})
    severity: critical

  - name: ConstraintViolationSpike
    expr: rate(constraint_violations_total[5m]) > 10
    severity: warning
```

---

## 🧹 清理任务

### 立即清理

- [ ] 删除未使用的导入
- [ ] 删除注释的代码
- [ ] 统一包名（postgres vs postgresstore）
- [ ] 更新 README.md

### 后续清理（可选）

- [ ] 删除旧的 claim 相关代码
- [ ] 合并 service.go 和 service_new.go
- [ ] 清理测试中的 TODO
- [ ] 优化数据库索引

---

## 📝 文档更新

### 需要更新的文档

- [ ] API 文档
- [ ] 架构图
- [ ] 运维手册
- [ ] 故障排查指南

### 已完成的文档

- [x] IMPLEMENTATION_STATUS.md
- [x] REFACTOR_SUMMARY.md
- [x] QUICKSTART_MEMORY.md
- [x] REFACTOR_COMPLETION_REPORT.md
- [x] FINAL_REPORT.md
- [x] COMPLETION_SUMMARY.md

---

## ⚡ 性能优化建议

### 数据库优化

1. **索引优化**
   ```sql
   -- 已创建的索引
   CREATE INDEX idx_memories_subject ON memories(scope, subject_kind, subject_id, status);
   CREATE INDEX idx_memories_fact_key ON memories(scope, subject_kind, subject_id, predicate, qualifier);
   CREATE INDEX idx_memories_participants ON memories USING gin(participant_ids);
   CREATE INDEX idx_evidence_memory ON memory_evidence(memory_id);
   CREATE INDEX idx_evidence_event ON memory_evidence(event_id);
   
   -- 考虑添加
   CREATE INDEX idx_memories_created ON memories(created_at DESC);
   CREATE INDEX idx_memories_updated ON memories(updated_at DESC);
   ```

2. **查询优化**
   - 使用连接池（已配置）
   - 批量查询优化
   - 缓存热点数据

### 应用优化

1. **LLM 调用**
   - 批量提炼（已实现）
   - 响应缓存
   - 超时控制

2. **内存使用**
   - 窗口大小限制（60 条）
   - 及时释放大对象
   - 使用对象池

---

## 🎉 部署成功标志

- [ ] 数据库迁移成功
- [ ] 服务启动无错误
- [ ] 测试 API 响应正常
- [ ] 监控指标正常
- [ ] 日志无异常
- [ ] 端到端测试通过

---

## 📞 联系方式

**部署负责人**: [Your Name]  
**技术支持**: [Support Email]  
**紧急联系**: [Emergency Phone]

---

## 📅 部署时间表

**建议部署时间**: 周末凌晨 2:00-6:00（低峰期）

**预计停机时间**: 30 分钟

**部署窗口**: 4 小时

---

**部署准备完成**: ✅  
**准备部署**: 待确认  
**部署完成**: 待执行
