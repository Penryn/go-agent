# QQ 群友 AI Bot

go-agent 是一个长期在线的 QQ AI 群友，使用 Go 和 [Eino](https://github.com/cloudwego/eino)，通过 [NapCat](https://github.com/NapNeko/NapCatQQ)（OneBot 11）接入群聊。它结合上下文自主选择接话、表情、表情包、协助任务或沉默。

## 能力与运行方式

- 稳定人格、人物事实、情绪和群友关系。
- 有来源的长期记忆、相处偏好与混合检索。
- 图片和视频理解、表情包收藏与复用。
- 可选 MCP 与 Codex 工具，以及文件任务产物发送。
- 持久化 Outbox、运行记录与 Vue 管理后台。

```text
群事件 → 归档与状态更新 → 冷却检查 → 统一上下文
       → 模型与工具规划 → 事实和动作校验 → 发送与反馈
```

当前部署是一个 `qqbotd` 进程，外接 NapCat 和 PostgreSQL/pgvector。群状态串行更新，同群新模型回合取消旧回合，跨群并发。能力范围见 [项目定位与功能](docs/PROJECT.md)。

## 快速开始

在仓库根目录操作。需要 Go 1.25.8+、Node.js 与 npm、Docker Compose，以及用于接入的 QQ 账号和主模型 API Key。

### 1. 启动依赖

```bash
docker compose up -d
docker compose ps
```

Compose 启动 PostgreSQL/pgvector 与 NapCat，默认端口仅绑定本机，数据位于 `data/`。Bot 本身在后续步骤启动。

打开 [NapCat 后台](http://127.0.0.1:6099)，完成 QQ 登录，并配置 OneBot HTTP 服务 `3000` 和正向 WebSocket 服务 `3001`，填写各自的 Access Token。

### 2. 配置

```bash
cp configs/config.example.yaml configs/config.yaml
```

在复制的配置中填写主模型、QQ 账号和 NapCat 参数，例如：

```yaml
models:
  main:
    provider: ark
    model: 你的模型名称或端点 ID
    api_key: 你的 API Key

qq:
  enabled: true
  self_id: 123456789                  # 替换为机器人 QQ 号
  group_whitelist: [987654321]        # 替换为允许参与的群号
  outbound_url: http://127.0.0.1:3000
  outbound_token: 你的 OneBot HTTP Token
  event_ws_url: ws://127.0.0.1:3001
  event_ws_token: 你的 OneBot WebSocket Token
```

接入 OpenAI 兼容服务时，同时调整 `models.main.provider`、`base_url`、`model` 和 `api_key`。视觉模型与 Embedding 模型各自配置；没有配置时，相应的媒体理解或向量召回不可用。

`storage.postgres` 需要与 Compose 的数据库连接信息一致。配置模板使用本地 Compose 默认值；更改 Compose 的 `POSTGRES_*` 环境变量时，也需同步修改 YAML。应用从 YAML 读取运行配置，不从 `.env` 自动加载密钥。

`qq.group_whitelist` 留空会允许所有接入群；按群工具与连续发言策略使用 `default_policy` / `group_policies`。完整字段见 [配置示例](configs/config.example.yaml)，工具权限见 [工具与文件任务](docs/TOOLS.md)。本地 `configs/config.yaml` 已被 Git 忽略。

### 3. 启动 Bot

```bash
make run
```

`make run` 会安装所需前端依赖、构建管理后台，再启动 Go 服务。首次启动会将 [当前 Schema](schema/README.md) 应用到配置的 PostgreSQL 数据库。

```bash
curl http://127.0.0.1:8088/healthz
```

数据库健康时返回 `{"ok":true}`。该检查不代表 QQ 或外部模型已连通；能力状态可在后台查看。

打开 [管理后台](http://127.0.0.1:8088/admin/)，查看概览、记忆、关系、运行记录、Outbox、表情包和模型指标。`server.admin_token` 留空时后台数据仅允许本机访问；需要远程访问时配置令牌。

## 开发验证

```bash
make build              # 前端类型检查与打包，再构建 bin/qqbotd
make test               # 后端测试，需已有前端构建产物
go test -race ./...      # 竞态检查
docker compose config --quiet
```

测试数据库、单事件验证、前端开发和命令限制见 [开发与验证](docs/DEVELOPMENT.md)。

## 文档

| 需要了解 | 入口 |
| --- | --- |
| 功能、定位与能力边界 | [项目定位与功能](docs/PROJECT.md) |
| 主链路、模块、数据和生命周期 | [当前架构](docs/ARCHITECTURE.md) |
| 构建、测试和开发入口 | [开发与验证](docs/DEVELOPMENT.md) |
| 内置工具、MCP、Codex 和文件任务 | [工具与文件任务](docs/TOOLS.md) |
| 记忆与表情包的检索规则 | [混合检索](docs/RETRIEVAL.md) |
| 全部文档 | [文档索引](docs/README.md) |
