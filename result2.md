# 问题与下一步方向报告

## 一、潜在问题

### High 优先级

#### H1: docker-compose 使用 `latest` 标签
- **文件**: `docker-compose.yml`
- **问题**: `qdrant/qdrant:latest` 和 `minio/minio:latest` 使用浮动标签，不同环境可能拉取不同版本，导致不可复现的行为差异
- **影响**: 生产环境或团队协作中行为不一致
- **建议**: 固定到具体稳定版本号

#### H2: MinIO 集成测试未在服务不可用时正确跳过
- **文件**: `internal/adapters/storage/minio/store_test.go`
- **问题**: `minio.New()` 创建客户端时不会立即连接服务器（仅验证参数），因此 `t.Skipf` 分支永远不会被触发。实际连接失败发生在 `EnsureBucket()` 调用中，此时会 `t.Fatalf` 而非 skip
- **影响**: CI 环境中无 MinIO 实例时测试会硬失败
- **建议**: 在 `EnsureBucket` 的连接错误处添加 skip 检测

#### H3: Scheduler 作业错误被静默吞没
- **文件**: `internal/runtime/scheduler/scheduler.go:38`
- **问题**: `_ = job.job(ctx)` 完全忽略作业执行错误
- **影响**: 后台任务（Curator/Learning/Review）失败时无任何可观测信号
- **建议**: 至少 log.Printf 记录错误

#### H4: /healthz 端点不反映真实依赖状态
- **文件**: `internal/runtime/bootstrap/app.go:156-159`
- **问题**: 健康检查仅返回 `{"ok":true}`，不检查 MySQL、Redis、Qdrant、MinIO 连接状态
- **影响**: 编排平台（Docker/K8s）无法正确判断应用是否真正可用
- **建议**: 添加基础设施连接探活

#### H5: WebSocket 连接/断开缺少日志
- **文件**: `internal/adapters/inbound/napcat/ws_receiver.go`
- **问题**: WS 拨号失败和重连仅静默进行 backoff，没有任何日志输出
- **影响**: 运维人员无法了解连接状态，难以排查 NapCat 连通性问题
- **建议**: 添加 dial 失败和重连的 log 输出

### Medium 优先级

#### M1: docker-compose 中 qdrant 和 minio 缺少 healthcheck
- **文件**: `docker-compose.yml`
- **问题**: MySQL 和 Redis 配置了 healthcheck，但 Qdrant 和 MinIO 没有
- **影响**: depends_on 和编排工具无法正确等待就绪
- **建议**: 添加 HTTP 探活

#### M2: 配置中 QQ.SelfID 未被校验
- **文件**: `internal/config/load.go:204-226`
- **问题**: 当 `qq.enabled=true` 时不校验 `self_id > 0`，若 `self_id` 为 0 会导致 @bot 检测永远不成功
- **影响**: Bot 上线但无法被 @ 触发
- **建议**: 添加 `self_id > 0` 校验

#### M3: Embedding Provider 未接通
- **问题**: `ModelsConfig` 有 `Embedding` 配置位，但 Factory 未提供 Embedding 模型创建方法，Qdrant 存储也未被接入主链路
- **影响**: 语义记忆检索链路不可用，当前 `QueryMemories` 使用子字符串匹配
- **建议**: 后续版本打通 Embedding → Qdrant 链路

#### M4: 后台 Curator/Learning/Review 未集成
- **问题**: 三个服务代码完整但 Scheduler 未启动
- **影响**: 长期记忆提炼、画像更新、黑话学习不运行
- **建议**: 暂时不在此轮修复，但应记录为技术债

#### M5: 模型缓存缓存了错误
- **文件**: `internal/adapters/model/factory.go:76-81`
- **问题**: 模型创建失败时，错误也被缓存。后续所有调用都会返回缓存的错误，需要重启才能恢复
- **影响**: 临时网络故障可能导致模型永久不可用
- **建议**: 仅缓存成功结果，或添加缓存过期机制

### Low 优先级

#### L1: DeterministicPlanner 回退响应过于固定
- **问题**: 硬编码中文回复字符串不可配置
- **建议**: 后续可从 PersonaConfig 中读取

#### L2: web_search 工具未实现
- **问题**: 工具在 allowlist 和配置中都有定义，但运行时返回空结果
- **建议**: 要么移除，要么标记为 placeholder

#### L3: PersonaState/RelationshipState 未注入 Prompt
- **问题**: Composer 只使用 PersonaConfig，未利用动态状态
- **建议**: 后续增强 prompt 上下文

---

## 二、设计缺陷

1. **健康检查过于简单**: DESIGN.md 要求检查 MySQL、Redis、Qdrant、MinIO、模型 Provider，当前仅返回 `{"ok":true}`
2. **后台链路完全断开**: Scheduler、Curator、Learning、Review 存在但未接入
3. **Embedding 链路缺失**: 向量检索退化为子字符串匹配

---

## 三、技术债

| 项目 | 严重度 | 说明 |
|------|--------|------|
| 后台任务未接入 | Medium | Scheduler Start() 未调用 |
| Embedding 未实现 | Medium | 语义检索退化 |
| 模型错误缓存 | Medium | 临时故障可能持久化 |
| Vision 无预算控制 | Low | DESIGN.md 要求但未实现 |
| 工具超时未执行 | Low | 配置存在但未强制 |

---

## 四、可用 Eino/ADK/Compose 替代或增强的能力

1. **Embedding Provider**: 当前未使用 `eino-ext` embedding provider。应使用 `eino-ext/components/embedding` 打通向量化链路
2. **Retriever**: 当前内存中的子字符串匹配可以替换为 `eino-ext/components/retriever/qdrant`
3. **Indexer**: 记忆写入后应使用 `eino-ext/components/indexer/qdrant` 同步到向量库
4. **Vision Agent**: 可以考虑使用 Eino 的 `UserInputMultiContent` 替代当前的 URL 传递方式

---

## 五、下一步开发方向

### 本轮实施（最佳实践收敛）

| 序号 | 改动 | 优先级 |
|------|------|--------|
| 1 | Pin docker-compose images to stable versions | High |
| 2 | Fix MinIO test skip guard | High |
| 3 | Log scheduler job errors | High |
| 4 | Enrich /healthz endpoint | High |
| 5 | Add WS reconnect logging | High |
| 6 | Add healthcheck for qdrant/minio in docker-compose | Medium |
| 7 | Validate QQ.SelfID when QQ enabled | Medium |

### 后续版本建议

| 序号 | 方向 | 优先级 |
|------|------|--------|
| 1 | 接通 Embedding → Qdrant 语义检索链路 | High |
| 2 | 启动 Scheduler，集成 Curator/Learning | High |
| 3 | 模型缓存添加过期/重试机制 | Medium |
| 4 | Vision 预算控制按 group 维度 | Medium |
| 5 | 增强 Prompt 注入 PersonaState/Relationship | Medium |
| 6 | 实现 web_search 工具或标记 placeholder | Low |
| 7 | DeterministicPlanner 回退响应可配置化 | Low |
