# go-agent

go-agent 是长期参与 QQ 群聊的虚拟群友：按语境选择接话或旁听，记住群友与经历，自动积累并使用表情包。运行时采用一个 Go 进程，连接 NapCat、PostgreSQL/pgvector 和模型服务。

- 聊天按群合并分条消息，一次聊天生成决定沉默、文字或表情包；普通新消息不会反复取消正在生成的回答。
- 记忆按群隔离，保留来源、修订和遗忘状态；向量、BM25、人物与时间线索参与融合召回。
- 表情包从各群自动采集，全局去重和复用；重复发送冷却按群计算。
- 运维后台查看对话、检索与发送结果，支持笔记和素材纠错，并展示记忆学习积压、召回、账本和索引健康状态。

主链路和可靠性边界见 [架构说明](docs/ARCHITECTURE.md)。程序不提供电脑控制、本地命令、外部工具或文件任务执行能力。

## 启动

需要 Go 1.26、带 pgvector 的 PostgreSQL、NapCat OneBot HTTP 与 WebSocket 服务，以及模型配置。后台 React 源码位于 `web/admin/`，构建结果嵌入 Go 程序；运行和常规 Go 构建使用仓库中的已提交静态资源，不需要 Node.js。修改后台前端时需要 Node.js 20.19 或更高版本。

```bash
cp configs/config.example.yaml configs/config.yaml
```

填写本地配置中的 `models.main`、`models.embedding` 和 QQ 连接参数。主模型应支持图片输入；也可以通过可选的 `models.vision` 配置图片模型。参数定义和默认值见 [配置示例](configs/config.example.yaml)。

```bash
make build
./bin/qqbotd -config configs/config.yaml
```

默认 `qq.enabled: false`，此时不连接 QQ，也不发送真实消息；后台整理和索引仍可运行。接入真实群时填写 `qq.self_id`、接口地址与令牌，再开启 QQ。建议先设置 `qq.group_whitelist`，并在切换前停止旧实例。

PostgreSQL 与 NapCat 的本地服务定义见 [docker-compose.yml](docker-compose.yml)。新运行时自动应用独立的 `groupfriend` 数据库命名空间，具体行为见 [数据库说明](schema/README.md)。旧配置不受支持，旧表与旧素材目录不会自动导入新运行时，也不会被启动流程删除。

访问后台：`http://127.0.0.1:8088/admin/`。未设置 `server.admin_token` 时，数据接口只接受本机连接；配置令牌后，在页面中输入令牌。健康检查为 `/healthz`。

## 验证

```bash
make admin-install
make check
make replay
```

`make check` 同时执行后台类型检查、生产构建、Go vet、竞态测试和 Go 构建。没有 Node.js 环境时可用 `make check-go` 只验证 Go 侧；`make build` 始终只构建使用已提交后台资源的 Go 二进制。后台开发命令与构建产物同步要求见 [开发说明](docs/DEVELOPMENT.md)。

两套默认回放使用预录模型输出、内存存储和模拟发送，不连接真实 QQ，也不调用外部模型。真实模型回放、测试数据库与验证边界见 [开发说明](docs/DEVELOPMENT.md)。

## 文档

- [架构说明](docs/ARCHITECTURE.md)：聊天、记忆、全局表情包、发送与关闭。
- [开发说明](docs/DEVELOPMENT.md)：后台前端构建、检查命令、离线与真实模型回放。
- [数据库说明](schema/README.md)：表、索引、版本和数据边界。
