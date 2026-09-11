# 项目架构

本文描述当前代码的职责边界、运行时链路和可靠性方案。项目是一个模块化单体：目录按六边形架构分层，业务状态与外部实现分离，`internal/app` 是唯一的依赖组装入口。

## 架构定位

当前方案可以概括为：

- **部署形态**：单个 Go 进程 `qqbotd`，外接 NapCat 和 PostgreSQL/pgvector。
- **代码形态**：六边形架构（Ports and Adapters）+ 按业务能力拆分的模块化单体。
- **运行模型**：事件驱动；每个群一个 Group Actor，同群串行、跨群并发。
- **AI 调度模型**：消息进入工作记忆，决策引擎实时评估并通过响应规划器生成回复。
- **异步模型**：视觉理解、向量索引、学习等可重放副作用通过 PostgreSQL Outbox 执行。

## 部署拓扑

```text
                         ┌─────────────────────┐
                         │        QQ 用户       │
                         └──────────┬──────────┘
                                    │
                         OneBot 11 / NapCat
                       WebSocket 入站、HTTP 出站
                                    │
              ┌─────────────────────▼─────────────────────┐
              │                 qqbotd                    │
              │  Presence Runtime / Agent / Tools        │
              │  Memory / Meme / Learning / Scheduler    │
              │  Admin HTTP + embedded Vue assets        │
              └───────────────┬───────────────┬───────────┘
                              │               │
                         SQL / pgvector     HTTP API
                              │               │
              ┌───────────────▼──────┐  ┌───▼──────────────┐
              │ PostgreSQL + pgvector│  │ 管理后台浏览器    │
              └──────────────────────┘  └──────────────────┘
```

本地编排由 `docker-compose.yml` 提供 PostgreSQL 和 NapCat；Go 服务通过配置连接二者。启动入口为 [`cmd/qqbotd/main.go`](../cmd/qqbotd/main.go)，依赖组装和生命周期管理集中在 [`internal/app/app.go`](../internal/app/app.go)。

## 总体结构

```text
cmd/qqbotd
    |
    v
internal/app ------------------------------ composition root
    |                  |                  |
    v                  v                  v
application -------> domain <--------- adapters
    |                  ^                  |
    +-------> search   +------------------+
    |
    +-------> config <---------------- adapters/model
```

- `domain` 定义事实、状态和值对象，不知道数据库、模型、网络或应用编排。
- `application` 实现用例、Presence 生命周期、后台任务和外部能力端口。
- `adapters` 实现端口，负责 NapCat、PostgreSQL、模型和内存替身等技术细节。
- `app` 创建具体实现并完成依赖注入，不承载业务规则。
- `search` 是无基础设施依赖的检索算法内核。
- `config` 负责配置结构、默认值、文件和环境变量加载。

`internal/architecture/layers_test.go` 会检查生产代码的关键依赖方向，防止应用层反向引用 adapter，或领域层逐渐混入基础设施代码。

## 运行时主链路

```text
NapCat WebSocket
  -> inbound/napcat
  -> normalizer
  -> Presence Runtime 过滤和归一化
  -> Group Actor Observe
       -> 归档消息事实
       -> 更新 group working memory
  -> perception outbox（图片/视频/表情包）
  -> decision engine
       -> 基于上下文和社交认知评估
       -> 决策是否响应及响应类型
  -> response planner
       -> 根据决策生成回复计划
       -> 选择合适的动作类型和内容
       -> 调用 Composer.ComposeResponse 生成自然语言
            -> 检索相关记忆（最多5条）
            -> 构建提示词（人格+记忆+意图）
            -> LLM 生成自然语言回复
            -> 降级：失败时使用 intent
  -> action executor
       -> OutputGuard 和策略校验
       -> text / quote / meme / react / poke / recall / silent
  -> outbound/napcat
  -> feedback window
       -> 发送时打开反馈窗口（30秒观察期）
       -> 收集窗口内的用户回复
       -> 情绪分析（LLM 或规则）
       -> 分类反馈并记录到关系系统
  -> reflection
       -> ThoughtRecord、model usage、retrieval trace
       -> cooldown、情绪、关系、人格事实更新
```

