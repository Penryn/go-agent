# 项目架构

本文描述当前代码的职责边界、运行时链路和可靠性方案。项目是一个模块化单体：目录按六边形架构分层，业务状态与外部实现分离，`internal/app` 是唯一的依赖组装入口。

## 架构定位

当前方案可以概括为：

- **部署形态**：单个 Go 进程 `qqbotd`，外接 NapCat 和 PostgreSQL/pgvector。
- **代码形态**：六边形架构（Ports and Adapters）+ 按业务能力拆分的模块化单体。
- **运行模型**：事件驱动；每个群一个 Group Actor，同群串行、跨群并发。
- **AI 调度模型**：消息先进入工作记忆并产生候选，Presence Runtime 再决定是否调用模型和发送动作。
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
       -> 产生 ThoughtCandidate
  -> perception outbox（图片/视频/表情包）
  -> Presence Runtime 调度候选
       -> 到期、抢占、同群串行、模型并发限制
  -> deliberation
       -> ContextSnapshot
       -> memory / meme retrieval
       -> Agent Planner 或 deterministic fallback
  -> action executor
       -> OutputGuard 和策略校验
       -> text / quote / meme / react / poke / recall / silent
  -> outbound/napcat
  -> reflection
       -> ThoughtRecord、model usage、retrieval trace
       -> cooldown、情绪、关系、人格事实更新
```

入站路径和回复路径是有意分离的：入站事件必须先落事实和工作记忆；回复是一个可取消、可过期、可沉默的候选任务。

## Presence Runtime 与 Group Actor

核心实现位于 [`internal/application/presence`](../internal/application/presence)。

`Runtime` 负责：

- 处理同步回放接口 `ProcessRawEvent` 和异步入站接口 `SubmitRaw`；
- 过滤机器人自身消息、无效事件和非白名单群；
- 扫描候选并控制 Job timeout、模型全局并发和主动开口；
- 在模型调用前后检查输出冷却，避免过期候选发送；
- 统一处理成功、沉默、取消、过期和异常终态；
- 在动作完成后写入思考摘要、用量和反思状态。

`group_actor.Manager` 为每个群维护一个 Actor。Actor 内的 `GroupWorkingMemory` 包含：

- 最近消息尾部和当前消息 burst；
- 当前话题、未闭环话题；
- 待处理候选及其状态；
- 媒体描述、Prompt Session 和 projection checkpoint。

同一群的候选由群锁串行执行，不同群可以并发执行。工作记忆持久化到 `group_working_memory`，Actor 空闲超过 `runtime.actor_idle_ttl` 后可回收并在下次消息到达时恢复。

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
| 记忆声明 | `memory_claims` | 带证据的候选记忆，确认后才进入长期记忆 |
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
2. 候选有效性和输出冷却检查；
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
2. 同群候选串行，跨群并发；模型调用有全局槽位和超时。
3. 候选在 stale、expired、cancelled、silent、sent 和 error 路径都进入终态。
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
