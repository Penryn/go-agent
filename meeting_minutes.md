# Sub-Agent 小组讨论会议纪要

## 会议信息

- **主题**: QQ 群友 AI Bot 最佳实践收敛改造评审
- **日期**: 2026-03-12
- **主持**: 主 Agent（首席维护工程师）

## 参与者

| 角色 | 职责 |
|------|------|
| Architecture Agent | 整体架构、模块边界、依赖方向、分层是否干净 |
| Runtime & Infra Agent | bootstrap、配置、启动流程、健康检查、Docker、持久化 |
| AI & Eino Agent | Agent 实现、Eino 集成、模型 wiring、prompt 组织 |
| Protocol & Adapter Agent | OneBot 11 适配、WS/HTTP 通讯、事件标准化、错误隔离 |
| Data & Memory Agent | 消息归档、长期记忆、画像、表情包、数据一致性 |
| Quality & Testing Agent | 测试覆盖、回归风险、可观测性、文档准确性 |

---

## 各 Agent 观点摘要

### Architecture Agent
- **现状**: 分层结构（adapters → core → domain → services → runtime）整体符合 DESIGN.md
- **优点**: domain 层纯模型无业务逻辑，ports 接口实现了依赖反转
- **问题**: 后台 Agent（Curator/Learning/Review）代码存在但未接入 runtime，形成"死代码"风险
- **建议**: 本轮关注 wiring 层面收敛，后台集成留到下个版本

### Runtime & Infra Agent
- **现状**: bootstrap 正确组装了所有依赖；docker-compose 覆盖了 MySQL/Redis/Qdrant/MinIO/NapCat
- **问题**:
  1. docker-compose 中 `qdrant:latest` 和 `minio:latest` 使用浮动标签
  2. healthz 端点不探活任何依赖
  3. qdrant/minio 缺少 healthcheck 配置
  4. scheduler 作业错误被吞没
- **建议**: 立即修复 docker 版本固定、healthz、healthcheck、scheduler 日志

### AI & Eino Agent
- **现状**: Main Persona Agent 使用 ADK 正确；Gate/Vision 使用直接调用；Composer 四层 prompt
- **问题**:
  1. Embedding provider 未实现，语义检索退化
  2. 模型错误会被缓存，无恢复机制
  3. PersonaState/Relationship 未注入 prompt
- **建议**: Embedding 链路和模型缓存修复复杂度较高，建议本轮仅记录；prompt 增强可考虑

### Protocol & Adapter Agent
- **现状**: OneBot 11 标准优先，NapCat 扩展仅在 outbound adapter 层（poke）；WS 有指数退避重连
- **问题**:
  1. WS 连接/断开无日志输出，运维不可见
  2. WS 读超时 90s 硬编码
- **建议**: 添加 WS 日志；90s 读超时合理暂不修改

### Data & Memory Agent
- **现状**: MySQL 做消息归档+记忆+画像+表情包；Redis 做运行时状态；in-memory 做测试
- **问题**:
  1. Qdrant 向量索引未被主链路使用
  2. MinIO 仅在 bootstrap 做 EnsureBucket，未被媒体下载链路真正调用
  3. QueryMemories 使用子字符串匹配而非语义检索
- **建议**: 向量索引和媒体下载链路是后续版本功能，本轮确保存储层接线正确即可

### Quality & Testing Agent
- **现状**: 大部分 service 有单元测试；存储层有 integration test；有 once-event 端到端验证
- **问题**:
  1. MinIO test 在无服务时硬失败而非 skip
  2. 无 coverage 报告
  3. README 文档基本准确但缺少故障排查说明
- **建议**: 修复 MinIO test skip guard；README 改进留后续

---

## 争议与否决过程

### 争议 1: 是否在本轮修复模型缓存错误问题

- **AI & Eino Agent 提议**: 修改 Factory.chatModel 使其不缓存错误结果
- **Architecture Agent 反对**: 缓存逻辑变更可能影响并发安全性，且当前无法在 CI 中充分验证
- **Runtime & Infra Agent 补充**: 同意推迟，模型初始化在 Warmup 阶段已经验证过一次
- **主 Agent 裁决**: **推迟到下一版本**。理由：涉及并发控制变更，风险大于收益，且 Warmup 已经提供了早期失败检测

### 争议 2: 是否在本轮启动 Scheduler 并集成后台任务

- **Architecture Agent 提议**: 至少启动 Scheduler 的 Start()
- **Data & Memory Agent 反对**: Curator/Learning 工作流需要真实模型才能测试，空启动无实际意义
- **Quality & Testing Agent 补充**: 启动空 Scheduler 不会有负面影响，但也没有正面价值
- **主 Agent 裁决**: **推迟**。理由：空 Scheduler 启动无业务价值，且后台链路需要完整的 Embedding+Qdrant 才有意义

### 争议 3: healthz 应该探测哪些依赖

- **Runtime & Infra Agent 提议**: 探测 MySQL + Redis + Qdrant + MinIO + 模型
- **Protocol & Adapter Agent 反对**: Qdrant 和 MinIO 在当前版本非主链路关键，模型探测成本高
- **Architecture Agent 补充**: healthz 应反映服务基本可用性，MySQL 和 Redis 是最小必要集
- **主 Agent 裁决**: **本轮 healthz 仅探测存储依赖（通过 storeBundle 暴露 Ping）**。模型探测和 readiness 分离留后续

### 争议 4: QQ.SelfID 是否需要校验

- **Protocol & Adapter Agent 提议**: 当 qq.enabled=true 时必须校验 self_id > 0
- **Quality & Testing Agent 赞同**: self_id=0 会导致 @bot 检测永远失败，是严重 bug
- **所有 Agent 一致同意**
- **主 Agent 裁决**: **立即修复**

---

## 最终共识

### 本轮实施清单（已排序）

| 序号 | 项目 | 优先级 | 状态 |
|------|------|--------|------|
| 1 | Pin docker-compose images (qdrant, minio) | High | 待实施 |
| 2 | Fix MinIO test skip guard | High | 待实施 |
| 3 | Log scheduler job errors | High | 待实施 |
| 4 | Enrich /healthz to probe dependencies | High | 待实施 |
| 5 | Add WS dial/reconnect logging | High | 待实施 |
| 6 | Add healthcheck for qdrant/minio | Medium | 待实施 |
| 7 | Validate QQ.SelfID when enabled | Medium | 待实施 |

### 明确推迟的项目

| 项目 | 原因 |
|------|------|
| 模型缓存不缓存错误 | 并发安全性风险 |
| 启动 Scheduler | 无业务价值 |
| Embedding → Qdrant 链路 | 需要完整后端支持 |
| Prompt 增强（PersonaState/Relationship） | 低优先级 |
| web_search 工具实现 | 外部依赖 |

---

## 主 Agent 重大决策

1. **本轮范围**: 聚焦"接线质量"和"运维可观测性"，不做功能新增
2. **每个修复单独 commit**: 便于 review 和回退
3. **验证标准**: 每项修改后执行 `go build ./...` + `go test ./...`
4. **不修改 DESIGN.md**: 当前实现与设计的差距记录在 result2.md 中
5. **不做纯风格化重构**: 所有变更都解决真实工程问题