入站路径和回复路径是有意分离的：入站事件必须先落事实和工作记忆；回复由决策引擎实时评估，不再依赖候选队列系统。

## Presence Runtime 与 Group Actor

核心实现位于 [`internal/application/presence`](../internal/application/presence)。

`Runtime` 负责：

- 处理同步回放接口 `ProcessRawEvent` 和异步入站接口 `SubmitRaw`；
- 过滤机器人自身消息、无效事件和非白名单群；
- 协调事件观察者（反思、人格、场景服务等）；
- 统一处理成功、沉默、取消、过期和异常终态；
- 在动作完成后写入思考摘要、用量和反思状态。

`group_actor.Manager` 为每个群维护一个 Actor。Actor 内的 `GroupWorkingMemory` 包含：

- 最近消息尾部和当前消息 burst；
- 当前话题、未闭环话题；
- 媒体描述、Prompt Session 和 projection checkpoint。

同一群的操作由群锁串行执行，不同群可以并发执行。工作记忆持久化到 PostgreSQL，进程重启后可恢复。

决策引擎（`decision_engine`）实时评估是否需要响应，并通过响应规划器（`response_planner`）生成具体的回复计划。化到 `group_working_memory`，Actor 空闲超过 `runtime.actor_idle_ttl` 后可回收并在下次消息到达时恢复。

## 领域、应用和适配器

### Domain

`internal/domain` 只表达业务事实和值对象，不知道数据库、模型、网络或应用生命周期。主要概念包括消息会话、群场景、关系事件与关系投影、记忆声明、人格事实、策略和回复动作。

### Application

`internal/application` 负责用例和编排：

- `presence`：消息到回复的主生命周期；
- `context` / `prompting`：模型上下文和 Agent 规划；
- `action`：最终动作构造、校验、发送；
- `memory` / `meme` / `learning`：记忆、表情包和增量学习；
- `retrieval`：BM25、向量召回、RRF 和降级；
- `tools`：内置工具、MCP、Codex；
- `runtime/outbox`、`runtime/scheduler`：后台任务和定时任务。

### Ports

`internal/application/ports` 定义应用需要的能力，例如 `MemoryStore`、`ProfileStore`、`RuntimeStateStore`、`VectorMemoryStore`、`OutboxStore`、`ThoughtStore` 和 `OutboundSender`。

### Adapters

`internal/adapters` 实现具体技术接入：

- `inbound/napcat`：OneBot WebSocket 事件接收；
- `outbound/napcat`：群消息、撤回、戳一戳和表情回应；
- `model`：Ark/OpenAI 模型工厂；
- `storage/postgres`：关系数据、运行状态、Outbox 和 pgvector；
- `inmemory`：测试和无 QQ 模式替身。

## 数据分层与持久化

PostgreSQL 同时承担权威事实、运行状态、异步任务和观测数据存储：

| 数据类别 | 主要表 | 用途 |
| --- | --- | --- |
| 会话事实 | `messages` | 保存入站和出站消息 |
| 长期知识 | `memories`、`meme_assets`、descriptor 表 | 记忆和表情包权威数据 |
| 群状态 | `group_working_memory`、`runtime_states` | 工作记忆、冷却、情绪等运行状态 |
| 用户关系 | `member_profiles`、`relationship_events`、`relationships` | 群友画像、关系证据和当前投影 |
| 用户记忆 | `memories` | 用户明确要求保存的信息，带来源事件并直接进入权威记忆 |
| 人格一致性 | `persona_fact_events`、reservations | 追加式人物事实和并发预留 |
| 异步任务 | `async_outbox` | 可重放的慢任务 |
| AI 观测 | `thought_records`、`retrieval_traces`、`model_usage_records` | 决策、检索和模型成本追踪 |
| 向量投影 | `memory_vectors`、`meme_vectors` | pgvector 语义检索索引 |

