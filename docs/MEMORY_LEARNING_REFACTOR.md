# 记忆与学习系统重构方案

状态：`[修订方案，未实施]`。2026-09-10 根据项目定位、当前调用链和同类项目调研收敛首版范围；本次只修改文档，不修改运行代码或数据库。

核对基线：2026-09-10 当前工作区。以下代码结论来自静态检查，不代表线上效果验证；未重新运行应用测试，也不沿用上一版“4 个包 22 项通过”作为当前验收依据。

目标是让长期生活在 QQ 群中的社交角色记住相处中有用的内容：**少量必须遵守的人物设置 + 带来源的事实和经历 + 一个后台提炼流程**。继续使用单进程 Go、PostgreSQL、Outbox、BM25 和 pgvector。

相较上一版，取消“声明实体 → 审核任务 → 有效记忆投影”的双重生命周期，改为一份权威记忆数据；把沉默学习、个人边界、纠错和遗忘纳入首版闭环；完整动作反馈学习后移。保留证据、主体隔离、事务写入和重试去重要求。

重构仍直接替换旧学习逻辑：不保留旧字段别名、旧工具参数、双写、旧表读取回退或自动兼容分支。能力可以分批交付，每次上线只运行一套记忆逻辑。旧自动学习结果默认不作为可信记忆导入，有价值的内容从原始证据重新提炼。

## 1. 首版要改变的行为

| 场景 | 期望行为 |
| --- | --- |
| 群友表达自己的偏好 | 下一次相关对话能自然记起，知道是谁、何时、在哪个群说的。 |
| 群友要求换称呼、停止某种互动 | 当前回合及时处理，后续行动持续遵守；临时边界按明确时间失效。 |
| Bot 沉默期间出现有价值的信息 | 后台仍能学习，不依赖回复模型调用记忆工具。 |
| 发生一次有意义的讨论 | 保存参与者、经过和已知结果，区分 Bot 参与过和仅观察到。 |
| 对方更正、偏好改变或要求忘记 | 当前事实与缓存及时更新；历史任务不能把旧内容重新学回。 |
| 没有值得长期保存的信息 | 输出空列表，不为刷屏、热词或每条消息生成记忆。 |

首版学习有来源的事实和明确互动偏好。无人接话、发送成功、检索命中和群聊热闹程度，不自动成为“对方讨厌我”“大家喜欢这种回复”等长期判断。模型训练、自动修改系统规则和扩充工具权限不属于本方案。

## 2. 当前实现与需要修复的缺口

当前真实回复链路已经接入模型和记忆读取，不能再描述成模板回复：

```text
App → Group Actor.decideAndRespond → DecisionEngine
      → Persona ContextAssembler → ResponsePlanner
      → Composer.ComposeResponse
          → RetrieveRelevant(groupID, evt.Text, 5)
          → buildResponsePrompt → LLM.Generate
      → ResponseExecutor → Sender
      → 新 WindowManager + 旧 FeedbackCollector 两条反馈路径
```

[app.go](/Users/admin/Desktop/go-agent/internal/app/app.go:120) 已注入 LLM 和 MemoryRetriever；[composer.go](/Users/admin/Desktop/go-agent/internal/application/prompting/composer.go:560) 已执行检索和模型调用。当前任务是补齐上下文与读写契约，不是重新创建模型入口。

后台仍是“每 6 小时读取 200 条 → n-gram/行为词/ThoughtRecord → learning_candidates → confidence >= 0.7 → MarkIntent”。在线 `stage_memory_claim` 已注册，但 App 创建 AgentPlanner 后未使用，当前生成入口没有调用该工具的模型循环；ClaimService 也没有形成确认与消费闭环。

