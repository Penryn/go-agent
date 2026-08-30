# 真人交互改造文档

## 1. 目标

本项目的目标不是“更会自动回复的机器人”，而是一个具备持续存在感的群成员：能记住事实，会根据关系和即时状态调整语气，知道什么时候应该思考、什么时候保持沉默，并能通过后续学习修正自己的行为。

本轮覆盖五个主题：上下文增量注入与平台消息归档、Agent 工具循环控制、人格配置与运行时状态分离、主动任务模型、混合检索。

总体链路：

```text
平台事件 -> Normalize -> ArchiveEvent -> Group Actor
  -> ContextSnapshot -> ThoughtCandidate -> Presence Scheduler
  -> Deliberator/Agent Planner -> Action Executor -> Self Event
  -> Reflection/Curator/Learning
```

## 2. AstrBot 对照与迁移原则

参考文件：

- `astrbot/builtin_stars/astrbot/group_chat_context.py`：群上下文与消息窗口
- `astrbot/core/platform_message_history_mgr.py`：平台消息历史归档
- `astrbot/core/agent/runners/tool_loop_agent_runner.py`：Agent 工具循环
- `astrbot/core/agent/context/manager.py`、`compressor.py`、`truncator.py`：上下文压缩与截断
- `astrbot/core/persona_mgr.py`：人格配置管理
- `astrbot/core/cron/manager.py`：定时/主动任务
- `astrbot/core/knowledge_base/retrieval/manager.py`：知识库检索

吸收的原则：平台事件先成为不可变事实，再生成可重建的运行时投影；上下文按窗口、游标和预算增量构建；工具循环必须有迭代、调用、结果和重复限制；静态人格与即时状态分开；主动行为先形成候选，再由调度器决定；关键词与语义检索并行并稳定融合。

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
- `go test ./...` 与 `go test -race ./...` 通过。

## 7. 后续演进

1. 增加摘要 checkpoint 和 token 计数器。
2. 将 `ScheduleCandidate` 接到 follow-up/open loop 管理器和可恢复 cron 表。
3. 为工具增加副作用声明与按工具预算。
4. 建立混合检索离线评估集，调节权重和时间衰减。
5. 将 ThoughtRecord、Reflection 和 Memory 接入统一评估流水线，形成“观察 -> 反思 -> 学习 -> 行为”闭环。
