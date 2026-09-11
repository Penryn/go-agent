# 社交角色架构重构方案

状态标记：`[已完成]` 表示已接入运行时并通过测试；`[部分完成]` 表示已有可运行基础实现但未达到目标边界；`[未开始]` 表示仅保留目标设计。

本文中的状态以当前工作区代码为准。`[已完成]` 和 `[部分完成]` 是已经发生的代码变更；`[未开始]` 和 `[目标]` 只是后续方案，不代表当前系统已经具备对应能力。

## 实施状态

| 能力 | 状态 | 当前实现 |
| --- | --- | --- |
| 群场景投影 | [已完成] | `GroupScene` 持久化到 `group_scenes`，入站和成功出站事件都会更新场景。 |
| 关系事件与投影 | [已完成] | `relationship_events` 是证据来源，`relationships` 是事务内更新的当前投影。 |
| 用户记忆 | [已完成] | `remember_memory` 将用户明确要求保存的信息直接写入 `memories`；来源事件由运行时绑定。 |
| 社交上下文 | [已完成] | `ContextSnapshot` 和 Prompt 已消费关系投影与群场景。 |
| 角色三层模型 | [部分完成] | 稳定身份和 Persona Canon 已有；群姿态、按群即时状态和 `PersonaContext` 尚未拆出。 |
| 社交决策引擎 | [已完成] | `DecisionEngine` 基于社交上下文评估是否响应；候选系统已移除。 |
| 结构化 ResponsePlan | [已完成] | `ResponsePlanner` 生成结构化回复计划，包含动作类型、内容和元数据。 |
| 发送后反馈窗口 | [未开始] | 已记录 `direct_reply` 关系事件，尚未等待后续群消息并分类反馈。 |
| 管理后台事件视图 | [部分完成] | 关系页已展示 trust/friction，尚未展示关系事件和投影原因。 |

## 已落地改动清单

以下内容已经修改代码并接入当前运行时：

| 状态 | 已修改位置 | 已落地内容 |
| --- | --- | --- |
| [已完成] | `schema/schema.sql`、`internal/adapters/storage/postgres/social_repository.go` | 新增 `group_scenes`、`relationship_events`、`relationships` 的持久化与读取。 |
| [已完成] | `internal/application/scene` | 增加 `GroupScene` 投影；入站事件和成功出站事件都会推进群场景。 |
| [已完成] | `internal/application/relationship` | 以关系事件作为证据，在同一 PostgreSQL 事务内更新关系投影，并处理幂等和事务锁。 |
| [已完成] | `internal/application/memory`、`internal/application/tools` | 增加 `remember_memory` 直写权威记忆；增加 `record_relationship_signal`；删除旧的 claim 暂存链路。 |
| [已完成] | `internal/application/context`、`internal/application/prompting` | `ContextSnapshot` 和 Prompt 已消费群场景、关系投影及 `trust`/`friction` 等社交上下文。 |
| [部分完成] | `internal/app/admin.go`、`web/src/views/RelationsView.vue` | 关系后台已切换到 `trust`、`friction` 等新投影字段；关系事件和投影原因页面尚未完成。 |
| [已验证] | Go 与前端构建链路 | `go test ./...`、`go vet ./...`、`git diff --check`、`npm run build` 已通过。 |

## 1. 重新定义项目

项目不是通用 Agent，也不是问答机器人，而是一个长期存在于 QQ 群中的社交角色：

```text
感知群聊 -> 理解场景 -> 评估关系 -> 决定是否参与 -> 选择动作 -> 观察反馈 -> 更新状态
```

核心目标按优先级排列：

1. 像一个真实群友一样控制出现频率。
2. 对不同群、不同成员形成稳定但可变化的关系。
3. 保持角色连续性，不因模型切换或进程重启失忆。
4. 回复内容服务于社交目标，而不是单纯完成用户指令。
5. 所有状态变化都有证据、来源和可解释结果。

因此，架构中心应从 `Agent + Tools` 改为 `Social Character Runtime`。

## 2. 目标架构 [目标，未完全落地]

```text
OneBot Event
    |
    v
Conversation Ingestor
    |  immutable conversation facts
    v
Group Actor
    |  ordered group events
    v
Scene Projector
    |  current group scene
    v
Social Decision Engine <---- Persona Runtime
    |                         |
    |                         +-- stable identity
    |                         +-- group posture
    |                         +-- current affect
    v
Response Planner
    |  speech/action intent
    v
Action Guard -> OneBot Sender
    |
    v
Feedback Collector
    |
    +--> Relationship Projector
    +--> Memory Projector
    +--> Persona State Projector
    +--> Evaluation / Trace
```

部署形态继续使用单个 Go 进程和 PostgreSQL。当前的 Group Actor、Outbox、pgvector 和管理后台可以继续作为基础设施，但不再作为业务中心。

## 3. 模块边界 [部分完成]

