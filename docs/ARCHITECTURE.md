# 项目架构

当前身份与角色生产链路以 [`ARCHITECTURE_CURRENT.md`](./ARCHITECTURE_CURRENT.md) 为准；身份角色系统的后续改造以 [`AI_GROUP_FRIEND_ARCHITECTURE_PLAN.md`](./AI_GROUP_FRIEND_ARCHITECTURE_PLAN.md) 为准。

本文只保留当前仍有效的模块化单体、事件存储、检索和 Outbox 基础设计，不再描述已经删除的旧决策引擎、旧 Persona Context、旧 ResponsePlanner 或旧反馈表。

## 架构定位

- 部署形态：单个 Go 进程 `qqbotd`，外接 NapCat 和 PostgreSQL/pgvector。
- 代码形态：六边形架构加模块化单体。
- 运行模型：事件驱动，每个群一个 Group Actor，同群串行、跨群并发。
- AI 调度模型：事件进入工作记忆，构建统一 `ContextSnapshot`，经过 Admission Gate 后由 `AgentPlanner` 组织动作。
- 异步模型：视觉理解、向量索引、学习和清理等可重放副作用通过 PostgreSQL Outbox 执行。

## 当前运行链路

```text
NapCat / OneBot
  -> inbound normalizer
  -> Presence Runtime
  -> TurnObserver / GroupActor
  -> ContextService.BuildSnapshot
  -> Admission Gate
  -> Composer / PromptSession / AgentPlanner
  -> Action Service / OutputGuard
  -> Canon prepare
  -> outbound sender
  -> Canon after delivery
  -> TurnObserver.AfterTurn
```

`GroupActor` 负责群内事件归档、工作记忆、消息 burst 和 PromptSession；`ContextService` 负责一次性组装记忆、画像、场景、关系、策略和 PersonaView；`Action Service` 是发送、撤回、表情、表情包和戳一戳的最终边界。

## 分层和依赖

```text
cmd/qqbotd
    -> internal/app                 composition root
       -> application               use cases and orchestration
          -> domain                 facts and state
       -> adapters                  model, NapCat, PostgreSQL
       -> search                    pure retrieval algorithms
```

- `domain` 不依赖数据库、模型、网络或应用生命周期。
- `application` 通过 `ports` 使用外部能力，不直接依赖 adapter。
- `adapters` 实现模型、NapCat、PostgreSQL 和其他外部系统。
- `app` 只负责依赖组装、生命周期和健康检查。
- `search` 保持无基础设施依赖，提供 BM25、scope 等纯算法。

架构依赖由 [`internal/architecture/layers_test.go`](../internal/architecture/layers_test.go) 检查。

## 主要数据边界

| 数据类别 | 主要表 | 说明 |
| --- | --- | --- |
| 会话事实 | `messages` | 入站和出站消息事实 |
| 长期记忆 | `memories`、`memory_vectors` | 权威事实和可重建向量投影 |
| 表情包 | `meme_assets`、`meme_descriptors`、`meme_vectors` | 权威素材和检索投影 |
| 群运行状态 | `group_working_memory`、`runtime_states` | 工作记忆、冷却、连续发言等 |
| 用户画像 | `member_profiles` | 群内成员统计和常用表达 |
| 关系 | `relationship_events`、`relationships`、`relationship_history` | 关系证据、当前投影和历史 |
| 人格事实 | `persona_fact_events`、`persona_fact_reservations` | Canon 事实和并发预留 |
| AI 观测 | `thought_records`、`retrieval_traces`、`model_usage_records` | 决策、检索和成本追踪 |
| 异步任务 | `async_outbox` | 可重放、可重试的后台副作用 |

旧的 `group_persona_postures`、`group_persona_ephemeral`、`participation_decisions`、`action_feedbacks`、`feedback_windows` 已从 schema 和运行数据库删除。

## 检索边界

```text
query + session visibility
  -> hard filter: scope / expiry / type
  -> BM25 candidates + vector candidates
  -> RRF merge / deduplicate
  -> domain ranking
  -> ContextSnapshot or tool result
```

BM25 和向量索引是可重建 projection，不是事实来源。权威事实仍由 PostgreSQL 中的记忆、素材和 Canon 表维护；检索 trace 记录候选、融合和降级轨道。

## Outbox 可靠性

```text
业务事实 + outbox task
  -> 同一事务写入
  -> worker claim + lease
  -> completed / retry / dead_letter
```

Outbox handler 必须可重入，外部副作用使用稳定业务幂等键。向量索引、感知、学习抽取、人格事实 finalize 和发送计数更新都不应依赖仅存在于进程内的队列作为事实来源。

## 当前演进边界

- 角色模式仍需从 `GroupScene.RecommendedRole` 提升为可执行 `PresenceMode`。
- 发送后的反馈窗口需要补齐基于 `action_id`、平台消息 ID 和源事件的可靠归因。
- 同群并发 deliberation 需要 turn token、过期检查和发送幂等。
- BM25、Outbox 和向量 projection 需要继续补充健康检查、回放和死信运维能力。

详细改造顺序、验收标准和身份角色边界见 [`AI_GROUP_FRIEND_ARCHITECTURE_PLAN.md`](./AI_GROUP_FRIEND_ARCHITECTURE_PLAN.md)。
