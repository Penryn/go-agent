# QQ 群友 AI Bot

一个会观察群聊氛围、选择合适时机参与对话的 QQ Bot。项目使用 Go + [Eino](https://github.com/cloudwego/eino) 构建，通过 [NapCat](https://github.com/NapNeko/NapCatQQ)（OneBot 11）接入 QQ。

它的目标不是逐条应答，而是像普通群友一样：该接话时简短回应，也可以发表情、发梗图或保持沉默。

## 核心能力

- 自主参与：结合上下文、时机和频率决定回复、回应或沉默
- 可配置人格：支持说话风格、情绪状态和按群覆盖策略
- 多模态理解：识别图片、视频和梗图，为回复提供摘要
- 表情包复用：自动收藏、语义检索、去重并限制重复发送
- 长期记忆：保存群聊事实、群友画像、兴趣和关系状态
- 异步处理：通过持久化 Outbox 执行视觉分析和向量索引任务
- 外部工具：可选接入 MCP，以及将复杂本地任务委派给 Codex

## 工作方式

```text
NapCat 事件
    ↓
归一化、去重与持久化
    ↓
群聊状态与候选回复
    ↓
时机判断与人格规划
    ↓
文本 / 引用 / 表情 / 表情包 / 戳一戳 / 沉默
```

所有消息都会被感知，但不会触发必然回复。详细设计见 [架构说明](docs/ARCHITECTURE.md)。

## 环境要求

- Go 1.25.8+
- Docker 与 Docker Compose
- 一个 QQ Bot 账号
- 火山方舟或 OpenAI 兼容模型的 API Key

## 快速开始

### 1. 启动依赖

```bash
git clone https://github.com/Penryn/go-agent.git
cd go-agent
docker compose up -d
```

这会启动 PostgreSQL（含 pgvector）和 NapCat。运行数据保存在 `data/`，不会提交到 Git。

### 2. 登录并配置 NapCat

打开 [http://127.0.0.1:6099](http://127.0.0.1:6099)，扫码登录 QQ，然后在“网络配置”中启用：

| 服务 | 端口 | 用途 |
|------|------|------|
| HTTP | `3000` | Bot 发送 OneBot 动作 |
| 正向 WebSocket | `3001` | Bot 接收群事件 |

为两个服务设置同一个 Access Token，并保存应用配置。

### 3. 配置 Bot

```bash
cp configs/config.example.yaml configs/config.yaml
```

至少填写以下内容：

```yaml
models:
  main:
    provider: ark            # 或 openai_compat
    model: 你的模型名称或端点 ID
    api_key: 你的 API Key

qq:
  enabled: true
  self_id: 你的机器人 QQ 号
  access_token: NapCat 中设置的 Token
  outbound_url: http://127.0.0.1:3000
  event_ws_url: ws://127.0.0.1:3001
```

完整配置和说明见 [configs/config.example.yaml](configs/config.example.yaml)。`configs/config.yaml` 已被 Git 忽略，请勿提交真实密钥。

### 4. 启动

```bash
go run ./cmd/qqbotd -config configs/config.yaml
```

应用启动时会自动执行幂等的 PostgreSQL Schema 迁移。健康检查：

```bash
curl http://127.0.0.1:8088/healthz
```

预期返回：

```json
{"ok":true}
```

## 本地事件验证

将 `qq.enabled` 临时设为 `false`，可在不连接 QQ、也不发送真实 OneBot 动作的情况下处理单条测试事件：

```bash
go run ./cmd/qqbotd \
  -config configs/config.yaml \
  -once-event tests/testdata/mention_event.json
```

## Bot 工具

内置工具默认可用；配置非空 `tool_allowlist` 后，只向该群开放列出的内置工具。

| 类型 | 工具 |
|------|------|
| 最终动作 | `speak_text`、`quote_reply`、`send_meme`、`react_emoji`、`repair_message`、`poke_member`、`stay_silent` |
| 信息读取 | `query_memory`、`search_meme`、`query_member_profile` |
| 状态更新 | `mark_memory_intent`、`update_affinity`、`update_member_profile` |

可选扩展：

- MCP：启动时加载，工具名统一为 `mcp_<server>_<tool>`
- Codex：通过 `delegate_codex_task` 执行复杂本地任务，默认只读

外部 MCP/Codex 工具即使已启用，也必须显式加入目标群的 `tool_allowlist`。Codex 写任务还受 QQ 用户白名单、工作目录和危险操作确认约束；联网默认关闭。

每轮可以连续调用多个信息或状态工具，但只能选择一个最终动作。`poke_member` 只在被戳场景开放；`repair_message` 只允许撤回上下文中最近的 Bot 消息，并可发送一条纠正文案。

## 常用配置

| 配置段 | 作用 |
|--------|------|
| `models` | 主模型、视觉模型和 Embedding 模型 |
| `persona` | 人格、语气、输出长度和按群覆盖 |
| `default_policy` / `group_policies` | 默认及群级策略、工具白名单 |
| `autonomy` | 主动参与概率、观察窗口和限流 |
| `memory` / `meme` | 记忆检索与表情包策略 |
| `tools` | MCP、Codex、超时和权限 |
| `storage` | PostgreSQL 连接和向量维度 |
| `qq` | NapCat 地址、Token 和群白名单 |

## 项目结构

```text
cmd/qqbotd/           启动入口
configs/              配置模板
internal/app/         依赖组装与生命周期
internal/domain/      领域模型
internal/application/ 业务编排与运行时
internal/adapters/    NapCat、模型和 PostgreSQL 适配器
internal/search/      无 I/O 的检索算法
schema/               PostgreSQL Schema
docs/                 架构、ADR 和专项设计
tests/testdata/       测试事件
```

## 开发验证

```bash
go build ./...
go test ./...
go test -race ./...
docker compose config
```
