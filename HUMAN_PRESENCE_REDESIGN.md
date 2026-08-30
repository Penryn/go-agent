# Human Presence Runtime 重构文档

## 目标

项目的目标不是“收到问题后回答”，而是让 Bot 成为一个长期在线的群成员：持续观察群聊，只在有理由时参与；能够等待、合并消息、延后跟进、记住自己说过什么，并根据互动结果调整对群友和话题的理解。

因此，运行时的基本单位不再是“单条消息 Turn”，而是“每个群持续存在的 Presence Actor”。消息是 Actor 的输入，回复只是 Actor 偶尔产生的副作用。

## 当前实现的问题

当前链路为：

```text
消息 → Normalize → BuildSnapshot → Autonomy.Decide → Planner → Send
```

这套链路的问题不是模型数量不足，而是时间模型不正确：

- 每条消息都被当作独立问答回合。
- 直接 @ 几乎等价于立即回复，没有等待和重新判断。
- 普通消息在回复队列满时被丢弃，导致感知和学习语料丢失。
- Vision 和 Agent 调用阻塞同群消息接收。
- ContextSnapshot 是 flat 的最近消息和记忆，没有活跃话题、未完事项和参与者关系。
- Bot 的出站消息依赖平台回流，无法可靠进入自己的对话记忆。
- 回复后的观察、反思和长期学习没有形成实时闭环。

## 新的总体链路

```text
NapCat / OneBot
      ↓
Ingress Gateway
      ↓
Immutable Event Log
      ↓
Group Presence Actor
  ├─ Working Memory
  ├─ Topic State
  ├─ Presence State
  ├─ Open Loops
  └─ Thought Candidates
      ↓
Presence Scheduler
      ↓
Context Projection
      ↓
Deliberation Agent
      ↓
Response Realizer
      ↓
Action Policy / Outbound
      ↓
Self Event
      ↓
Reflection / Learning
```

核心原则：

```text
所有消息都必须被感知，但不是所有消息都值得回复。
```

## 一条群消息的完整生命周期

### 1. Ingress：接收事实

Ingress 只做确定性工作：校验来源、消息 ID 去重、识别 `origin`、分配顺序号，并把事件写入不可变事件日志。

```text
EventRecord {
  EventID
  GroupID
  UserID
  Origin: inbound | outbound | system
  Sequence
  Timestamp
  RawPayload
  Message
}
```

入站队列拥塞时，只能降低后续思考/回复的优先级，不能丢掉事件事实。

### 2. Group Presence Actor：维护每群连续状态

每个群拥有一个长期存活的 Actor。事件、Vision 结果、定时唤醒、发送结果和反思结果都进入同一个 mailbox，由 Actor 串行修改状态。

模型调用不在 Actor 内同步等待；模型调用完成后以异步结果重新投递给 Actor。

### 3. Perception：理解发生了什么

感知不决定是否发言，只更新状态：

- speech act：提问、陈述、吐槽、玩笑、行为请求。
- addressee：消息在和谁说话。
- 情绪和群体气氛。
- 活跃话题归属或话题切换。
- 新的未完成问题和待回应承诺。
- 用户画像和关系证据。
- 图片/视频/表情包的媒体状态。

媒体先生成 `pending` 占位，Vision 后台完成后发送 enrichment event，不阻塞群聊感知。

### 4. Working Memory：形成短中期上下文

```text
GroupWorkingMemory {
  ActiveTopics
  TopicSummaries
  RecentTail
  CurrentBurst
  OpenLoops
  PendingCommitments
  ParticipantAttention
  GroupMood
}
```

同一用户在短时间内的连续消息先合并为一个 burst。消息不再被孤立地交给模型，模型获得的是“群里当前正在发生什么”。

### 5. Thought Candidate：产生候选想法

Perception 后生成零个或多个候选，不立即发送：

```text
ThoughtCandidate {
  CandidateID
  SourceEventIDs
  TopicID
  Addressee
  Intent
  Urgency
  Score
  DueAt
  ExpiresAt
  Uncertainty
  Status: pending | deferred | accepted | cancelled | expired
}
```

候选意图包括：`answer`、`acknowledge`、`ask`、`tease`、`react`、`send_meme`、`follow_up`、`observe_only`。

候选可以等待更多消息、与其他候选合并、因为话题变化而取消，或者在未来时间点唤醒。

### 6. Presence Scheduler：决定何时参与

