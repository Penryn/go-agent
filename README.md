# go-agent

go-agent 是一个长期参与 QQ 群聊的虚拟群友。运行时由 Go 进程连接 PostgreSQL/pgvector、NapCat OneBot 和模型服务。

## 快速启动

### 1. 准备配置

```bash
cp configs/config.example.yaml configs/config.yaml
```

编辑 `configs/config.yaml`，填写聊天模型和 Embedding 模型：

```yaml
models:
  main:
    model: "你的聊天模型"
    api_key: "你的模型密钥"
  embedding:
    model: "你的 Embedding 模型"
    api_key: "你的模型密钥"
```

默认 `qq.enabled: false`，可以先只验证数据库和后台。配置文件可能包含密钥，不要提交。

### 2. 启动 PostgreSQL

```bash
docker compose up -d postgres
docker compose ps
```

看到 `qqbot-postgres` 状态为 `healthy` 后继续。

### 3. 接入 QQ（需要时）

```bash
docker compose up -d napcat
docker compose logs -f napcat
```

首次启动 NapCat 需要用手机 QQ 扫描日志里的二维码完成登录；也可以打开 `http://127.0.0.1:6099` 进入 NapCat WebUI。登录成功后确认 OneBot HTTP/WS 分别使用 `3000`/`3001`。

再编辑配置：

```yaml
qq:
  enabled: true
  self_id: 123456789
  outbound_url: http://127.0.0.1:3000
  event_ws_url: ws://127.0.0.1:3001
  group_whitelist: [987654321]
```

把示例中的 QQ 号和群号替换成实际值。

### 4. 启动 Go 服务

```bash
make run
# 等价于：go run ./cmd/qqbotd -config configs/config.yaml
```

检查服务：

```bash
curl http://127.0.0.1:8088/healthz
```

启用 QQ 时，返回内容应包含 `"ok":true` 和 `"qq_connected":true`。后台地址：

```text
http://127.0.0.1:8088/admin/
```

未配置 `server.admin_token` 时，后台只允许本机访问；配置后在页面输入令牌。

## 验证

```bash
make replay       # 离线回放，不连接 QQ 和外部模型
make check-go     # Go 侧检查
```

后台前端改动后再执行：

```bash
make admin-install
make check
```

更多说明：

- [配置示例](configs/config.example.yaml)
- [开发说明](docs/DEVELOPMENT.md)
- [架构说明](docs/ARCHITECTURE.md)
