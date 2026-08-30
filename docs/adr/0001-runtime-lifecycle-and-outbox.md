# ADR-0001: Runtime 生命周期与异步任务可靠性

## 状态

Accepted

## 背景

入站消息需要快速返回，模型推理、媒体理解、向量索引、学习和策展属于慢任务。旧实现为候选或后台任务直接创建 goroutine/进程内队列，存在并发无界、队列满载丢任务、进程重启丢状态和关闭时任务悬挂的问题。

## 决策

1. Human Presence Runtime 使用固定数量 worker 和有界 job queue；同一群通过 group lock 串行执行，候选在错误、过期和关闭路径都必须进入终态。
2. Group Actor 使用有界候选/去重状态，并以 `actor_idle_ttl` 回收长期无操作且没有 live candidate 的 Actor。working memory 在每次写操作中持久化，重新出现消息时恢复。
3. 可重放的异步副作用使用 `OutboxStore`。任务必须包含 `kind`、幂等键和 JSON payload，领取采用租约；成功标记 completed，失败按 attempts 进入 retry 或 dead-letter。
4. 进程内 background Runtime 仅作为兼容回退和非关键短任务执行器，不能作为持久化事实来源。

## 后果

- 模型延迟或群数量增长不会线性创建 goroutine；关闭可等待有限 worker 收敛。
- outbox 任务可在进程重启后恢复，重复投递由幂等键抑制。
- 每种任务需要稳定的 payload schema 和注册 handler；schema 变更必须保持向后兼容或提供迁移。
- outbox worker 需要监控 pending、retry、dead-letter 数量及处理延迟；生产环境应配置清理策略。

## 约束

- 不把模型 chain-of-thought 写入 outbox payload。
- handler 必须可重入，外部副作用使用业务幂等键。
- 修改租约、重试或终态语义前，先补充重启恢复和并发领取测试。
