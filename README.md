# QQ 群友 AI Bot

一个长期在线、能观察群聊、具备稳定人格、能自主决定是否发言的 AI 群成员。

> **不是简单的问答 Bot。** 它能感知上下文、识别梗图、学习群文化、控制发言频率，像一个真正的群友一样参与群聊。

## 特性

- 🧠 **自主决策引擎** — 状态机 + LLM Gate 混合决策，自动判断是否发言、何时发言
- 🎭 **稳定人格系统** — 可配置的说话风格、情绪、能量，影响回复内容和主动性
- 👀 **多模态理解** — 图片描述、梗图识别、视频关键帧抽取
- 🃏 **表情包系统** — 自动收藏群内表情包，语义检索后精准复用
- 📝 **长期记忆** — 记住群友偏好、关系、话题，写入向量数据库
- 🔄 **后台学习** — Curator 和 Learning Agent 异步更新画像与群文化

## 架构概览

```
┌──────────────────────────────────────────────────────────────┐
│                     NapCat / OneBot 11                        │
│               (QQ 协议层 · WebSocket + HTTP)                  │
└────────────┬─────────────────────────────────┬───────────────┘
             │ 事件                            ▲ 动作
             ▼                                 │
┌──────────────────────────────────────────────────────────────┐
│                   确定性 Runtime Kernel                       │
│  事件标准化 → 上下文构建 → 策略/自主决策 → 动作执行             │
└──────┬────────┬────────┬────────┬────────┬───────────────────┘
       │        │        │        │        │
       ▼        ▼        ▼        ▼        ▼
   ┌───────┐┌───────┐┌───────┐┌────────┐┌────────┐
   │ Main  ││ Gate  ││Vision ││Curator ││Learning│
   │Persona││ Agent ││ Agent ││ Agent  ││ Agent  │
   │(Eino) ││(Chat) ││(Chat) ││(Graph) ││(Work-  │
   │  ADK  ││ Model ││ Model ││       ││ flow)  │
   └───────┘└───────┘└───────┘└────────┘└────────┘
       │        │        │        │        │
       ▼        ▼        ▼        ▼        ▼
┌──────────────────────────────────────────────────────────────┐
│               存储层：MySQL · Redis · Qdrant · MinIO          │
└──────────────────────────────────────────────────────────────┘
```

| Agent | 职责 | 实现方式 |
|-------|------|----------|
| **Main Persona** | 生成回复文本 | Eino ADK |
| **Gate** | 轻量判定是否值得回复 | `BaseChatModel.Generate` |
| **Vision** | 图片/视频多模态理解 | `BaseChatModel.Generate` |
| **Curator** | 后台画像更新 | `compose.Graph` |
| **Learning** | 群文化/黑话学习 | `compose.Workflow` |

## 前置要求

| 依赖 | 最低版本 | 用途 |
|------|---------|------|
| Go | 1.21+ | 编译运行 |
| Docker & Docker Compose | - | 基础设施 |
| 一个 LLM API | - | 模型推理（支持 Ark、OpenAI 兼容接口） |
| QQ 号 | - | 作为机器人身份登录 NapCat |

## 快速开始

### 1. 启动基础设施

```bash
docker compose up -d
```

启动 MySQL、Redis、Qdrant、MinIO 和 NapCat。NapCat 可长期常驻，QQ 登录完成后通常无需重启。

### 2. 配置

```bash
cp .env.example .env
```

编辑 `.env`，填入 API Key 和密码：

```dotenv
QQBOT_QQ_ACCESS_TOKEN=你的Token
QQBOT_MAIN_MODEL_API_KEY=你的模型Key
QQBOT_GATE_MODEL_API_KEY=你的模型Key
QQBOT_VISION_MODEL_API_KEY=你的模型Key
```

编辑 `configs/config.yaml`，确认 QQ 相关字段：

```yaml
qq:
  enabled: true
  self_id: 你的机器人QQ号
  outbound_url: http://127.0.0.1:3000
  event_ws_url: ws://127.0.0.1:3001/event
```

### 3. 配置 NapCat

NapCat 启动并完成 QQ 扫码登录后，打开管理页面 `http://127.0.0.1:6099`：