`memories`、`meme_assets` 是权威事实；BM25/vector 索引是可重建 projection，不反向成为事实来源。

## Outbox 可靠性边界

```text
业务事实 + Outbox task
          │ 同一事务
          ▼
      async_outbox
          ▼
   worker claim + lease
          ├── completed
          ├── retry（退避）
          └── dead_letter
```

Outbox 任务包含 `kind`、幂等键和 JSON payload。当前主要任务类型包括：

- `perception_event`：图片、视频和表情包感知；
- `memory_vector_index` / `meme_vector_index`：向量投影；
- `learning_extract`：群聊增量学习；
- 人格事实抽取和 finalize；
- `meme_mark_sent`：发送计数更新。

任务状态在 PostgreSQL 中持久化，进程重启后可继续领取。handler 必须可重入，外部副作用使用业务幂等键。

## 检索架构

统一入口为 [`internal/application/retrieval/service.go`](../internal/application/retrieval/service.go)：

```text
query + session visibility
        -> scope / expiry / type hard filter
        -> BM25 candidates || vector candidates
        -> RRF merge and deduplicate
        -> importance / confidence / recency / cooldown ranking
        -> context or tool result
```

- Memory：按群、用户、类型、过期时间和 scope 过滤；最终按重要性、置信度和时间衰减排序。
- Meme：按群/global、审核状态、情绪、场景、冷却和哑弹率排序。
- BM25 或向量轨道失败时，保留明确的 lexical/vector fallback 语义。
- 查询可写入 `retrieval_traces`，记录候选数量、各轨道排名、融合分数和降级轨道。

当前 BM25 是 Go 进程内全量扫描，适合验证行为和中小规模数据；数据规模增长后应替换为持久化倒排索引或数据库侧实现。

## 模型、工具与动作边界

模型链路为：

```text
ContextSnapshot
    -> Composer
    -> AgentPlanner
    -> ReplyPlan
    -> Action Executor
```

Agent 可以调用信息读取工具，也可以提交经过校验的记忆声明和关系信号，但不能直接修改最终状态或调用 NapCat。只有最终动作工具能产生发送意图，且仍需经过：

1. 群策略和工具白名单；
2. 决策引擎评估和输出冷却检查；
3. OutputGuard 文本清洗与长度限制；
4. Action Executor 参数校验；
5. NapCat outbound adapter。

主模型不可用时使用确定性 Planner；视觉模型和 Embedding 模型属于可选能力，可分别降级。

## 管理后台

Go 服务同时提供：

- `/healthz`：数据库健康检查；
- `/admin/`：嵌入式 Vue 管理后台。

后台读取运行状态、记忆、关系、表情包、Outbox、检索 Trace 和模型用量。前端源码位于 [`web`](../web)，`make web` 将构建产物输出到 `internal/app/adminui/dist`（不入库），编译时嵌入二进制。`server.admin_token` 为空时仅允许本机读取，配置令牌后用于受保护的远程访问。

## 可靠性与并发原则

当前实现的主要边界：

1. 入站事实先归档，再推进内存去重游标。
2. 同群操作串行，跨群并发；模型调用有全局槽位和超时。
3. 决策在 silent、sent 和 error 路径都进入终态，确保每个事件有明确的处理结果。
4. 可重放副作用进入持久化 Outbox；进程内队列不作为事实来源。
5. 关闭流程按 Scheduler、Runtime、Outbox、外部连接的顺序收敛。
6. `internal/architecture/layers_test.go` 自动检查生产代码依赖方向。

## 当前限制与演进方向

- **BM25 扩展性**：当前全量扫描，生产数据量上升后需要持久化 lexical index。
- **单体边界**：所有能力仍在一个 Go 进程中，暂不拆微服务；应等吞吐和运维压力明确后再拆分。
- **Outbox 运维**：已有 retry/dead-letter 状态，但还需要更完整的积压、延迟和死信告警/回放工具。
- **索引治理**：向量 projection 已支持 revision 和原子投递，仍需补充 reconcile/backfill 与索引健康检查。
- **AI 健康度**：`/healthz` 主要反映 PostgreSQL；模型和向量能力通过独立 capability probe 观测。
- **Composition Root 规模**：`internal/app/app.go` 当前集中完成大量 wiring，新增能力仍应优先保持“接口在 application、实现接到 app”的模式。

