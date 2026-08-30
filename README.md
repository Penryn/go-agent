# QQ 群友 AI Bot

基于 `DESIGN.md` 实现的模块化单体 QQ 群友 Bot。

当前形态：

- 确定性 Runtime Kernel：事件标准化、上下文构建、策略/自主决策、动作执行
- 前台 Agent：`Main Persona Agent` 使用 Eino ADK，`Gate` / `Vision` 直接走 `BaseChatModel.Generate`
- 后台流水线：`Curator` 使用 `compose.Graph`，`Learning` 使用 `compose.Workflow`
- 存储与基础设施：MySQL、Redis、Qdrant、MinIO 适配层和本地 integration test
- 协议边界：通过 [`zjutjh/napcat-sdk`](https://github.com/zjutjh/napcat-sdk) 接入 NapCat；OneBot 11 标准优先，NapCat 专有动作只在适配器层

## 目录

- `cmd/qqbotd`: 启动入口
- `configs/config.yaml`: 默认配置
- `internal/domain`: 核心领域模型
- `internal/core/ports`: 端口接口
- `internal/services`: 运行时、Agent、记忆、画像、多模态、后台工作流
- `internal/adapters`: NapCat/OneBot 适配器、存储适配器、模型适配器
- `migrations`: MySQL 初始化脚本

## 快速开始

1. 启动依赖：

```bash
docker compose up -d
```

这条命令会启动 MySQL、Redis、Qdrant、MinIO、NapCat。NapCat 可以长期常驻；只要 QQ 登录状态和网页端配置已经就绪，后续通常不需要反复重启。

2. 准备配置：

- 公开配置改 [`configs/config.yaml`](configs/config.yaml)
- 私密配置写 [`.env.example`](.env.example) 对应的 `.env`
- 如果要接真实 QQ，把 `qq.enabled` 改成 `true`，并填好 `qq.self_id`

```bash
cp .env.example .env
```

3. 使用默认配置验证最小主链路：

```bash
go run ./cmd/qqbotd -config configs/config.yaml -once-event tests/testdata/mention_event.json
```

4. 启动 HTTP 服务：

```bash
go run ./cmd/qqbotd -config configs/config.yaml
```

默认健康检查：

```text
GET /healthz
```

调试 / 回放入口：

```text
POST /onebot/events
```

## 接入 QQ

- 入站：默认通过 NapCat 正向 WebSocket `qq.event_ws_url` 收事件
- 出站：默认通过 OneBot 标准动作 `send_group_msg`
- SDK：正向 WebSocket、事件解析、HTTP action 调用和错误 envelope 统一由 `github.com/zjutjh/napcat-sdk` 处理
- 引用回复：通过 `reply` 消息段 + `text` 消息段组合
- 撤回：映射 `delete_msg`
- 可选 poke：仅在 NapCat 出站适配器中映射 `group_poke`

如果配置了 `.env` 中的 `QQBOT_QQ_ACCESS_TOKEN`，WS 握手和 HTTP 出站都会带上鉴权头。

### 应用侧配置

`configs/config.yaml` 至少确认这些字段：

```yaml
qq:
  enabled: true
  self_id: 你的机器人QQ号
  outbound_url: http://127.0.0.1:3000
  event_ws_url: ws://127.0.0.1:3001/event
```

私密配置放 `.env`：

```dotenv
QQBOT_QQ_ACCESS_TOKEN=和NapCat网页里设置的一样
```

### NapCat 网页端配置

NapCat 启动并完成 QQ 登录后，打开 `6099` 端口对应的管理页面，按下面配置即可对接本项目。

1. 在 OneBot / 网络配置里开启 HTTP API。
2. HTTP API 监听端口填 `3000`。
3. Access Token 填和 `.env` 里 `QQBOT_QQ_ACCESS_TOKEN` 相同的值。
4. 开启正向 WebSocket 事件流，确保 `3001` 端口可用。
5. 事件地址使用 `ws://127.0.0.1:3001/event`，与 `configs/config.yaml` 中的 `qq.event_ws_url` 保持一致。
6. 保存并应用配置，必要时重启 NapCat。

补充说明：

- 当前默认链路是 `WS 收事件 + HTTP 发动作`。
- WebSocket 断线后应用会自动重连；SDK 解析事件后仍以原始 JSON 进入领域归一化层。
- `POST /onebot/events` 仍然保留，主要用于本地回放和兼容性调试。
- 当前出站调用的是 OneBot 11 标准动作 `send_group_msg`，不是 NapCat 私有动作。

## 配置

优先级：

```text
builtin default < configs/config.yaml < .env / process environment
```

约定：

- 公开配置放 [`configs/config.yaml`](configs/config.yaml)
- 私密配置只放 [`.env.example`](.env.example) 对应的 `.env`

程序启动时会自动读取 `.env`。

常用文件：

- 公开配置示例：[`configs/config.yaml`](configs/config.yaml)
- 私密配置示例：[`.env.example`](.env.example)

## Migration

MySQL 初始化脚本：

- [`migrations/001_init.sql`](migrations/001_init.sql)

当前包含：

- 消息归档
- 长期记忆
- 画像与关系
- 表情包资产与描述
- 学习候选

## 验证

```bash
go mod tidy
go build ./...
go test ./...
docker compose config
```
