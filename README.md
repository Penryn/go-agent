# QQ 群友 AI Bot

一个像真正群友一样参与群聊的 AI 机器人。基于 Go + [Eino](https://github.com/cloudwego/eino) 构建，通过 [NapCat](https://github.com/NapNeko/NapCatQQ)（OneBot 11 协议）接入 QQ。

Bot 会自动判断何时该说话、何时沉默，用短句和表情包自然接话——不做客服、不做话痨。

## 特性

- 🧠 **自主决策** — 状态机 + LLM Gate 双重判断，控制发言时机与频率
- 🎭 **人格系统** — 可配置说话风格、心情、精力，按群独立覆写策略
- 👀 **多模态理解** — 图片 / 视频 / 梗图自动识别，Vision Agent 提取摘要
- 🃏 **表情包库** — 自动收藏群内表情包，语义检索精准复用，去重 + 冷却
- 📝 **长期记忆** — 记住群友偏好与关系，Qdrant 向量检索 + MySQL 结构化存储
- 👤 **群友画像** — 跟踪活跃度、常用短语、亲密度，动态调整互动方式
- 🔄 **后台学习** — Curator + Learning Agent 异步提炼群聊黑话和高频表达

## 架构

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
   │ (ADK) ││(Chat) ││(Chat) ││(Graph) ││(Work-  │
   │       ││ Model ││ Model ││       ││ flow)  │
   └───────┘└───────┘└───────┘└────────┘└────────┘
       │        │        │        │        │
       ▼        ▼        ▼        ▼        ▼
┌──────────────────────────────────────────────────────────────┐
│               存储层：MySQL · Redis · Qdrant · MinIO          │
└──────────────────────────────────────────────────────────────┘
```

| Agent | 职责 | 实现方式 |
|-------|------|---------|
| **Main Persona** | 生成自然回复，调用运行时工具 | Eino ADK `ChatModelAgent` |
| **Gate** | 轻量判定是否值得回复 | `BaseChatModel.Generate` + 启发式回退 |
| **Vision** | 图片 / 视频内容理解 | `BaseChatModel.Generate`（多模态） |
| **Curator** | 后台提炼记忆和画像候选 | `compose.Graph` |
| **Learning** | 定时学习群聊黑话和表达 | `compose.Workflow` |

## 环境要求

| 依赖 | 版本 | 用途 |
|------|------|------|
| Go | 1.25+ | 编译运行 |
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

启动 MySQL、Redis、Qdrant、MinIO、NapCat 五个服务。

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
| ⑤ | Access Token | 自定义一个值 | 和 `.env` 中 `QQBOT_QQ_ACCESS_TOKEN` 保持一致 |

设置完成后点击 **保存并应用**，必要时重启 NapCat。

> **通信链路**：NapCat WS (`:3001`) → Bot 收事件 → Bot HTTP (`:3000`) → NapCat 发动作 → QQ

### 4. 配置应用

```bash
cp .env.example .env
```

编辑 `.env`，填入密钥（最少只需下面两行）：

```dotenv
QQBOT_QQ_ACCESS_TOKEN=你在NapCat网页里设置的Token
QQBOT_MAIN_MODEL_API_KEY=你的LLM-API-Key
```

编辑 `configs/config.yaml`，启用 QQ 并填写模型信息：

```yaml
qq:
  enabled: true
  self_id: 你的机器人QQ号          # 必填

models:
  main:
    provider: ark                  # 或 openai_compat
    model: 你的模型端点ID
```

> 完整配置项说明见 [`configs/config.yaml`](configs/config.yaml) 中的注释。

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
| `POST /onebot/events` | 调试 / 事件回放 |

## 配置参考

**优先级**：内置默认值 → `configs/config.yaml` → `.env` / 环境变量

| 文件 | 用途 |
|------|------|
| [`configs/config.yaml`](configs/config.yaml) | 公开配置（人格、策略、模型地址等） |
| [`.env.example`](.env.example) → `.env` | 私密配置（API Key、密码、Token） |

<details>
<summary>环境变量一览</summary>

| 变量 | 说明 |
|------|------|
| `QQBOT_QQ_ACCESS_TOKEN` | NapCat Access Token |
| `QQBOT_MAIN_MODEL_API_KEY` | 主模型 API Key |
| `QQBOT_GATE_MODEL_API_KEY` | Gate 模型 Key（可选，可复用主 Key） |
| `QQBOT_VISION_MODEL_API_KEY` | Vision 模型 Key（可选） |
| `QQBOT_EMBEDDING_MODEL_API_KEY` | Embedding 模型 Key（预留） |
| `QQBOT_STORAGE_MYSQL_PASSWORD` | MySQL 密码 |
| `QQBOT_STORAGE_REDIS_PASSWORD` | Redis 密码（本地无密码可留空） |
| `QQBOT_STORAGE_QDRANT_API_KEY` | Qdrant API Key（本地可留空） |
| `QQBOT_STORAGE_MINIO_ACCESS_KEY` | MinIO Access Key |
| `QQBOT_STORAGE_MINIO_SECRET_KEY` | MinIO Secret Key |

</details>

<details>
<summary>配置段一览</summary>

| 配置段 | 说明 |
|--------|------|
| `app` | 应用名、运行模式（dev / prod） |
| `server` | HTTP 监听地址、读写超时 |
| `models` | 主模型 / Gate / Vision / Embedding 的 provider、model、base_url |
| `persona` | 人设名、别名、说话风格、约束、回复字数上限 |
| `default_policy` | 默认群策略（存在感级别、安静时段、连续发言上限等） |
| `group_policies` | 按群号覆写策略 |
| `autonomy` | 自主决策参数（观察窗口、主动概率、限流阈值） |
| `tools` | Agent 工具白名单、超时、预算 |
| `memory` | 长期记忆 top_k、TTL、写入阈值 |
| `meme` | 表情包收藏、去重阈值、发送冷却 |
| `multimodal` | 图片 / 视频下载超时、抽帧数、视觉预算 |
| `storage` | MySQL、Redis、Qdrant、MinIO 连接信息 |
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
├── cmd/qqbotd/              # 启动入口
├── configs/config.yaml      # 公开配置
├── migrations/              # MySQL 初始化脚本
├── tests/testdata/          # 测试用事件数据
├── internal/
│   ├── domain/              # 核心领域模型
│   │   ├── conversation/    #   事件、消息、上下文快照
│   │   ├── media/           #   多模态附件、表情包资产
│   │   ├── memory/          #   长期记忆、学习候选
│   │   ├── persona/         #   人格配置与状态
│   │   ├── policy/          #   群策略、自主决策、状态机
│   │   ├── profile/         #   群友画像、关系状态
│   │   └── reply/           #   回复计划、动作执行
│   ├── core/
│   │   ├── ports/           #   端口接口（依赖倒置边界）
│   │   └── usecase/         #   核心处理流程编排
│   ├── services/            # 业务服务
│   │   ├── normalizer/      #   OneBot 事件标准化
│   │   ├── context/         #   上下文快照构建
│   │   ├── autonomy/        #   自主决策状态机
│   │   ├── policy/          #   群策略引擎
│   │   ├── gate/            #   Gate Agent
│   │   ├── prompting/       #   Prompt 编排 & Agent Planner
│   │   ├── action/          #   动作执行器
│   │   ├── outputguard/     #   回复截断 & 安全过滤
│   │   ├── memory/          #   记忆读写
│   │   ├── profile/         #   画像管理
│   │   ├── persona/         #   人格状态与情绪驱动
│   │   ├── meme/            #   表情包采集与检索
│   │   ├── multimodal/      #   图片 / 视频理解
│   │   ├── curator/         #   后台记忆提炼
│   │   ├── learning/        #   后台黑话学习
│   │   ├── review/          #   记忆 / 学习审核
│   │   └── tools/           #   Agent 运行时工具集
│   ├── adapters/            # 外部适配器
│   │   ├── inbound/napcat/  #   WS 收事件 + HTTP 入站
│   │   ├── outbound/napcat/ #   HTTP 出站动作
│   │   ├── model/           #   LLM 工厂（Ark / OpenAI）
│   │   ├── inmemory/        #   内存存储（开发用）
│   │   └── storage/         #   MySQL / Redis / Qdrant / MinIO
│   ├── runtime/
│   │   ├── bootstrap/       #   应用初始化与依赖组装
│   │   ├── dispatcher/      #   群级事件分发与串行化
│   │   └── scheduler/       #   后台任务调度
│   └── config/              # 配置加载与校验
├── docker-compose.yml       # 基础设施编排
└── DESIGN.md                # 完整架构设计文档
```

## 数据库迁移

MySQL 初始化脚本 [`migrations/001_init.sql`](migrations/001_init.sql)，包含消息归档、长期记忆、画像与关系、表情包资产与描述、学习候选表。

docker-compose 启动后手动执行：

```bash
mysql -h 127.0.0.1 -u qqbot -p qqbot < migrations/001_init.sql
```

## 开发

```bash
go mod tidy        # 依赖整理
go build ./...     # 编译
go test ./...      # 全量测试
docker compose config  # 验证 compose 配置
```

## License

详见项目授权。