Presence Scheduler 是确定性运行时，不能由 LLM 接管。它根据候选紧迫度、话题活跃度、群内轮次、Bot 最近存在感、关系、人格状态和认知预算选择候选。

```text
observing → listening → thinking → scheduled → expressing → cooldown
                                      ↘ resting / suppressed
```

被 @ 只提高 urgency，并提供响应 SLA，不再简单等价于强制立即回复。普通候选可以等待几秒，follow-up 候选可以延后，旧候选可以过期。

### 7. Context Projection：生成窄上下文

Deliberation 只接收当前候选需要的投影：

- 当前话题摘要和相关消息 tail。
- open loops 和待回应承诺。
- 当前用户画像及关系状态。
- 有证据的少量长期记忆。
- 媒体 descriptor 及置信度。
- Persona 稳定特征和当前 mood/energy/attention。
- 候选来源、历史和不确定性。

投影明确区分事实、推断、不确定内容和过期内容，不再把宽 `ContextSnapshot` 原样塞给 Agent。

### 8. Deliberation：思考和表达意图

主 Agent 不直接决定是否发言，也不直接调用出站动作。它输出结构化思考结果：

```text
Thought {
  Understanding
  ChosenIntent
  ShouldAct
  Confidence
  EvidenceUsed
  NeedsFollowUp
  ShortRationale
}
```

只保存短 rationale 和审计信息，不保存或展示完整 CoT。Runtime 负责最终的权限、频率、过期和状态校验。

### 9. Response Realizer：实现自然表达

根据 `Thought` 生成 0 到 N 条短气泡，并决定是否引用、发图、poke 或 react。表达应由话题、关系、情绪、人格和历史表达共同决定，固定 fallback 只作为最后的技术兜底。

发送前再次检查候选是否过期、话题版本是否变化、动作是否被群策略允许。

### 10. Self Event：把 Bot 自己纳入对话

发送成功后主动生成 `origin=outbound` 的 EventRecord，更新话题 tail、Presence 状态、cooldown、open loop 和候选状态。不依赖 NapCat 是否回流 Bot 消息；若平台回流，用 EventID 去重。

### 11. Reflection：观察互动结果

回复后延迟观察后续消息：群友是否接住、纠正、继续、忽略或转移话题。反思结果更新 mood、energy、relationship、topic confidence 和未来 presence 偏好。

### 12. Learning：有证据地学习

```text
Event Log
  → Reflection Batch
  → Evidence-backed Candidate
  → Memory Write Gate
  → pending / approved / rejected / superseded
  → Topic Memory / Profile / Long-term Memory
```

学习任务使用每群 durable watermark，只处理上次之后的新事件。长期记忆、群黑话和用户画像必须带 evidence event IDs，并经过去重、冲突检测、TTL 和审核。

## 新模块

```text
internal/humanbot/
  domain/
    event
    group
    topic
    presence
    thought
    memory
    relationship
  runtime/
    ingress
    group_actor
    perception
    candidate
    scheduler
    deliberation
    realization
    reflection
  adapters/
    onebot
    mysql
    redis
    qdrant
    model
  bootstrap/
```

最重要的 deep module 是 `Group Presence Actor`。它隐藏事件顺序、burst 合并、状态修改、候选过期、模型回调和发送后的写回；外部只提交事件或异步结果。

## 实施顺序

这不是迁移计划，而是新实现的垂直切片顺序：

1. 实现 Event Log、EventRecord 和 Group Actor mailbox。
2. 实现 Working Memory、burst 合并、Topic State 和 open loops。
3. 实现 Thought Candidate、due/expiry 和 Presence Scheduler。
4. 接入窄 Context Projection 和 Deliberation Agent。
5. 实现 Response Realizer、Action Policy 和 outbound self event。
6. 实现 Reflection、Curator、Learning watermark 和审核发布。
7. 最后接入 Vision、表情包、向量记忆和跨群长期能力。

每一步都应能在不启动真实 NapCat 和基础设施的情况下通过事件回放测试。

## 验收标准

- 普通消息始终进入 Event Log，即使没有产生回复。
- 连续短消息会被合并为一个语义 burst。
- Bot 可以等待、延迟、取消和重新评估候选。
- 被 @ 不会机械地产生相同回复。
- Vision 慢时，群仍能继续感知新消息。
- Bot 自己的发言能进入下一轮上下文。
- 回复后能根据群友反应更新状态。
- 长期记忆和学习结果都能追溯到证据事件。
- 主链路只有一个 Presence Runtime interface，完整行为可通过它测试。
