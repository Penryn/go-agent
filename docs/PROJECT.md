# 项目定位与功能

## 定位

go-agent 是长期在线的 QQ AI 群友：拥有稳定的虚拟人格，理解群聊上下文，记住群友和相处经历，自主选择接话、表情、表情包、协助任务或沉默。

产品优先级是自然参与、人格一致、记忆可信和运行可靠。被 @ 也保留沉默权；情绪、精力、关系和群氛围提供参与倾向，权限、冷却、事实和发送校验由运行时执行。

当前部署是一个 Go 进程 `qqbotd`，连接 NapCat 和 PostgreSQL/pgvector。MCP 和 Codex 是群友按需使用的外部能力，文件任务使用按回合隔离的临时工作区。

## 功能地图

| 能力 | 用户看到的行为 | 当前实现 |
| --- | --- | --- |
| QQ 事件接入 | 理解普通群消息、@、点名、引用、图片、视频、文件、戳一戳和群通知 | `adapters/inbound/napcat`、`application/normalizer` |
| 自主参与 | 结合语境决定说话或沉默；受群白名单、冷却和连续发言限制 | `application/presence`、`presence/deliberation`、`presence/reflection` |
| 对话上下文 | 合并短时间分条消息，保留说话人和回复目标相关历史，避免重复接话 | `presence/group_actor`、`application/context`、`application/prompting` |
| 稳定人格 | 保持人物口吻、背景和表达风格，情绪与精力跨群共享 | `domain/persona`、`application/persona`、`application/prompting` |
| 人物事实 | 区分锁定设定、管理员更新、可补全事实和未核实转述；公开自述送达后才落为 Canon | `application/persona/`、`domain/persona` |
| 长期记忆 | 显式记住信息；后台从归档事件增量抽取有证据的记忆；按群和用户可见性检索 | `application/memory`、`application/learning`、`application/retrieval` |
| 相处偏好 | 当前说话人的称呼、禁止 @ 和禁止戳偏好进入统一快照，实际动作校验使用同一份约束 | `learning` → `memories` → `context` → `prompting` / `action` |
| 群友关系 | 更新熟悉度、好感、信任和摩擦；明确关联机器人的互动进入反馈窗口 | `application/profile`、`application/relationship`、`presence/feedback` |
| 群氛围 | 从消息维护活跃度、话题和社交温度，作为模型参考 | `application/scene` |
| 多模态与表情包 | 异步理解媒体、收集和检索已批准表情包、限制重复发送 | `application/multimodal`、`application/meme`、`presence/perception` |
| 表达动作 | 发文字、引用、表情、表情包、戳回、撤回纠正或保持沉默 | `application/tools`、`application/action`、`adapters/outbound/napcat` |
| 外部工具 | 使用获准 MCP 工具；可委派 Codex 执行本地任务并发送产物 | `application/tools`、`application/workspace` |
| 后台可靠性 | 慢任务入 Outbox，按租约领取、重试和死信管理 | `application/runtime/outbox`、`application/runtime/scheduler` |
| 运维后台 | 查看概览、记忆、关系、运行记录、任务、表情包、模型指标和 MCP 配置 | `internal/app/admin`、`web/src/views` |

## 三条使用路径

### 日常相处

群消息先归档并更新工作记忆、画像、关系和场景。通过冷却检查后建立一次上下文快照，模型选择一个最终动作。消息实际送达后，运行时更新反馈、人格和群冷却状态。机器人出站消息进入对话事实，不进入成员画像。

### 记忆与相处偏好

`remember_memory` 接受当前用户明确要求保存的信息。后台学习每两分钟扫描配置白名单群的未处理事件，并通过 Outbox 抽取候选；校验证据作者和群范围后写入正在使用的 `memories` 表。

结构化偏好保留 `predicate`、`normalized_value`、`qualifier` 和有效期。上下文按当前群及说话人读取有效偏好，相同主体、属性和限定项采用证据时间较新的有效记录。称呼用于生成，禁止互动用于发送前校验；约束读取故障会阻止本轮规划，普通记忆检索故障可降级。

### 协助任务

复杂文件或代码任务可进入 `delegate_codex_task`。当前工作区包含隔离的 `input/` 和 `output/`；产物交由 Action 发送，回合结束后清理临时目录。外部工具和写任务受群白名单、用户权限及确认规则限制，具体用法见 [工具与文件任务](./TOOLS.md)。

任务当前依附聊天回合；新群消息或停机可能取消旧回合。后台任务队列页面展示 Outbox 副作用，不代表独立、可跨重启恢复的 Codex 长任务。

## 当前能力边界

- 主动参与是收到群事件后的自主选择；当前没有冷场后独立定时开口的调度器。
- Group Actor 串行更新群状态；模型回合以同群新回合取消旧回合、跨群并发的方式运行，没有全局模型并发槽位。
- 历史只由真实收发事实重建，工具过程、静默计划和未送达文本不成为下一轮聊天历史。
- 相处约束目前按当前说话人加载，不是对群内所有成员的全量权限查询。
- 普通消息发送的幂等仅在进程内；Outbox 不保证 QQ 发送在跨重启后恰好执行一次。
- BM25 目前扫描可见语料，向量检索依赖可用的 Embedding 模型和正确维度；没有向量时可使用词法召回。
- 自动归档只覆盖接入并获准处理的事件，不包括被忽略的元事件、私聊和机器人自身回流。

## 功能演进原则

新增能力先说明它改善了哪一种群友互动，再确定它属于事件感知、上下文、规划、表达或反馈。可靠的后台副作用复用 Outbox，外部能力通过工具运行时接入。人格倾向交给模型，系统执行权限、事实和发送边界。

实现细节见 [项目架构](./ARCHITECTURE.md)，运行与验证见 [开发说明](./DEVELOPMENT.md)，启动方法见 [README](../README.md)。