1. **开启 HTTP API** — 网络配置 → 启用 HTTP 服务，监听端口 `3000`
2. **设置 Token** — Access Token 填写与 `.env` 中 `QQBOT_QQ_ACCESS_TOKEN` 相同的值
3. **开启正向 WebSocket** — 启用 WS 事件流，端口 `3001`
4. **保存并应用** — 必要时重启 NapCat

> 通信链路：WS 收事件（`ws://127.0.0.1:3001/event`）+ HTTP 发动作（`http://127.0.0.1:3000`）

### 4. 验证最小链路

不接 QQ 时可用测试事件验证全链路：

```bash
go run ./cmd/qqbotd -config configs/config.yaml -once-event tests/testdata/mention_event.json
```

### 5. 启动服务

```bash
go run ./cmd/qqbotd -config configs/config.yaml
```

| 端点 | 用途 |
|------|------|
| `GET /healthz` | 健康检查 |
| `POST /onebot/events` | 调试 / 本地回放 |

## 出站动作

支持的 OneBot / NapCat 动作类型：

| 动作 | OneBot 端点 | 说明 |
|------|------------|------|
| `reply` | `/send_group_msg` | 文本回复（含引用） |
| `meme_only` | `/send_group_msg` | 表情包发送 |
| `recall` | `/delete_msg` | 撤回消息 |
| `poke_back` | `/group_poke` | 戳一戳 |
| `react` | `/set_msg_emoji_like` | 消息表情回应（NapCat 扩展） |
| `silent` | — | 不执行任何动作 |

## 配置参考

配置优先级：`内置默认值` < `configs/config.yaml` < `.env` / 环境变量

| 分类 | 文件 | 说明 |
|------|------|------|
| 公开配置 | [`configs/config.yaml`](configs/config.yaml) | 全部非敏感配置 |
| 私密配置 | `.env`（参考 [`.env.example`](.env.example)） | API Key、密码 |

<details>
<summary>主要配置段一览</summary>

| 配置段 | 说明 |
|--------|------|
| `app` | 应用名、运行模式（dev/prod） |
| `server` | HTTP 监听地址、超时 |
| `models` | 主模型/Gate/Vision/Embedding 的 provider、model、base_url |
| `persona` | 人设名、别名、说话风格、约束、回复上限 |
| `default_policy` | 默认群策略（存在感、安静时段、连续发言上限等） |
| `group_policies` | 按群号覆写策略 |
| `autonomy` | 自主决策参数（观察窗口、主动概率、限流） |
| `tools` | 工具白名单、超时、预算 |
| `memory` | 长期记忆 top_k、TTL、写入阈值 |
| `meme` | 表情包收藏、去重、冷却 |
| `multimodal` | 图片/视频下载超时、抽帧数 |
| `storage` | MySQL、Redis、Qdrant、MinIO 连接配置 |
| `qq` | QQ 开关、自身 ID、出入站地址、群白名单 |

</details>

## 项目结构

```
cmd/qqbotd/              启动入口
configs/config.yaml      默认配置
internal/
  core/ports/            端口接口（OutboundSender、MemoryStore 等）
  domain/                核心领域模型
    conversation/        会话事件
    policy/              自主决策、状态机
    reply/               回复计划、动作执行
    persona/             人格状态
    memory/              记忆模型
    media/               多模态 & 表情包
    profile/             群友画像 & 关系
  services/              业务服务
    action/              动作构建与执行
    meme/                表情包检索
    ...                  其它 Agent 服务
  adapters/
    outbound/napcat/     NapCat/OneBot 出站适配器
    inmemory/            内存实现（开发/测试用）
    ...                  MySQL、Redis、Qdrant、MinIO 适配器
  runtime/               运行时 Kernel
  config/                配置加载
migrations/              MySQL DDL（消息归档、记忆、画像、表情包、学习候选）
tests/                   测试数据
docker-compose.yml       基础设施编排
DESIGN.md                完整架构设计文档
```

## 开发

```bash
# 依赖整理
go mod tidy

# 编译
go build ./...

# 全量测试
go test ./...

# 验证 Docker Compose 配置
docker compose config
```

## 数据库迁移

MySQL 初始化脚本位于 [`migrations/001_init.sql`](migrations/001_init.sql)，包含：

- 消息归档表
- 长期记忆表
- 画像与关系表
- 表情包资产与描述表
- 学习候选表

## License

详见项目授权。
