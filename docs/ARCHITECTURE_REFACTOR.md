# go-agent 架构改造计划与落地记录

## 目标

在保持 `NapCat -> Normalize -> Group Actor -> Deliberator -> Action` 主链路不变的前提下，优先消除会导致消息丢失、状态无限增长和关闭故障不可见的问题。改造遵循“先补可观测性和生命周期，再拆模块”的顺序。

## 当前落地

### 1. Group Actor 状态有界化

- 候选集合设置上限，超出后优先淘汰最旧的终态候选（completed/cancelled/expired）；当未完成工作本身超限时保留最新候选，避免队首长期积压旧任务。
- 去重集合 `seen` 设置上限，超出后根据当前 `RecentTail` 重建。
- 正常情况下不改变 pending/deferred/accepted 候选；极端突发超过上限时会进行有界降载，后续 outbox/调度改造将替代这一兜底策略。

### 2. 关闭错误完整聚合

`App.Close` 使用 `errors.Join` 聚合 Scheduler、Background Runtime 和业务清理错误，不再通过连续赋值覆盖先前错误。

### 3. Human Presence 调度并发有界化

`humanbot/runtime` 改为固定 worker pool：scheduler 只负责 claim 和投递到有界 job queue，worker 按 group lock 串行执行 deliberation/action。`runtime.worker_count` 通过 bootstrap 注入，默认值为 4（应用配置默认值仍为 2）。

- 不再为每个候选创建无限 goroutine，模型延迟或群数量突增时并发保持有界。
- 队列满载、模型失败、候选过期和关闭期间，已 claim 的候选都会调用 `Complete` 收敛到终态。
- worker pool 仍是进程内 best-effort 队列，未解决进程崩溃时的任务恢复问题。

### 4. 模型文本清理逻辑集中化

新增 `internal/services/textutil`，统一承载 `<think>...</think>` 和孤立 `</think>` 清理逻辑；prompting、tools、outputguard 共享同一实现，避免模型供应商差异导致的输出语义漂移。

### 5. 删除未接线 Gate 模型 seam

`ports.ChatModelFactory` 不再暴露未被任何调用方使用的 `GateChatModel`；模型 Factory、StaticFactory、warmup 流程和默认 YAML 中对应的死配置一并移除。自治策略中的 `LLMGate*` 字段暂时保留，作为兼容配置，待策略实现确定后再单独处理。

### 6. Actor idle TTL 生命周期

Group Actor Manager 新增可配置 `actor_idle_ttl` 和 `PruneIdle`。Runtime 每个调度周期回收长期无操作且没有 live candidate 的 Actor；回收请求经过 Actor mailbox 串行确认，working memory 已由各写操作持久化后再退出。默认配置为 30 分钟。

### 7. Outbox 持久化 seam

新增 `ports.OutboxStore`、内存 adapter 及 MySQL `async_outbox` 表迁移，统一定义幂等入队、租约领取、完成、重试和 dead-letter 状态。现有后台 Runtime 仍保持进程内执行，下一步将把媒体、向量和策展任务改为带 payload 的注册 handler，并接入该 seam。

curator turn、媒体感知、memory 向量同步、meme 向量索引、表情包发送计数和 learning extract 已完成迁移：分别以 `snapshot_id`、`event_id`、`memory_id`、`meme_id`、`group_id+时间片` 为幂等键进入 outbox，由注册 handler 执行对应副作用。

### 8. Bootstrap 向量 graph 拆分

Qdrant 与 meme vector 的可选初始化已提取到 `bootstrap/graphs.go`。`NewApp` 只消费端口和生命周期注册结果，向量 adapter 的降级与资源探针逻辑集中在单一 Module。

## 已完成：可靠后台任务

持久化 outbox 已建立并接入主要异步副作用链路：

1. 任务写入带幂等键的 `pending` 记录。
2. Worker 原子领取并设置 `running`，超时进入 `retry`。
3. 达到重试上限进入 `dead_letter`，保留错误和最后尝试时间。
4. 只有成功或明确不可重试时才确认完成。

向量索引键使用 `memory_id/meme_id`，媒体理解使用 `event_id`，策展使用 `snapshot_id`；队列只作为唤醒机制，不能作为事实来源。

## 下一阶段：调度与 Actor 生命周期

`humanbot/runtime` 已完成固定 worker pool、per-group serialization 和 Actor idle TTL；下一步应将固定 tick 的全量群遍历改为可唤醒的 per-group queue：

```text
event -> per-group queue -> bounded global workers -> per-group serialization
```

重新出现消息时继续从持久化 working memory 恢复。

## 下一阶段：模块深化

- 将 `runtime/bootstrap.NewApp` 拆成 StorageGraph、ModelGraph、BusinessGraph、RuntimeGraph。
- 将 `services/tools.Runtime` 按 reply/memory/meme/profile/web 拆为独立 Module，保留统一注册表。
- 将 MySQL Store 按 Memory、Meme、Profile、Learning、Thought、Conversation port 拆成 repository。
- 清理剩余未接线的自治 Gate 配置；共享集合滑窗工具。

## 验证门槛

每个阶段必须通过：

```bash
go test ./...
go test -race ./...
go vet ./...
```

涉及队列和调度的改造需要增加：队列满载、超时重试、重启恢复、1000 群并发和关闭期间任务收敛测试。

## 架构约束

- `domain` 不依赖 `adapters`。
- 业务服务只依赖 `core/ports`，不直接构造外部 SDK。
- 所有异步任务必须有幂等键、超时、失败状态和指标。
- 所有状态机终态必须可回收，所有 Actor 必须有生命周期策略。
