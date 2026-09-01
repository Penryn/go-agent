# 项目架构

本文描述当前代码的职责边界和依赖规则。目录按六边形架构分层，业务状态与外部实现分离，`internal/app` 是唯一的依赖组装入口。

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

## 主消息链路

```text
NapCat WebSocket
  -> inbound adapter
  -> normalizer
  -> Presence ingress / event log
  -> group actor / working memory
  -> thought candidate
  -> deliberation + context + prompting
  -> action executor
  -> outbound adapter
  -> reflection / learning / durable outbox
```

这里有三条不同的可靠性边界：

1. 入站事实先归一化并进入事件与 working-memory 路径。
2. 回复候选由 Presence Runtime 的有界 worker 处理；候选可以取消、过期或沉默。
3. 可重放副作用进入 PostgreSQL outbox，以租约、重试和幂等键保证重启恢复。

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
