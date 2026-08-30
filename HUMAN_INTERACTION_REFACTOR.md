# 真人交互改造文档

## 1. 目标

本项目的目标不是“更会自动回复的机器人”，而是一个具备持续存在感的群成员：能记住事实，会根据关系和即时状态调整语气，知道什么时候应该思考、什么时候保持沉默，并能通过后续学习修正自己的行为。

本轮覆盖八个主题：学习反馈链、Actor 状态持久化、结构化认知记录、带取消的输出节奏、上下文增量注入与平台消息归档、Agent 工具循环控制、人格配置与运行时状态分离、主动任务模型与混合检索。

总体链路：

```text
平台事件 -> Normalize -> ArchiveEvent -> Group Actor
  -> ContextSnapshot -> ThoughtCandidate -> Presence Scheduler
  -> Deliberator/Agent Planner -> Action Executor -> Self Event
  -> Reflection/Curator/Learning
```

## 2. Koishi 对照与迁移原则

参考文件：

- `/Users/phlin/Public/code/preference/koishi/packages/core/src/session.ts`：`sendQueued()`、`cancelQueued()` 与会话级发送队列
- `/Users/phlin/Public/code/preference/koishi/packages/core/src/context.ts`：`delay.message`、`delay.character`、`delay.cancel` 等节奏配置
- `/Users/phlin/Public/code/preference/koishi/packages/core/src/middleware.ts`：可组合 middleware、最大深度和生命周期清理
- `/Users/phlin/Public/code/preference/koishi/packages/loader/src/shared.ts`：插件作用域、热重载和 dispose
- `/Users/phlin/Public/code/preference/koishi/packages/core/src/database.ts`、`session.ts`：会话中的用户/频道观察与持久化边界

吸收的原则：会话先承载事件和上下文，再由可组合处理链决定动作；发送节奏属于会话状态，队列可以取消且不会阻塞新事件；配置集中声明节奏参数；插件和后台任务必须有明确的作用域与 dispose；用户/频道观察属于数据层，不与人格配置混淆。

本项目的映射：Koishi 的 `Session` 对应 `ConversationEvent` + `Group Actor`；`sendQueued()` 对应 `Action Executor.executeRhythm()`；middleware 生命周期对应 Runtime 的 observer、background job 和 `Close()`。当前 Go 实现默认使用 350ms 的 bubble 间隔，并通过 `WithBubbleDelay` 注入；后续可进一步接入 Koishi 式“固定消息延迟与按字符长度取最大值”的 YAML 配置。

明确不照搬：

- 不采用随机概率直接发言；本项目使用 Group Actor、候选到期、冷却和认知负载控制存在感。
- 不保存或展示隐藏 chain-of-thought，只保存 `ThoughtRecord` 的解释、证据、动作和结果。
- 不让插件或 cron 绕过 Scheduler 直接发送消息；主动任务必须进入 Actor mailbox。
- 不把人格、心情、精力混成一个可变配置对象。

## 3. 已落地改造

### 3.1 上下文增量注入与平台消息归档

`MemoryStore.ArchiveEvent` 保存平台消息事实。`group_actor.Manager.Observe` 先归档，再更新事件日志和 Group Actor；同一 `EventID` 可安全重试。Group Actor 持有 `GroupWorkingMemory`，包括 recent tail、消息 burst、话题、未解决问题、候选和媒体描述，并通过 `WorkingMemoryStore` 持久化到内存或 MySQL。

`ContextSnapshot.Projection` 提供投影 `Version`、最后事件 `Cursor`、`Complete` 和 `RecentTruncated` 元数据。Composer 使用确定性字符预算，从最新消息向前保留，始终保留当前事件，超限时注入“较早上下文已裁剪”标记。未来可在此 seam 增加摘要或 checkpoint。

### 3.2 Agent 工具循环控制

每次 Plan 创建独立 `toolRuntimeGuard`，默认最多 4 次迭代、12 次工具调用、单次结果 12 KiB。Guard 提供参数 hash 去重、只读工具空结果重试、UTF-8 安全截断、合法 JSON envelope、调用审计和超预算错误。终止工具仅允许 `speak_text`、`quote_reply`、`stay_silent`；观察模式最多两轮并强制沉默。

### 3.3 人格配置与运行时状态分离

`PersonaConfig` 是静态定义，可带 `Version`、按群 `GroupOverrides`、少样本示例和工具白名单。`persona.Resolve` 按基础配置、typed group override、`PersonaOverlay` 合并并生成稳定 hash。`PersonaState` 只保存 mood、energy、talk bias 及时间字段，由 `RuntimeStateStore` 独立持久化。Composer 每回合合并 `PersonaProfile` 与 `PersonaState`，不会把状态写回配置。

### 3.4 主动任务模型

`ThoughtCandidate` 统一表示回答、观察、follow-up 和提醒，包含来源、意图、评分、到期/过期、不确定性、`ReasonCode` 与 `DeliveryTarget`。外部任务使用 `Runtime.ScheduleCandidate` 入队，实际链路仍为：