```text
internal/domain/
  conversation/   不可变消息事实
  scene/           群场景和话题状态
  relationship/    关系事件和关系投影
  memory/          权威记忆写入和检索对象
  persona/         稳定人格、群姿态和即时状态
  presence/        参与候选和社交决策
  action/          回复动作和发送结果
  feedback/        发送后的互动反馈

internal/application/
  ingest/          事件归一化、去重、落库
  scene/           Group Actor 和场景投影
  socialdecision/  是否参与、目标和社交目的
  response/        回复内容和动作规划
  reflection/      反馈收集、状态更新
  memory/          明确记忆写入和检索
  relationship/    关系事件写入和投影
  persona/         人格装配和状态转换
  projection/      Outbox projector runtime
  evaluation/      决策、结果和质量指标

internal/adapters/
  napcat/          OneBot 入站和出站
  postgres/        权威事实和投影
  model/            模型供应商
```

`scene`、`relationship` 和 `memory` 已按此边界落地；现有 `tools` 仍是过渡层，明确记忆请求和关系信号已由应用层校验，完整的结构化决策将在 `ResponsePlan` 阶段替换终结工具编排。

## 4. 运行时链路 [部分完成]

### 4.1 事件接入 [部分完成]

`ConversationEvent` 只表示平台事实，不包含模型判断和业务推断。

接入流程：

```text
OneBot payload
  -> normalize
  -> deduplicate
  -> conversation_events
  -> group actor
```

入站事件已先写入持久化消息事实，再进入 Group Actor。成功发送的 Bot 消息也会归档并更新群场景；统一 `conversation_events` 表仍是后续收敛项。

### 4.2 Group Actor [部分完成]

每个群一个 Actor，同群事件严格按序处理，跨群并发。

Actor 只维护短期可重建状态：

```text
recent_events
current_burst
active_topic
open_loops
recent_speakers
pending_candidates
scene_revision
```

关系最终值已移出 Actor；工作记忆仍承载部分话题和候选调度状态，尚未完全收敛为纯短期投影。

### 4.3 群场景 [已完成：基础投影]

新增 `GroupScene`，替代把群状态分散在 `GroupWorkingMemory`、`RuntimeState` 和 Prompt 字段中的做法：

```go
type GroupScene struct {
    GroupID             int64
    ActivityLevel       float64
    SocialTemperature   float64
    CurrentTopic        string
    OpenLoops           []string
    ActiveSpeakers      []int64
    Audience            []int64
    ConflictLevel       float64
    BotReception        string
    RecommendedRole     string
    LastHumanMessageAt  time.Time
    LastBotMessageAt    time.Time
    Revision             int64
}
```

`RecommendedRole` 只取有限值：

```text
observer / participant / helper / joker / moderator / withdrawn
```

场景由规则和事件投影产生。模型可以辅助识别主题和情绪，但不能直接写入最终场景。

## 5. 社交决策 [未开始]

社交决策和回复生成必须分开。

```go
type ParticipationDecision struct {
    DecisionID       string
    GroupID          int64
    TriggerEventID   string
    Participate      bool
    TargetUserID     int64
    Audience         string
    Intent            string
    ReasonCode        string
    SocialValue      float64
    InterruptionRisk  float64
    ExpiresAt         time.Time
}
```

决策顺序：

1. 硬规则过滤：黑名单、冷却、连续发言上限、事件过期、权限。
2. 场景判断：是否有人需要回应，群是否正在快速对话，是否存在未闭环话题。
3. 关系判断：当前目标用户与角色的关系是否允许调侃、追问或主动靠近。
4. 人格判断：当前群姿态和精力是否适合参与。
5. 模型判断：只在规则允许后选择社交目的和动作。

模型可以选择 `stay_silent`，但不能绕过前四步直接发送。

## 6. 角色系统 [部分完成]

稳定身份已经落地；群姿态和按群即时状态仍是目标设计。

### 6.1 稳定身份 [已完成]

全局共享，变化慢：

```text
name
background
traits
values
speech_style
constraints
canonical_facts
```

现有 Persona Canon 的追加事实、来源、冲突和成功发送后提交机制可以保留，但归入 `persona_facts`，不再和即时情绪共用状态表。

### 6.2 群姿态 [未开始]

按群隔离：

```text
familiarity
participation_bias
humor_level
helpfulness_bias
formality
trust_in_group
preferred_topics
```

这表示“我在这个群里是什么状态”，不能放到全局 PersonaState。

### 6.3 即时状态 [部分完成]

按群隔离并自然衰减：

```text
mood
energy
social_patience
last_trigger
expires_at
```

当前即时 mood/energy 仍以全局 `PersonaState` 保存；按群状态和 `PersonaContext` 组装尚未实现。

## 7. 关系系统 [已完成：事件和基础投影]

关系不再由模型直接修改最终分数。

### 7.1 关系事件

新增 `relationship_events`：

```text
event_id
persona_id
group_id
user_id
kind
valence
intensity
reason
evidence_event_id
source_decision_id
created_at
```

事件类型先控制在以下范围：

```text
message_received
direct_reply
positive_feedback
negative_feedback
help_given
help_received
user_correction
user_teasing
user_rejected_teasing
bot_overtalked
conversation_continued
conversation_dropped
```

### 7.2 关系投影

`relationships` 只保存当前投影，不是唯一事实来源：