| 当前证据 | 影响与修复方向 |
| --- | --- |
| [learning/service.go](/Users/admin/Desktop/go-agent/internal/application/learning/service.go:228) 晋升 MemoryID 只包含群、类型和值，不包含 TargetUserID；存储 Upsert 会覆盖 scope。 | 同群不同成员可能共用记忆身份。以独立主体建模，删除旧直接晋升入口。 |
| 同一学习器用行为词推导群级候选，并从 ThoughtRecord 抽取可晋升的 behavior_feedback。 | 个人原话可能被放大为全群规则，技术结果可能被当作学习证据。替换为窗口结构化提炼和原始证据校验。 |
| [app.go](/Users/admin/Desktop/go-agent/internal/app/app.go:555) 的检索适配器不传 UserID；[scope.go](/Users/admin/Desktop/go-agent/internal/search/scope.go:6) 要求非零 UserID 才能读用户级 scope。 | 当前生成路径无法召回用户级 scope 记忆。目标契约分开主体集合与群内可见范围，不能只补一个当前发言人 ID。 |
| [composer.go](/Users/admin/Desktop/go-agent/internal/application/prompting/composer.go:600) 只写入 mem.Content 和最新消息，不使用传入的 personaCtx。 | 主体、来源、时间、完整对话和解析后人格视图没有交给模型。扩展当前真实生成入口。 |
| [social_repository.go](/Users/admin/Desktop/go-agent/internal/adapters/storage/postgres/social_repository.go:171) 重复 Stage 会覆盖证据数组和状态。 | 新证据只追加去重，不得复活已替代或撤销的记录。旧 memory_claims 退出运行。 |
| 学习固定读取 200 条且不续跑，水位按发生时间推进；[store.go](/Users/admin/Desktop/go-agent/internal/adapters/storage/postgres/store.go) 的替代字段未构成统一召回排除条件。 | 需要可靠追赶、晚到补偿和所有入口一致的有效性过滤。 |
| [actor.go](/Users/admin/Desktop/go-agent/internal/application/presence/group_actor/actor.go:450) 对入站事件独立启动后台决策，不能仅凭 Actor 名称认为整个模型和发送阶段已串行。 | 收敛同群动作提交顺序，发送前检查新到的停止请求和记忆版本。 |
| [response_executor.go](/Users/admin/Desktop/go-agent/internal/application/presence/planning/response_executor.go:71) 返回平台消息 ID，未检查 receipt.Sent，也未完成成功出站归档。 | 补完整回执、出站事实、输出检查和 Canon 发送后提交；部分成功只记录实际成功动作。 |

已有的混合检索、RRF、trace、记忆与向量任务事务写入、向量 revision 检查，以及 Persona Canon 的事实裁决代码继续复用。修复真实调用方，不建立第二套回复或发送系统。

### 2.1 反馈现状单独核对

[adapters.go](/Users/admin/Desktop/go-agent/internal/app/adapters.go:67) 已改为查询发送后的 `(since, since + duration)`，上一版“读取发送前窗口”的结论已过时。但它仍从固定数量的最近消息中筛选，缺少完整正序分页。

[actor.go](/Users/admin/Desktop/go-agent/internal/application/presence/group_actor/actor.go:758) 同时启用新 WindowManager 与旧 Collector。旧路径仍 Sleep 后丢弃结果；[旧分类器](/Users/admin/Desktop/go-agent/internal/application/reflection/feedback.go:106) 根据事件数量假定正向反馈；[新窗口](/Users/admin/Desktop/go-agent/internal/application/presence/feedback/window.go:110) 仍将时间接近的消息视为潜在响应，并尝试写入关系事件。不能概括成“全部反馈都未消费”，也不能视为可靠归因。

首版记忆不消费这些启发式结果。发送事实的正确性属于首版前置要求，完整反馈窗口、关系学习和状态更新属于后续独立工作；它们不阻塞普通自述的记忆、更正与遗忘。

## 3. 同类项目与本项目的选择

调研日期：2026-09-10。下表依据公开文档和项目说明，用于比较设计取舍；未在本项目部署这些系统或对它们进行统一效果评测。