## 目录职责

```text
go-agent/
├── cmd/qqbotd/                  # 可执行程序入口、信号和日志初始化
├── configs/                     # 可提交的运行配置
├── docs/                        # 架构、ADR 和专项设计
├── schema/                      # PostgreSQL 幂等 schema
├── tests/testdata/              # 跨包测试数据
└── internal/
    ├── app/                     # composition root、生命周期和健康检查
    ├── config/                  # 配置模型、加载与校验
    ├── domain/                  # 领域状态，按概念拆包
    │   ├── conversation/
    │   ├── media/
    │   ├── memory/
    │   ├── persona/
    │   ├── policy/
    │   ├── presence/
    │   ├── profile/
    │   └── reply/
    ├── application/             # 用例与编排
    │   ├── ports/               # 外部能力接口和持久化契约
    │   ├── presence/            # 入站事件到动作结果的主生命周期
    │   │   ├── deliberation/
    │   │   ├── group_actor/
    │   │   ├── ingress/
    │   │   ├── perception/
    │   │   └── reflection/
    │   ├── runtime/             # 通用后台执行能力
    │   │   ├── outbox/
    │   │   └── scheduler/
    │   └── <capability>/         # memory、meme、prompting、tools 等用例
    ├── adapters/                # 外部系统实现
    │   ├── inbound/napcat/
    │   ├── outbound/napcat/
    │   ├── model/
    │   ├── storage/postgres/
    │   └── inmemory/
    └── search/                  # BM25、scope 等纯检索组件
```

## 依赖规则

新增代码时按以下顺序判断归属：

1. 只描述业务事实或状态：放 `domain/<concept>`。
2. 编排一个业务动作或决策：放 `application/<capability>`。
3. 定义应用需要、但由外界提供的能力：放 `application/ports`。
4. 接协议、数据库、模型或文件系统：放 `adapters/<technology>`。
5. 只负责创建对象和启停：放 `app`。
6. 与业务无关且无 I/O 的算法：放独立内核（当前为 `search`）。

禁止以下依赖：

- `domain -> application/adapters/app`
- `application -> adapters/app`
- `adapters -> app`
- 在 `cmd` 中直接组装具体服务

## 包拆分原则

- 优先按稳定职责拆包，不按“一个类型一个包”拆分。
- 同一能力的模型放在 `domain`，流程放在 `application`，外部实现放在 `adapters`，三者可以同名但不混放。
- package 文件超过约 700 行时优先按职责拆文件；只有出现独立依赖边界时才继续拆包。
- 通用工具必须有明确调用方；仅被一个能力使用的 helper 留在该能力内部，避免重新形成无边界的 `utils`。
- 新 adapter 先实现 `application/ports` 中的最小接口，再在 `app` 中接线。

## 变更入口

- 改群聊参与时机：`application/presence`、`domain/presence`、`domain/policy`
- 改 Prompt 或 Agent 工具：`application/prompting`、`application/tools`
- 改记忆/表情检索：`application/retrieval`、`search`、对应 storage adapter
- 改 QQ 协议接入：`adapters/inbound/napcat` 或 `adapters/outbound/napcat`
- 改数据库持久化：`adapters/storage/postgres`、`schema/schema.sql`
- 改启动和依赖生命周期：`app`

具体可靠性决策见 [`adr/0001-runtime-lifecycle-and-outbox.md`](adr/0001-runtime-lifecycle-and-outbox.md)，RAG 设计见 [`RAG_REFACTOR.md`](RAG_REFACTOR.md)。

---

## 反馈窗口与关系学习

### 反馈窗口机制

