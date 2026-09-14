# 当前身份与角色运行架构

本文只描述当前生产组装路径。旧的 `socialdecision`、`MessageCoordinator`、
`ContextAssembler`、`ResponsePlanner` 和 `ResponseExecutor` 并行实现已移除。

后续改造清单见 [`AI_GROUP_FRIEND_ARCHITECTURE_PLAN.md`](./AI_GROUP_FRIEND_ARCHITECTURE_PLAN.md)。

## 运行主链路

```text
NapCat / OneBot
  -> Normalizer
  -> Presence Runtime
      -> TurnObserver（冷却、连续发言上限）
      -> GroupActor（事件归档、群内工作记忆）
      -> Profile / Scene / Relationship observers
      -> ContextService.BuildSnapshot
          -> 最近群聊、记忆、成员画像、群场景、关系、策略
          -> PersonaState（全局 mood / energy / talk_bias）
          -> PersonaView（当前有效人物事实）
      -> Deliberation Adapter
          -> admission gate（重复、冷却、权限和安全边界）
          -> AgentPlanner
              -> Composer 稳定人设指令
              -> PromptSession
              -> LLM + tools
      -> Action Service
          -> OutputGuard
          -> 发送 / 撤回 / 表情 / 表情包
          -> 记录 outbound 事件
      -> Canon.AfterDelivery
      -> TurnObserver.AfterTurn
          -> 更新情绪、精力、冷却、关系
```

## 身份层

`PersonaConfig` 是稳定身份定义，包含名字、别名、性格、口吻、场景规则、
回复边界和人物事实规则。`PersonaDefinition` 会将配置编译成不可变规则集，
`PersonaView` 是当前回合可见的有效事实视图。

人物事实通过 Canon 维护：模型只能提议 `self_complete_once` 或
`self_mutable` 槽位；运行时会校验证据、策略和冲突，且只有消息实际发送成功后
才提交事实。未核实的群友转述不会直接变成角色亲身经历。

身份询问也保持角色扮演口吻，不把“是否承认 AI”做成运行时状态或配置项；
但虚拟人物经历仍必须遵守 Canon 事实规则，不能包装成现实世界的可验证事实。

## 动态状态层

当前运行路径使用单一 `ContextSnapshot`，避免决策和生成分别读取状态。

- `PersonaState`：全局人格状态，描述 mood、energy、talk_bias。
- `RuntimeState`：按群保存冷却、最近发言和连续发言计数。
- `GroupScene`：按群保存话题、活跃度、社交温度和推荐角色。
- `SocialRelationship`：按群、按用户保存熟悉度、好感度、信任和摩擦度。
- `MemberProfile`：按群、按用户保存昵称、活跃度和常用表达。

## 参与决策与生成

`TurnObserver` 在模型前执行确定性闸门，负责冷却和连续发言限制。
`Deliberation Adapter` 在上下文快照建立后执行轻量 admission gate：

- 非直接指向机器人的重复消息不再次消耗模型回合；
- 退缩或低精力状态下不主动加入无关对话；
- 直接 @、点名或回复机器人仍保留回应机会。

通过闸门后，`AgentPlanner` 才创建工具循环。模型可以选择发文字、引用、表情、
表情包、工具调用或 `stay_silent`。最终动作仍由 `Action Service`、OutputGuard 和
发送适配器控制。

## 事件与画像边界

入站消息会进入成员画像、群场景和关系投影。机器人 outbound 事件只进入群工作记忆、
场景和发送后状态，不再写入成员画像，避免机器人污染自己的活跃度和常用语统计。

## 后续演进边界

角色仍由 `GroupScene.RecommendedRole` 作为上下文倾向提示模型，不独立成可执行的
`PresenceMode` 状态机。模型自主决定参与方式，运行时只执行冷却、权限、工具和安全边界。

发送后的反馈窗口也应继续沿 `action_id / decision_id / source_event_ids` 做归因，
再幂等更新关系和群状态。
