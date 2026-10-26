# 开发与验证

启动和配置步骤见 [README](../README.md)。以下命令均从仓库根目录运行。

## 构建与运行

| 命令 | 实际行为 |
| --- | --- |
| `make web` | 按 lockfile 安装所需前端依赖，执行 TypeScript/Vue 类型检查和 Vite 构建 |
| `make build` | 先构建前端，再生成 `bin/qqbotd` |
| `make run` | 先构建前端，再以 `configs/config.yaml` 启动 Bot |
| `make run CONFIG=其他配置路径` | 指定 YAML 配置启动 |
| `make test` / `make test-backend` | 运行后端测试与覆盖率统计，不自动构建前端 |

管理后台源码位于 `web/`，构建产物位于 `internal/app/adminui/dist/`，通过 `go:embed` 编入二进制。产物不入库；首次执行 Go 构建、测试或运行前，需要 `make web`。单独分发二进制时，还需提供 [数据库 Schema](../schema/README.md) 和配置。

应用从进程当前工作目录向上查找 `schema/schema.sql`。运行二进制时，应将工作目录设为含 `schema/` 的部署目录；配置路径通过 `-config` 指定。

## 后端测试

```bash
make web
make test
go test -race ./...
go test ./internal/architecture/... -count=1
docker compose config --quiet
```

PostgreSQL 测试使用本机 `127.0.0.1:5432`、用户 `qqbot`、密码 `qqbotpass`，为测试创建临时数据库并在结束后删除。该连接配置与应用 YAML 独立；账号需要创建数据库权限。可使用默认 Compose PostgreSQL：

```bash
docker compose up -d postgres
```

数据库不可用时，依赖它的测试会跳过，不能将这种结果作为数据库链路验证。模型、QQ 和 Codex 协议的测试替身也不证明真实外部服务已联通。

## 前端开发与校验

```bash
npm --prefix web ci
npm --prefix web run dev
```

开发服务器将 `/admin/api` 请求代理到 `http://127.0.0.1:8088`，需要同时运行 Go 后端。前端独立校验使用：

```bash
make web
```

当前 `web/package.json` 没有 `test` 脚本，因此 Makefile 中的 `test-frontend` 和 `test-all` 不能作为可用的完整测试入口；前端目前由类型检查和生产构建验证。

## 单事件验证

复制一份本地配置，将 `qq.enabled` 设为 `false`，然后执行：

```bash
make web
go run ./cmd/qqbotd \
  -config configs/config.yaml \
  -once-event tests/testdata/mention_event.json
```

这条路径使用内存出站 Sender，不连接 NapCat、不发送真实 OneBot 动作；仍会访问配置的数据库、保存处理结果，并可能调用已配置的模型或获准工具。测试应使用独立数据库。主模型未配置时，语言回复保持沉默，可以检查事件归档和状态链路。

## 变更入口

| 变更 | 代码位置 |
| --- | --- |
| 事件接入、同群取消和关闭 | `internal/application/presence/` |
| 上下文与提示词 | `internal/application/context/`、`internal/application/prompting/` |
| 人格、记忆、关系和群场景 | `internal/domain/` 及对应 `internal/application/` 包 |
| 工具权限和文件任务 | `internal/application/tools/`、`internal/application/workspace/` |
| 模型、QQ 或数据库接入 | `internal/adapters/` |
| 启动与依赖组装 | `internal/app/`、`cmd/qqbotd/` |
| 管理后台 | `internal/app/admin/`、`web/` |
| Schema | `schema/schema.sql`、`internal/adapters/storage/postgres/` |

分层规则与资源所有权见 [当前架构](./ARCHITECTURE.md)，应用包职责见 [应用层目录说明](../internal/application/README.md)。