实现位于 [`internal/application/presence/feedback/window.go`](../internal/application/presence/feedback/window.go)。

**核心流程：**

1. **打开窗口**：消息发送后自动创建 30 秒观察窗口
2. **收集事件**：捕获窗口内所有用户回复（最多10条）
3. **情绪分析**：使用 LLM 或规则分析情绪倾向（-1.0 到 1.0）
4. **分类反馈**：根据情绪值和参与度分类
5. **记录关系**：自动记录到关系系统，触发投影更新

**反馈类型：**

- `EventPositiveFeedback` (valence > 0.3)：感谢、赞赏、满意
- `EventNegativeFeedback` (valence < -0.3)：质疑、拒绝、批评
- `EventConversationKept` (valence ~0)：对话继续但无明显情绪
- `EventConversationDropped` (无回复)：被忽略

**情绪分析策略：**

```go
// 可插拔接口
type SentimentAnalyzer interface {
    AnalyzeSentiment(ctx, messages) (float64, error)
}

// LLM 实现：语义理解，支持复杂表达、反讽、隐含情绪
type LLMSentimentAnalyzer struct { llm LLMCaller }

// 降级策略：LLM 失败时使用基于关键词的规则分类
```

**集成点：**

- `group_actor.respond()` 发送时调用 `feedbackManager.OpenWindow()`
- `group_actor.observe()` 观察时调用 `feedbackManager.CheckInboundEvent()`
- 窗口自动关闭并调用 `RelationshipService.Apply()` 记录事件

### 关系投影历史追踪

实现位于 [`internal/adapters/storage/postgres/store.go`](../internal/adapters/storage/postgres/store.go)。

**数据模型：**

```sql
CREATE TABLE relationship_history (
  id BIGSERIAL PRIMARY KEY,
  persona_id TEXT NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  revision BIGINT NOT NULL,
  
  -- 投影快照
  familiarity DOUBLE PRECISION NOT NULL,
  affinity DOUBLE PRECISION NOT NULL,
  trust DOUBLE PRECISION NOT NULL,
  tease_tolerance DOUBLE PRECISION NOT NULL,
  friction DOUBLE PRECISION NOT NULL,
  
  -- 因果信息
  trigger_event_id TEXT,
  trigger_kind TEXT,
  snapshot_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  
  INDEX (persona_id, group_id, user_id, snapshot_at DESC)
);
```

**自动记录机制：**

- 每次调用 `ApplyRelationshipEvent()` 时，在事务中同时：
  1. 保存关系事件
  2. 更新关系投影
  3. 保存历史快照
- 使用 `ON CONFLICT DO NOTHING` 避免重复记录
- 记录触发事件 ID 和类型，建立完整因果链

**查询接口：**

- `GET /admin/api/relationships/{group_id}/{user_id}/events` - 关系事件列表
- `GET /admin/api/relationships/{group_id}/{user_id}/projection-history` - 投影历史快照

**降级策略：**

历史表为空时，查询返回当前关系状态作为唯一版本，确保 API 始终可用。

### 前端可视化

实现位于 [`web/src/views/RelationsView.vue`](../web/src/views/RelationsView.vue)。

**交互功能：**

- 点击关系行展开/收起详情面板
- 展开行高亮显示，流畅动画过渡

**关系事件时间线：**

- 按时间倒序展示所有事件
- 事件类型标签（正面/负面/中性）
- 情绪值颜色编码（绿色正面、红色负面、灰色中性）
- 显示证据事件 ID 用于追溯

**投影历史视图：**

- 显示最近 50 条历史版本
- 版本号 + 更新时间 + 触发事件类型
- 各维度变化对比（↑ 正向变化、↓ 负向变化）
- 卡片式布局，悬停效果

**数据可视化示例：**

```
熟悉度: 0.75 ↑ +0.05 (绿色)
亲密度: 0.80 ↓ -0.10 (红色)
信任:   0.70 (变化 < 0.01 不显示)
摩擦:   0.20 ↓ -0.05 (绿色 - 摩擦降低是好的)
```