| 项目 | 公开方案 | 采用的思路与边界 |
| --- | --- | --- |
| MaiBot | A_Memorix 包括人物画像、经历、知识图谱、来源管理、自动写回和反馈纠正。[官方说明](https://docs.mai-mai.org/en/manual/features/memory-system) | 社交群聊场景最接近，参考其记忆内容与使用时机；完整功能范围更大，不整套照搬。 |
| AstrBot 社区 Memoria 插件 | SQLite 单库、候选记忆、空闲后台整理，以及搜索、确认和遗忘操作。[项目说明](https://github.com/fttawa/astrbot_plugin_memoria) | 候选与整理可在一个小服务内完成；不复制跨群画像、衰减阈值和全部类型。社区实现只作结构参考。 |
| Letta | 当前 Agent SDK 将 system/ 下的记忆每轮放入上下文，其他内容按需读取，并支持后台整理。[官方文档](https://docs.letta.com/agent-sdk/memory) | 借鉴“少量必读 + 按需回忆”的读取分层；本项目在 PostgreSQL 上实现，无需引入文件、Git 或额外 Agent 运行体系。 |
| Mem0 | 提供抽取、保存、搜索与显式更新；当前 add 文档描述追加流程，update 单独处理修改。[添加](https://docs.mem0.ai/core-concepts/memory-operations/add)、[更新](https://docs.mem0.ai/core-concepts/memory-operations/update) | 借鉴结构化提炼与小接口；群聊主体、转述、更正权限和 Canon 仍由本项目负责，不假定 add 自动完成冲突裁决。 |

本项目选择保留现有基础设施，缩减业务生命周期。没有评测证据支持为了记忆能力整体更换框架；也不因其他项目提供更多功能就将其全部列为首版必需。

## 4. 一份权威记忆，按用途读取

```text
已归档群消息 → 后台窗口提炼 ─┐
明确更正/遗忘/互动设置 ─────┴→ MemoryService 校验与应用
                                → memories + evidence + changes
                                → Outbox 更新向量

行动前：精确读取称呼与互动边界 → 决策/发送检查
参与后：检索相关事实、群文化和经历 → 完整上下文 → 当前回复模型
角色自身事实：Persona Canon → 解析后的 PersonaContext
```

| 模块 | 职责 |
| --- | --- |
| application/learning | 从归档窗口提出候选和更新意图，控制批次与成本；不决定数据库状态。 |
| application/memory | 唯一记忆写入方，校验证据、应用更新、管理有效性，提供约束与回忆读取。 |
| application/retrieval | BM25/vector/RRF 与检索 trace；不裁决人物事实或关系分数。 |
| application/persona | 拥有角色设定、Canon 和即时角色状态；提供保留来源的解析视图。 |
| application/presence、socialdecision | 组装本轮上下文、读取约束、决定参与，并统一提交动作。 |
| application/reflection、relationship | 后续处理有归因的动作效果与关系投影；不重复拥有群友偏好。 |

“模型提出、服务裁决”是职责边界，不要求建立独立 Claim 持久化实体或确认 worker。新候选可直接经校验写入 active；值得等待补充证据的少量候选才保存为 pending，使用相同 memory_id 和生命周期。

通用记忆仍用 semantic、social、episodic 三种类型及少量 subtype，不为类型建立独立服务或数据库。稳定称呼和互动边界从有效记忆精确读取；普通兴趣、群梗、故事按相关性召回。人物摘要如以后确有需要，从同一有效记忆集合派生，不能成为另一份可独立修改的画像真值。

角色稳定背景和自述槽位由 Canon 拥有，不复制为通用 persona 记忆；Bot 参与过的群聊事件仍可保存为 episodic。短期话题、burst 和未完成对话沿用 Group Actor/GroupScene，不为每条消息创建长期摘要。

## 5. 记忆身份与来源契约

### 5.1 主体、范围和参与者

- scope 表示可见边界，subject_kind/subject_id 表示内容说的是谁。默认范围为来源群，不自动拼接跨群画像。
- Runtime 根据当前群、可见成员、明确提及和引用原消息解析目标 ID。A 可以询问 B 在同群公开表达过的偏好；歧义昵称不能凭模型猜测绑定账号。
- A 转述“B 喜欢咖啡”，来源是 A，候选主体是 B；不能写成 B 本人确认。群内可见也不等于适合在任意话题主动公开提及。
- episodic 的主体为该群，另存去重的 participant_ids 和 bot_role（participant/observer）。按成员查经历时匹配参与者，不只匹配单一主体。Bot 参与身份必须有成功出站或其他可信动作事实支持。

| 字段组 | 首版内容 |
| --- | --- |
| 身份 | memory_id、scope、subject_kind、subject_id、type、subtype |
| 内容 | content；需要唯一值或执行约束时另有受限 predicate、规范化 value、qualifier |
| 经历 | episodic 使用 participant_ids、bot_role、事件时间与稳定原事件锚点 |
| 状态 | status、revision、可选 supersedes_id |
| 时间 | first_observed_at、last_observed_at、可选 valid_until；pending 可有 pending_until |
| 来源 | source_kind、extractor_version；原始证据由 memory_evidence 唯一持有，可关联实际 action_id |

读取接口可返回证据列表和决定原因，但不在多张表重复保存证据数组与计数。模型自报 confidence 不作为自动晋升阈值，使用频率不作为真实性证据。

### 5.2 有限事实键与更新

只对结构化事实建立事实键：`scope + subject_kind + subject_id + predicate + normalized_qualifier`。允许的 predicate 与值类型由代码定义；首版包括 preferred_name（字符串）、allow_poke/allow_mention（布尔）和 likes_topic（集合项）。普通经历与观察使用自然语言，不强迫进入无限扩展的键值体系。

qualifier 首版只支持 default 与明确的绝对时间区间。区间端点统一时区和精度后编码；preferred_name 只支持 default。有效截止时间由已校验区间推导时不能再独立填写另一份矛盾时间。“晚上”“最近”等模糊描述只作软提示或等待澄清，不能自行猜成可执行时段。适用的互动边界重叠时拒绝优先，临时允许不能隐式解除长期拒绝；解除需要本人明确更正对应边界。

单值事实对当前 active fact_key 唯一，集合项按 active 的 fact_key/value 去重。“喜欢茶”和“喜欢咖啡”可共存；“不再喜欢咖啡”只撤下对应项。“改叫阿岚”创建新有效版本并使旧称呼 superseded。

memory_id 标识具体断言，不能对 fact_key/value 设置终身唯一。偏好 A→B→A 时，新的本人证据可以生成新的有效断言；旧证据重放不能恢复历史版本。任务幂等与证据去重独立于这个业务身份，提炼版本变化也不增加独立证据量。

episodic 以稳定原事件锚点识别经历，参与者可以随新增证据补充，不能因为参与者列表变化而换一个身份。提炼窗口不是经历主键；重叠窗口仅增补已匹配经历的证据。不能可靠判断同一事件时保持分开，不强行合并同群同时发生的不同话题。

### 5.3 证据校验与准入

MemoryService 从归档读取发言人、群、时间和引用对象，校验证据存在且在允许范围内；在线输入还必须来自本轮实际上下文。模型不能自行扩大 scope 或提供未经核对的用户身份。

| 信息 | 默认处理 |
| --- | --- |
| 本人明确的称呼、互动设置、普通偏好 | 对象、语义与权限通过校验后可直接 active，不经过额外审核任务。 |
| 玩笑、引用、否定或主体不清 | 无保存价值则丢弃；有价值且可等待补证时才 pending。 |
| 群梗解释 | 可保存“某成员这样解释”的有来源观察；推导全群惯例需要跨成员、跨场景证据，首版不设机械次数晋升规则。 |
| 第三人评价 | 不自动形成他人的稳定画像，有价值时只保存转述或事件。 |
| 有意义的讨论或共同经历 | 根据原始事件确认客观经过和已知结果；不要求完整情绪反馈系统，不推导稳定性格或好感。 |
| 健康、住址、账号秘密等信息 | 不进入默认自动长期学习，沿用明确的产品权限与保留策略。 |
| 角色设定、群治理规则、工具权限 | 走 Canon 或既有管理权限，不从群内热词获得授权。 |

重复事件、Bot 复述、模型摘要和复制刷屏不算新增独立确认。active 表示达到当前保存策略，不把“本人自述”升级为外部验证事实。图片描述、联网结果仍是有来源的派生材料。

## 6. 单一生命周期与及时纠错

```text
新候选 ──来源清晰、策略通过──> active
   └──值得补证────────────> pending ──补证通过──> active
                               ├──否决──> rejected
                               └──到期──> expired
active ──更正──> superseded
active ──时间失效──> expired
active/pending ──明确忘记──> revoked
```

唯一当前状态在 memories。memory_changes 只追加发生过的修改、原因、操作者、时间及必要字段差异，不维护另一份当前事实；memory_vectors 是可重建投影。普通证据追加不得把 rejected、superseded 或 revoked 改回 pending/active。pending 到期可由普通维护任务处理，无需为每条记忆创建审核流程。

一次 Apply 在短 PostgreSQL 事务内完成：核对操作幂等与证据 → 锁定相关事实、校验预期 revision → 写记忆及去重证据 → 替代旧记录、追加变更记录 → 投递索引/清理任务。不存在的单值槽位也通过唯一约束协调并发。模型提炼在事务外完成，遇到状态冲突重新读取并判断，不按任务完成时间覆盖较新事实。

本人明确更正优先于一般学习；第三人不能修改他人的个人设置。新证据比当前事实更早、顺序不明或来源强度相当却冲突时，保留争议，不采用最后写入者胜出。

明确停止请求在当前回合先约束动作，随后通过同一服务持久化；不等待后台窗口或完整 ActionFeedback。“你说错了”需要解析所指事实，无法归因时不能撤销整份召回结果。及时路径只处理本次明确更新，不把整条消息标记为后台已提炼，以免漏掉其中其他信息。

## 7. 一个后台提炼流程与可靠追赶

### 7.1 按窗口提炼，沉默也学习

后台从已归档消息读取有限窗口，输入原始发言人、时间、引用关系、参与者及必要的已有记忆，输出候选、证据 ID 和新增/补充/更正意图。结构化提炼复用现有模型接入，删除 n-gram、topicSuffixes、behaviorSignal 和 ThoughtRecord 晋升逻辑。

初始预算可使用最多 60 条待处理消息、15 分钟邻近上下文、空闲 2 分钟闭合；它们是待回放校准的起点。消息数量或等待时间达到上限也要处理，持续活跃的群不能无限等待空闲。跨窗口引用补回原消息，有限重叠只作上下文；并行话题不要合成一段经历。

Bot 是否参与回复不影响人类消息学习。Bot 自述需成功发送并经 Canon 校验；已归档的成功出站可支撑客观共同经历。是否受欢迎等效果判断仍等待后续有归因的反馈能力。

空结果是正常成功。模型失败、超时或结构不合法时重试，不当作空结果。限制单群调用量、总 token 和任务时间，前台回复优先；超过预算后续跑，失败或死信保持可补偿状态。

### 7.2 首版采用事件处理记录，不重建归档顺序

采用固定 event IDs 的 Outbox 批任务和 learning_event_progress，不再用 occurred_at 高水位判断此前是否全部处理。该表以 `(event_id, extractor_version)` 唯一记录提炼完成或按明确策略跳过及其原因，不表示该消息一定产生 active 记忆；读取失败不属于可跳过结果。

1. 周期扫描已提交且缺少完成记录的归档事件，在单群预算内选取批次；将待处理 IDs、提炼版本和必要上下文 IDs 固定在任务中，payload 不复制消息正文。扫描避开已有在途批次，不用新任务绕过原任务的退避和死信状态；失败批次保留显式重试入口，其他事件仍可继续处理。
2. Worker 在执行时重新核对证据与遗忘标记，读取上下文后调用模型。已处理的重叠消息可以供理解，但不能重复计入新证据；被禁止重学的原事件也从补回引用与重叠上下文中排除，并记录策略跳过，避免不断重新调度。
3. 保存结果时在同一事务内校验进度唯一键与记忆 revision，应用记忆、证据和变更，写完成记录，投递向量任务。事务失败则全部回滚；有效空结果也要写完成记录。
4. 重试若发现全部事件已完成可直接结束；部分事件已被其他任务处理时重新组装剩余批次，不提交基于旧状态生成的整批结果。多 worker 争抢由唯一约束和 revision 检查兜底。
5. 后续扫描继续选择未完成事件，包括晚到的旧时间事件；200 条是分页预算，不是总上限。入站归档和任务投递之间的中断由扫描补偿，不依赖当时一定成功投递。

按 created_at/event_id 排序仅用于稳定分页，不能把扫描最大值保存成永久排除较小值的水位。新版本重学需显式指定证据范围，不能仅因版本改变就无界重放全部历史。原事件实质修订需要新证据版本或新事件 ID，重复平台投递保持原身份；不能静默改写已处理证据后继续沿用完成标记。

这条路线增加完成记录及未处理事件查询成本，实施时检查索引与查询计划，但能缩小对所有归档写入方的改造。每群 archive_seq 配合提交顺序锁是未来规模变化时可评估的替代方案；首版不同时实现它。普通自增 ID 或 created_at 不能单独保证事务提交顺序。

## 8. 必读信息与按需回忆

| 读取层 | 内容与方式 |
| --- | --- |
| 行动前必读 | 按群和已解析目标成员精确读取有效称呼、互动边界。属于代码约束的字段交给决策与发送检查，不依赖向量模型或普通 TopK。 |
| 参与后按需读取 | 用本轮完整语义检索相关人物事实、群文化和经历，复用 BM25/vector/RRF 与降级能力。首版一个相关条目列表即可，不要求四套独立检索管线。 |

MemoryContext 是本轮读取结果，可包含 constraints 与 relevant_memories；它不是新存储层。条目携带 memory_id/revision、主体、参与者、内容、来源性质、观察时间和证据引用。内部 ID 用于校验和追踪，模型可见文本清楚表达“谁在何时说过什么”，不要求向群友展示内部字段。

当前 burst、被引用文本和当前话题共同提供检索上下文。“对”“那个呢”不能仅以最新两个字搜索。完整 PersonaConfig、场景和经 Canon 裁决的 PersonaView 与记忆一起交给当前 ResponsePlanner→Composer，保留事实来源；删除停用的拼接路径，不为旧 RelevantMemories 建兼容返回值。

自动上下文、query_memory、主动回忆和后台预览统一使用有效性与可见性过滤。默认只读 active 且未过期的记录；历史查询可返回明确标为“已更正/已失效”的版本，revoked 内容不返回。向量读取关联权威记录并校验源 revision，旧索引缺失或陈旧时走词法轨道。

在现有 trace 上记录 retrieved 与实际 included 的 ID/revision，首版即可检查“查到了但没进 Prompt”。cited 与 feedback_linked 后移。任何使用统计都不刷新原始证据时间、增加事实可信度或延长有效期。预算按相关性裁剪，不能把检索排序列表当时间序列截尾；无相关内容时不强行插入记忆。

发送前校验当前约束及已包含记忆的版本/有效性。生成期间收到停止、更正或遗忘请求时，取消过期计划或重建上下文，不发送已失效内容。PromptSession 的相关缓存同步失效；只过滤下一次检索不足以完成当前回合的纠错与遗忘。

## 9. 长期保存、遗忘与目标表

| 内容 | 默认时间策略 |
| --- | --- |
| 稳定称呼、偏好、身份自述、长期互动边界 | 无默认失效时间，等待新证据更正或本人撤销。 |
| 重要经历、群内故事、梗的历史出处 | 长期保存，年龄只影响常规召回热度；明确问起时仍可检索。 |
| 今晚别 @、本周有事等临时状态 | 明确 valid_until；到期退出当前约束，历史叙述保留时间限定。 |
| 少量 pending 候选 | 有限等待补证；pending_until 起点可为 7 天，不是长期记忆 TTL。 |

不使用“偏好 90 天、事件 180 天”统一删除策略。first/last_observed_at 只由有效原始证据决定，重试、索引重建和读取不得把旧事变成新观察。是否复核、压缩或归档在后续按实际规模评估，不为首版新增独立调度系统。

遗忘先事务性撤销并提升 revision，使所有读取与在途计划失效，再异步清理向量及缓存。保留最小不可召回的撤销标识和被禁止证据关系，自动提炼跨版本也必须检查；首版保守地排除被遗忘记忆引用的原事件用于再次自动提炼，可能少学该事件的其他信息，以避免改写后学回。新的本人明确记忆请求可使用新的证据建立记录，不能借旧任务复活。

声明为已遗忘的内容同时从记忆正文、含内容的变更历史、缓存和派生任务中清理；失效记录保留必要的最小身份与来源标识。原始群消息是否物理删除由消息保留策略另行决定，后台区分“退出记忆”与“删除原始消息”，不能把逻辑撤销宣传为全部记录已删除。

| 目标表/设施 | 职责与调整 |
| --- | --- |
| messages | 原始证据，保留发生时间及引用关系；明确证据修订契约，不要求新增 archive_seq 或改表名。 |
| memories | 唯一记忆权威数据，包含主体、经历参与者、状态、revision 和时间；不再是 memory_claims 的读取投影。 |
| memory_evidence | 按 memory_id/event_id 去重的证据关系及来源角色；不另存 JSON 证据数组或独立证据计数。 |
| memory_changes | 追加修改原因、操作 ID、版本和必要差异，供查看更正来源；不建立独立审核生命周期。 |
| learning_event_progress | 按 event_id/extractor_version 记录提炼完成，替换旧 learning_watermarks。 |
| memory_vectors、async_outbox | 复用版本检查、事务投递与重试，改用新记忆与批任务契约。 |
| retrieval_traces | 扩展本轮 included ID/revision；首版不为使用统计另建表。 |

删除旧 memory_claims、LearningCandidate/learning_candidates、learning_candidate_evidence、WriteIntent/MarkIntent 和直接业务 Upsert 入口；新表结构与调用方统一切换。候选只是提炼输出或 memories 中的 pending 状态，不保留 ClaimID 与 MemoryID 两套生命周期。旧 stage_memory_claim 工具及参数退出，目标工具调用同一 MemoryService；工具注册不代替真实调用和行为验收。

## 10. 实施顺序、切换与验收

| 阶段 | 范围 | 完成标准 |
| --- | --- | --- |
| A：首版闭环 | 补齐真实生成上下文、约束检查和发送事实；实现唯一记忆/证据/变更、后台窗口与处理进度、及时更正遗忘；删除旧学习入口。 | 自述能被正确记住、自然使用；沉默也学习；成员与群不串；边界生效；更正、遗忘与重试不冲突。 |
| B：按回放优化 | 调整筛选、窗口和检索预算，完善已有后台的证据查看和争议处理；评估历史检索与群文化提炼质量。 | 有质量与成本对比，能解释误记、漏记和无关召回；不以记忆数量增长作为进步。 |
| C：可选反馈学习 | 统一反馈所有者，持久化窗口与到期任务，完成动作归因后供记忆/关系/角色状态各自幂等消费。 | 无关群聊和无人回应保持 unknown；纠正只作用于对应事实；重试不会重复改变关系或状态。 |

阶段 A 内可以按数据写入、后台提炼、读取与回合接线分别开发，但上线验收要覆盖完整闭环。阶段 B、C 不作为首版发布前提，尚未可靠的反馈到记忆消费保持关闭。基础发送检查、Canon 的成功发送要求、个人边界和遗忘生效不能后移。

切换前停止旧记忆 worker、终止或排空在途任务，取消旧学习与索引任务，保存需要保留的原始证据和人工记录；替换 schema、代码、工具契约、配置与 PromptSession，从新记忆数据启动。历史重学显式选择范围并受任务预算约束，旧自动学习结果和向量不直接导入。人工记录如需迁入，用一次性离线脚本通过新 MemoryService 校验，不建设永久迁移适配层。

原始消息、人工记录和其他模块数据的清理范围在实际切换时单独确定。保留停机恢复用的备份和操作说明，不提供新运行时自动读旧表的兼容分支；本次文档修订不执行任何数据库删除。

首版用脱敏群聊片段做回放，并补真实 App 组装路径的集成验证：

| 用例 | 验收要求 |
| --- | --- |
| 本人说喜欢乌龙茶，下一次相关话题 | 模型看到正确主体、来源和内容；不只断言数据库存在记录。 |
| Bot 连续沉默，群友表达偏好 | 后台照常提炼，随后可回忆。 |
| 两人同偏好、同名不同人、跨群同一人 | 不共用主体记忆，不跨群拼接；同群公开事实可按明确提及者检索。 |
| 刷屏、复制、引用、Bot 复述或重复任务 | 不重复累计证据，不把转述当本人确认。 |
| 换称呼、临时停止、长期拒绝与 A→B→A | 新证据正确更新；模糊时间不转为执行时段；临时允许不解除长期拒绝。 |
| 多人共同经历，或 Bot 仅旁观讨论 | 按参与者可查；不虚构 Bot 参与和未知结果；不依赖情绪反馈才能保存客观事件。 |
| 一年后问起重要经历、长期边界无人再提 | 仍可检索和遵守；使用可控时间推进验证，不把测试称作一年线上运行证明。 |
| 忘记后重放、换提炼版本、旧向量任务晚到 | 不恢复内容；变更历史与缓存不可再次暴露被遗忘的记忆。 |
| 模型生成中收到更正、停止或遗忘 | 旧计划不得直接发送，版本检查与上下文失效实际生效。 |
| 第 201 条、晚到旧时间事件、并发任务、事务中断 | 能续跑，不跳过未处理证据，不重复应用；模型失败不写成功进度。 |
| 发送失败或部分成功 | 只记录成功动作，Canon 不提交未发出的设定，不伪造共同经历。 |

记录记忆正确率、主体/范围准确率、更正与遗忘生效情况、相关召回与实际 included 的正确性、无关记忆占比、积压时间和每千条消息模型成本。先建立回放基线，再决定是否需要更多审核、归因或索引能力。

阶段 C 启用前另行验证：只将有对象和证据的反馈归因到动作；窗口使用发送后 `(sent_at, deadline]` 区间并完整分页，同秒事件结合引用归属判断，顺序不明时保持 unknown；明确引用可在窗口结束后继续形成新证据；窗口、结果及消费任务可重启恢复，各消费者按反馈 ID/version 幂等。平台发送与 PostgreSQL 无法组成一个本地事务，回执不确定时记录待核对状态，不能盲目重发。

首版不引入图谱、强化学习、跨群画像合并、额外缓存层、reranker、独立记忆 Agent 或新的模型供应商。索引扩容按 [RAG_REFACTOR.md](/Users/admin/Desktop/go-agent/docs/RAG_REFACTOR.md) 的实际规模与评测结果推进。
