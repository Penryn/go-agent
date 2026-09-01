# QQ 群友 AI Bot

一个像真正群友一样参与群聊的 AI 机器人。基于 Go + [Eino](https://github.com/cloudwego/eino) 构建，通过 [NapCat](https://github.com/NapNeko/NapCatQQ)（OneBot 11 协议）接入 QQ。

Bot 会自动判断何时该说话、何时沉默，用短句和表情包自然接话——不做客服、不做话痨。

## 特性

- 🧠 **Presence Runtime** — 持续感知群聊，按候选、时机和认知负载决定是否参与
- 🎭 **人格系统** — 可配置说话风格、心情、精力，按群独立覆写策略
- 👀 **多模态理解** — 图片 / 视频 / 梗图自动识别，Vision Agent 提取摘要
- 🃏 **表情包库** — 自动收藏群内表情包，语义检索精准复用，去重 + 冷却
- 📝 **长期记忆** — 记住群友偏好与关系，PostgreSQL + pgvector 向量检索与结构化存储
- 👤 **群友画像** — 跟踪活跃度、常用短语、亲密度，动态调整互动方式
- 🔄 **后台学习** — 从持久化事件归档中异步提炼群聊黑话和高频表达
- 🔌 **类型安全的 NapCat 接入** — 使用 [`zjutjh/napcat-sdk`](https://github.com/zjutjh/napcat-sdk) 统一处理 WebSocket 事件、HTTP action 与协议错误

## 架构

```
NapCat / OneBot 11
        │ inbound event
        ▼
Normalize → Event Log + durable archive
        ▼
Group Presence Actor
  ├─ Working Memory / burst 合并
  ├─ Topic / open loop
  └─ Thought Candidate
        ▼
Presence Scheduler
        ▼
Deliberator → Context Projection → Persona Planner
        ▼
Action Executor → NapCat outbound
        │
        └── origin=outbound self event → Reflection / Learning
```

Runtime 的核心原则是：所有消息都被感知，但不是所有消息都值得回复。回复是候选经过调度和思考后的副作用，不是入站消息的必然结果。

| 模块 | 职责 | 实现方式 |
|------|------|---------|
| **Group Presence Actor** | 串行维护每群 working memory、burst、话题和候选 | Go mailbox |
| **Presence Scheduler** | 按 due time、urgency、过期时间和认知负载选择候选 | 确定性规则 |
| **Deliberator** | 为候选构建窄上下文，并选择 reply/react/meme/silent | Runtime seam + Eino Planner |
| **Main Persona** | 生成自然短句、引用和工具调用 | Eino ADK `ChatModelAgent` |
| **Vision** | 图片 / 视频内容理解 | 多模态 `ChatModel`，异步接入 |
| **Curator / Learning** | 从归档事件提炼记忆、画像和群聊表达 | `compose.Graph/Workflow` |

## 一条群消息的链路

1. NapCat 通过 WebSocket 或 HTTP 将 OneBot 事件交给 Bot。
2. Normalizer 生成统一的 `EventEnvelope`，并按事件 ID 去重。
3. Group Actor 写入 Event Log 和 PostgreSQL 消息归档，再更新群的 working memory。
4. Actor 将消息转成可延迟、可取消、可过期的 `ThoughtCandidate`。
5. Presence Scheduler 在合适的时间 claim 候选，避免每条消息都立即触发模型。
6. Deliberator 读取窄上下文，选择回复、表情回应、表情包或保持沉默。
7. Action Executor 做输出约束和平台动作校验，然后调用 NapCat。
8. 发送成功后主动写入 Bot 自己的 `origin=outbound` 事件，供下一轮上下文和学习使用。

## 环境要求

| 依赖 | 版本 | 用途 |
|------|------|------|
| Go | 1.25.8+ | 编译运行 |
| Docker & Docker Compose | — | 运行基础设施和 NapCat |
| LLM API Key | — | 支持火山方舟（Ark）或 OpenAI 兼容接口 |
| QQ 号 | — | 作为机器人身份登录 NapCat |

## 快速开始

### 1. 克隆并启动

```bash
git clone https://github.com/Penryn/go-agent.git
cd go-agent
docker compose up -d
```

启动 PostgreSQL、NapCat 两个服务。

### 2. 登录 QQ

访问 NapCat 管理页面 **http://127.0.0.1:6099**，使用 QQ 扫码完成登录。

> 登录状态会持久化到 `data/napcat/`，后续重启通常不需要重新登录。

### 3. 配置 NapCat 网页端

在 NapCat 管理页面（`http://127.0.0.1:6099`）的 **网络配置** 中完成以下设置：

| 步骤 | 配置项 | 值 | 说明 |
|------|--------|-----|------|
| ① | 启用 HTTP 服务 | ✅ 开启 | Bot 通过 HTTP 发送消息 |
| ② | HTTP 监听端口 | `3000` | 对应 `qq.outbound_url` |
| ③ | 启用正向 WebSocket | ✅ 开启 | Bot 通过 WS 接收事件 |
| ④ | WebSocket 端口 | `3001` | 对应 `qq.event_ws_url` |
| ⑤ | Access Token | 自定义一个值 | 和 `configs/config.yaml` 中 `qq.access_token` 保持一致 |

设置完成后点击 **保存并应用**，必要时重启 NapCat。

> **通信链路**：NapCat WS (`:3001`) → Bot 收事件 → Bot HTTP (`:3000`) → NapCat 发动作 → QQ

### 4. 配置应用

先复制示例配置，再编辑 `configs/config.yaml`，启用 QQ 并填写模型信息与密钥：

```bash
cp configs/config.example.yaml configs/config.yaml
```

```yaml
qq:
  enabled: true
  self_id: 你的机器人QQ号          # 必填
  access_token: 你在NapCat网页里设置的Token

models:
  main:
    provider: ark                  # 或 openai_compat
    model: 你的模型端点ID
    api_key: 你的LLM-API-Key
```

> 完整配置项说明见 [`configs/config.example.yaml`](configs/config.example.yaml) 中的注释。真实 `config.yaml` 可能包含密钥，已被 Git 忽略。

NapCat 正向 WebSocket、事件解析、HTTP action 调用和协议错误由 `github.com/zjutjh/napcat-sdk` 统一处理；断线后应用会自动重连，原始事件 JSON 仍会进入领域归一化层。

### 5. 验证 & 启动

```bash
# 不接 QQ，用本地测试事件跑通全链路
go run ./cmd/qqbotd -config configs/config.yaml -once-event tests/testdata/mention_event.json

# 正式启动
go run ./cmd/qqbotd -config configs/config.yaml
```

启动后 Bot 自动通过 WebSocket 连接 NapCat 接收群消息。

| 端点 | 用途 |
|------|------|
| `GET /healthz` | 健康检查 |

## 配置参考

**优先级**：内置默认值 → `configs/config.yaml`

| 文件 | 用途 |
|------|------|
| [`configs/config.example.yaml`](configs/config.example.yaml) | 可提交的完整配置模板，不包含真实密钥 |
| `configs/config.yaml` | 本地运行配置，统一保存普通配置与密钥，已被 Git 忽略 |

<details>
<summary>密钥配置一览</summary>

| YAML 配置项 | 说明 |
|------|------|
| `qq.access_token` | NapCat Access Token |
| `models.main.api_key` | 主模型 API Key |
| `models.vision.api_key` | Vision 模型 Key（可选） |
| `models.embedding.api_key` | Embedding 模型 Key（预留） |
| `storage.postgres.password` | PostgreSQL 密码 |

</details>

<details>
<summary>配置段一览</summary>

| 配置段 | 说明 |
|--------|------|
| `app` | 应用名、运行模式（dev / prod） |
| `server` | HTTP 监听地址、读写超时 |
| `models` | 主模型 / Vision / Embedding 的 provider、model、base_url |
| `persona` | 人设名、别名、说话风格、约束、回复字数上限 |
| `default_policy` | 默认群策略（存在感级别、安静时段、连续发言上限等） |
| `group_policies` | 按群号覆写策略 |
| `autonomy` | Presence Runtime 的观察窗口、主动概率和限流参数 |
| `tools` | Agent 工具白名单、超时、预算 |
| `memory` | 长期记忆 top_k、TTL、写入阈值 |
| `meme` | 表情包收藏、去重阈值、发送冷却 |
| `multimodal` | 图片 / 视频下载超时、抽帧数、视觉预算 |
| `storage` | PostgreSQL 连接信息 |
| `qq` | QQ 开关、自身 ID、出入站地址、群白名单 |

</details>

## 出站动作

| 动作 | OneBot 端点 | 说明 |
|------|------------|------|
| `reply` | `/send_group_msg` | 文本回复（含引用） |
| `meme_only` | `/send_group_msg` | 表情包发送 |
| `recall` | `/delete_msg` | 撤回消息 |
| `poke_back` | `/group_poke` | 戳一戳 |
| `react` | `/set_msg_emoji_like` | 消息表情回应（NapCat 扩展） |
| `silent` | — | 不执行任何动作 |

## 项目结构

```
go-agent/
├── cmd/qqbotd/                  # 启动入口
├── configs/config.example.yaml  # 完整配置模板
├── docs/                        # 架构、ADR 与专项设计
├── schema/                      # PostgreSQL 幂等 schema
├── tests/testdata/              # 跨包测试数据
├── internal/
│   ├── app/                     # 依赖组装、启停与健康检查
│   ├── config/                  # 配置加载与校验
│   ├── domain/                  # 纯领域状态和值对象
│   │   └── presence/            # Working memory 与 thought candidate
│   ├── application/             # 用例与业务编排
│   │   ├── ports/               # 外部能力接口
│   │   ├── presence/            # 主消息生命周期
│   │   ├── runtime/             # Outbox 与后台调度
│   │   └── <capability>/         # memory、meme、prompting、tools 等
│   ├── adapters/                # NapCat、模型与存储实现
│   └── search/                  # 无 I/O 的检索算法内核
└── docker-compose.yml           # 基础设施编排
```

完整目录职责、依赖规则和变更入口见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)。

## 数据库迁移

PostgreSQL 建表脚本在 [`schema/schema.sql`](schema/schema.sql)，由应用启动时自动执行（含 pgvector 扩展、关系表与向量表），全部语句幂等，可重复执行。

## 开发

```bash
go mod tidy        # 依赖整理
go build ./...     # 编译
go test ./...      # 全量测试
go test -race ./... # 竞态检测
docker compose config  # 验证 compose 配置
```

事件生命周期与异步可靠性决策见 [`docs/adr/0001-runtime-lifecycle-and-outbox.md`](docs/adr/0001-runtime-lifecycle-and-outbox.md)。

## License

详见项目授权。
