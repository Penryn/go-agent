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

## 下一阶段：可靠后台任务

当前进程内队列在满载时会丢弃媒体理解、向量索引和策展任务。下一阶段应引入持久化 outbox：

1. 任务写入带幂等键的 `pending` 记录。
2. Worker 原子领取并设置 `running`，超时进入 `retry`。
3. 达到重试上限进入 `dead_letter`，保留错误和最后尝试时间。
4. 只有成功或明确不可重试时才确认完成。

向量索引键使用 `memory_id/meme_id`，媒体理解使用 `event_id`，策展使用 `snapshot_id`。队列只作为唤醒机制，不能作为事实来源。

## 下一阶段：调度与 Actor 生命周期

`humanbot/runtime` 已完成固定 worker pool 和 per-group serialization；下一步应将固定 tick 的全量群遍历改为可唤醒的 per-group queue，并增加 Actor idle TTL：

```text
event -> per-group queue -> bounded global workers -> per-group serialization
```

同时为长期无活动的群增加 Actor idle TTL；回收前将 working memory 持久化，重新出现消息时从持久化状态恢复。

## 下一阶段：模块深化

- 将 `runtime/bootstrap.NewApp` 拆成 StorageGraph、ModelGraph、BusinessGraph、RuntimeGraph。
- 将 `services/tools.Runtime` 按 reply/memory/meme/profile/web 拆为独立 Module，保留统一注册表。
- 将 MySQL Store 按 Memory、Meme、Profile、Learning、Thought、Conversation port 拆成 repository。
- 删除已废弃的 Gate 接口，清理未接线配置；共享 `stripThinkBlocks` 和集合滑窗工具。

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