---

## 文本生成流程

### Composer 架构

实现位于 [`internal/application/prompting/composer.go`](../internal/application/prompting/composer.go)。

**核心方法：**

```go
func (c *Composer) ComposeResponse(
    ctx context.Context,
    personaCtx *personadomain.PersonaContext,
    evt *conversationdomain.ConversationEvent,
    intent string,
) (string, error)
```

**生成流程：**

1. **记忆检索**：使用 `MemoryRetriever` 检索相关记忆（最多5条）
2. **提示词构建**：组装人格、性格、记忆、对话、意图
3. **LLM 生成**：调用大语言模型生成自然语言回复
4. **降级保障**：LLM 失败或未配置时使用 intent

**提示词结构：**

```markdown
# 角色设定
你是 [人格名称]
[人格描述]

## 性格特点
- [特点1]
- [特点2]

## 行为风格
[风格描述]

# 相关记忆
1. [记忆内容1]
2. [记忆内容2]

# 当前对话
用户说: [用户消息]

# 回复意图
[决策引擎生成的 intent]

# 任务
请根据以上信息，生成一句符合角色人格和说话风格的自然回复。
要求：
1. 保持角色的性格特点和说话习惯
2. 回复要自然、简洁，不超过100字
3. 只输出回复内容本身，不要包含任何解释或元信息
4. 如果有相关记忆，可以自然地体现出来
```

**依赖注入：**

```go
composer := NewComposer(persona)
composer.WithLLM(llmAdapter).
    WithMemoryRetriever(memoryRetriever)
```

**适配器实现：**

- `llmAdapter`: `modelFactory` → `LLMCaller`
- `memoryRetrieverAdapter`: `retrievalService` → `MemoryRetriever`
- `textComposerAdapter`: `Composer` → `planning.TextComposer`

**降级策略：**

- LLM 未配置 → 使用 intent
- 记忆检索失败 → 继续生成（无记忆上下文）
- LLM 调用失败 → 降级到 intent
- 生成结果为空 → 降级到 intent

**示例对比：**

```
# 之前（模板回复）
用户: "今天天气怎么样？"
回复: "回复: 告知天气信息"  # 直接返回 intent

# 现在（LLM 生成）
用户: "今天天气怎么样？"
Intent: "告知天气信息"
记忆: ["用户喜欢晴天", "用户在北京"]
回复: "今天北京挺晴朗的，适合出门呢～"  # 自然语言 + 记忆
```

**集成点：**

`response_planner` 调用 `textComposer.ComposeResponse()` 生成回复文本，完全替代之前的模板回复机制。

---

## 数据流总览

完整的消息处理和学习循环：

```text
用户消息
  ↓
[入站] inbound/napcat → normalizer → Presence Runtime
  ↓
[观察] Group Actor Observe
  ├─ 归档事实
  ├─ 更新工作记忆
  └─ 检查反馈窗口 (CheckInboundEvent)
  ↓
[决策] Decision Engine
  ├─ 社交认知评估
  ├─ 场景匹配
  └─ 输出 Intent
  ↓
[生成] Response Planner
  ├─ Composer.ComposeResponse()
  │   ├─ 检索记忆（5条）
  │   ├─ 构建提示词
  │   └─ LLM 生成
  └─ 输出 ResponsePlan
  ↓
[执行] Action Executor → outbound/napcat
  ↓
[反馈窗口] FeedbackWindowManager
  ├─ OpenWindow (30秒观察)
  ├─ 收集用户回复
  ├─ 情绪分析 (LLM/规则)
  └─ 分类反馈类型
  ↓
[关系更新] RelationshipService.Apply()
  ├─ 保存事件
  ├─ 更新投影
  └─ 记录历史快照 [事务]
  ↓
[反思] Reflection
  ├─ ThoughtRecord
  ├─ 用量统计
  └─ Cooldown 更新
  ↓
[异步任务] Outbox
  ├─ 向量索引
  ├─ 学习抽取
  └─ 人格事实
```