```text
EnqueueCandidate -> ClaimDue -> CanExecute -> Deliberate -> Execute -> Complete
```

新消息可以 supersede 旧候选，过期或冷却时不会发送。

### 3.5 混合检索

`queryMemoriesDualTrack` 并行调用 MySQL 关键词检索和 Qdrant 语义检索。Qdrant 失败时降级为 MySQL。结果使用 Reciprocal Rank Fusion（`1/(k+rank)`）融合，按 `MemoryID` 去重，并优先保留 MySQL 的完整字段，最终受 top-k 限制。

### 3.6 学习链与结构化认知

入站事件先更新画像投影；完成一次审议和动作后，Runtime 将 Curator 放入后台队列，结果统一经过 Review 再写入长期记忆。原有按 watermark 的批处理学习继续保留，用于从较长时间窗口提炼群黑话和高频表达。

每次完成的 deliberation 写入一条 `ThoughtRecord`，包含候选、解释、证据、不确定性、选择动作和实际结果。它是可检索的认知摘要，不保存模型隐藏 chain-of-thought。当前闭环为“观察 -> 候选 -> 审议 -> 动作 -> 反思/学习”；摘要 checkpoint、长期目标和基于结果的策略更新仍是后续工作。

### 3.7 Koishi 风格的输出节奏

Reply 计划中的多个 bubble 会被拆成多个平台动作，动作之间等待节奏延迟。每个群维护独立取消 token，新的入站事件会取消旧队列的未发送 bubble；队列结束时只清理自己的 token，避免误删新队列。单 bubble、meme、react、recall、poke 仍保持一次发送。

## 4. 数据与接口

| 对象 | 作用 | 存储 |
| --- | --- | --- |
| `ConversationEvent` | 不可变平台消息事实 | `messages` |
| `GroupWorkingMemory` | 可重建群级投影 | `group_working_memory` |
| `ThoughtCandidate` | 待执行行动候选 | 工作记忆 JSON |
| `ThoughtRecord` | 可审计思考摘要 | `thought_records` |
| `PersonaConfig` | 静态人格 | YAML/配置中心 |
| `PersonaState` | 即时状态 | RuntimeStateStore |
| `MemoryRecord` | 长期学习结果 | MySQL + Qdrant |

主要接口：`ArchiveEvent`、`RecentEvents`、`LoadWorkingMemory`、`SaveWorkingMemory`、`Observe`、`EnqueueCandidate`、`ClaimDue`、`CanExecute`、`Complete`、`ScheduleCandidate`、`SearchMemories`、`SaveThought`。

## 5. 分阶段提交

本次改造按功能边界拆分，提交顺序为：

1. `f6874fe` 学习游标模型，`31957b1` 接入画像/Curator 学习链
2. `fad19ee` 持久化 Group Actor 工作记忆
3. `ea88711` 上下文投影游标与有界提示词
4. `13e1224` 主动候选通过 Actor 入队
5. `7e1b8fb` 混合检索 RRF 回归测试
6. `fd3184e` 人格配置与运行时状态分离
7. `c516543` Agent 工具循环预算与审计
8. `034fc07` ThoughtRecord 持久化与新消息打断队列
9. `2ff708a` 修复节奏队列取消句柄的并发安全，`be0c010` 补充取消回归测试
10. `f3cbcaa` 工具 Runtime 缺失时回退确定性 Planner
11. `8901a08` 本改造文档，`1a46bd0` 配置示例，`41cf2bc` 更新提交映射

每个提交只包含对应功能或测试；旧设计文档删除保持为工作区原有状态，未混入上述功能提交。

## 6. 验收标准

- 事件重试不会重复归档或重复进入 Actor。
- 重启后可恢复 recent tail、候选和媒体描述。
- 快照带投影版本/游标；长历史按预算裁剪且当前事件不丢失。
- 工具循环超过任一预算会停止，重复调用不会重复执行副作用工具。
- 群级人格覆盖只影响当前群配置，不污染其他群或 `PersonaState`。
- 主动候选只能经 Actor 入队，并接受到期、过期、supersede 和冷却检查。
- MySQL 与 Qdrant 可融合去重，Qdrant 不可用时仍可关键词检索。
- 完成的 deliberation 可写入不含隐藏推理的 `ThoughtRecord`。
- 多 bubble 回复按节奏拆分发送，新事件可以取消未发送部分；单 bubble 行为不回归。
- `go test ./...` 与 `go test -race ./...` 通过。

## 7. 后续演进

1. 增加摘要 checkpoint 和 token 计数器。
2. 将 `ScheduleCandidate` 接到 follow-up/open loop 管理器和可恢复 cron 表。
3. 为工具增加副作用声明与按工具预算。
4. 建立混合检索离线评估集，调节权重和时间衰减。
5. 将 ThoughtRecord、Reflection 和 Memory 接入统一评估流水线，形成“观察 -> 反思 -> 学习 -> 行为”闭环。
