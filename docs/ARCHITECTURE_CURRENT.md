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
          -> admission gate（非定向重复消息）
          -> AgentPlanner
              -> Composer 稳定人设指令
              -> 从已归档 inbound / outbound 重建相关对话
              -> 动态上下文预算与按轮工具集
              -> LLM + tools
          -> 将 ReplyPlan.PlannedActions 解析为 Decision.Action
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
- 直接 @、点名或回复机器人不受该重复消息规则拦截；
- mood、energy、关系和群场景只作为模型上下文，不在 Admission Gate 中硬编码参与意愿。

通过闸门后，`AgentPlanner` 才创建工具循环。模型可以选择发文字、引用、表情、
表情包、工具调用或 `stay_silent`。`Deliberation Adapter` 会校验计划动作是否适合
当前触发类型，并把它解析为唯一的 `Decision.Action`；最终动作仍由 `Action Service`、
OutputGuard 和发送适配器控制。

## 上下文与成本边界

模型上下文不再持久化内部 `PromptSession`。每一轮都从 `GroupActor` 和消息归档中的
真实 inbound / outbound 事件确定性重建，因此静默、工具中间结果、发送失败和未送达的
计划不会变成下一轮的“幽灵对话”。短时间同一用户的分条消息会合并理解；历史默认保留
最近 8 条，并额外保留当前说话人的近期消息和显式回复目标。

可变上下文统一按字节预算裁剪：历史 4800、相关记忆 1800、媒体摘要 1800、最近判断
700、单次工具结果 4096。相关记忆按检索顺序保留并按 `memory_id` 去重。工具 schema
也按本轮能力裁剪：`poke_member` 只在被戳时提供，`repair_message` 只在存在可撤回消息时
提供，群工具白名单之外的 schema 不发送给模型。

`model_usage_records.prompt_shape_json` 记录静态指令、历史、当前轮、记忆和工具 schema
的字节分区；同一记录同时保留输入、输出、cached、cache miss 和 uncached token，可在
管理后台按真实供应商回执观察优化效果。

## 事件与画像边界

入站消息会进入成员画像、群场景和关系投影。机器人 outbound 事件只进入群工作记忆、
场景和发送后状态，不再写入成员画像，避免机器人污染自己的活跃度和常用语统计。

## 后续演进边界

角色仍由 `GroupScene.RecommendedRole` 作为上下文倾向提示模型，不独立成可执行的
`PresenceMode` 状态机。模型自主决定参与方式，运行时只执行冷却、权限、工具和安全边界。

发送后的反馈窗口沿 `action_id / decision_id / source_event_ids` 做归因，再幂等更新
关系和群状态；后续只针对可复现的误归因继续收紧规则。