```text
familiarity
affinity
trust
tease_tolerance
friction
last_interact_at
revision
```

更新规则集中在 `relationship.Service`：

```text
familiarity = 互动证据累计并缓慢趋近上限
affinity = 正负互动加权并按时间衰减
trust = 承诺兑现、帮助和纠正结果驱动
tease_tolerance = 明确接受或拒绝调侃驱动
friction = 冲突增加、时间衰减
```

最终值只能由关系服务根据事件产生。模型只能提交关系观察，不能提交最终分数。

## 8. 记忆系统 [部分完成]

记忆分为四类：

```text
episodic      某次具体互动
semantic      稳定事实和偏好
social        群文化、关系模式和互动边界
persona       Bot 自己的连续性事实
```

### 8.1 用户明确记忆

用户明确要求保存信息时，模型调用 `remember_memory`：

```text
type
content
```

服务端根据当前群、当前用户和触发事件补齐：

```text
memory_id
scope = group:<group_id>:user:<user_id>
subject = <user_id>
source_event_id = 当前触发事件
origin = user_explicit
confidence = 1
importance = 1
```

信息直接进入 `memories`，并通过已有 outbox 同步向量索引；稳定哈希保证重复请求幂等。

低风险且重复出现的群文化可以自动确认；涉及个人身份、隐私、敏感属性的内容必须提高阈值或只保留短期观察。

### 8.2 记忆写入规则

- 没有当前触发事件，不写入记忆。
- 普通聊天中的新信息不主动保存，只有明确记忆请求才调用工具。
- 同一用户重复请求相同内容时幂等覆盖，不生成重复记忆。
- 所有记忆都有 scope、confidence 和有效期。
- 向量索引和 BM25 只是 projection，不是事实来源。

### 8.3 记忆检索结果

检索返回 `MemoryBundle`，而不是裸列表：

```text
target_user_facts
group_culture
current_topic_history
interaction_boundaries
relevant_episodes
```

每个结果附带 `memory_id`、来源、置信度、有效期和 scope，Prompt 只接收经过预算裁剪的 bundle。

## 9. 反馈与反思 [未开始]

发送成功不等于社交成功。

发送后创建反馈窗口：

```text
action_sent
  -> observe next N events or T seconds
  -> classify feedback
  -> append relationship events
  -> update group scene
  -> update authoritative memories when explicitly requested
```

反馈信号包括：

```text
被继续回复、被引用、被认可、被追问
被纠正、被要求停止、无人接话、重复打扰
```

`thought_records` 改为 `decision_records`，只记录：

```text
输入摘要、决策、动作、规则命中、模型版本、结果、反馈事件
```

不保存原始思维链。

## 10. 数据库目标模型 [部分完成]

保留：

```text
conversation_events
messages                  可由 conversation_events 替代或合并
group_scenes
persona_definitions
persona_facts
group_persona_states
member_profiles
relationship_events
relationships
memories
memory_vectors
actions
action_feedback
async_outbox
decision_records
retrieval_traces
model_usage_records
```

删除或合并：

```text
runtime_states             拆为 group_scenes 和 group_persona_states
group_working_memory       只保留为 Actor projection
learning_candidates        仅作为后台提炼候选
thought_records            改为 decision_records
```

权威事实和投影的关系：

```text
conversation_events
relationship_events
memories
persona_fact_events
action_feedback
        |
        +--> current projections
        +--> search indexes
        +--> admin views
```

## 11. 代码重写顺序

允许破坏旧接口时，按以下顺序重写：

1. [部分完成] 重写 domain 类型：`scene`、`relationship`、`memory` 已完成，`persona context`、`feedback` 待完成。
2. [已完成] 重写 schema 和 PostgreSQL repository：新增群场景、关系事件，记忆直接使用权威 `memories` 表。
3. [部分完成] 重写 `Group Actor`，当前仍保留候选调度和话题状态。
4. [未开始] 重写 `socialdecision`，移出 Runtime 中的主动开口和回复判断。
5. [未开始] 重写 `response`，让模型输出结构化 `ResponsePlan`。
6. [未开始] 重写 `reflection`，引入发送后的反馈窗口。
7. [部分完成] 重写管理后台和配置：关系页字段已更新，事件和原因视图待补。

不建议先改 Prompt。Prompt 只是最后消费 `PersonaContext`、`GroupScene`、`RelationshipContext` 和 `MemoryBundle` 的适配层。

## 12. 明确不做

- 不拆微服务。
- 不引入图数据库。
- 不引入独立 Agent 编排平台。
- 不让模型直接操作数据库。
- 不把所有消息都向量化后当作长期记忆。
- 不用更多人格字段掩盖关系模型缺失。
- 不在检索质量未经真实数据验证前引入 reranker。

已落地的范围以“实施状态”和“已落地改动清单”为准。下一阶段是 `socialdecision`、结构化 `ResponsePlan` 和发送后的反馈窗口。

验收标准不是“模型能调用更多工具”，而是：

```text
同一群内行为连贯
不同群之间状态不串
关系变化可解释
记忆有证据且能过期
发送后能根据反馈修正行为
进程重启后状态可恢复
```
