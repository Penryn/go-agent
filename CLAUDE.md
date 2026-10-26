# 项目协作说明

go-agent 是长期在线的 QQ AI 群友，使用 Go / Eino、NapCat、PostgreSQL/pgvector 和 Vue 管理后台。

## 文档入口

- [README](README.md)：安装、配置和启动。
- [项目定位与功能](docs/PROJECT.md)：产品范围与能力边界。
- [当前架构](docs/ARCHITECTURE.md)：运行链路、依赖方向、数据和生命周期。
- [开发与验证](docs/DEVELOPMENT.md)：构建、测试、单事件验证和代码入口。
- [工具与文件任务](docs/TOOLS.md)、[混合检索](docs/RETRIEVAL.md)：专项行为。

修改行为时同步更新对应文档；当前实现以实际调用链和配置定义为准，不从历史计划推导已支持能力。

## 实现边界

- `internal/app` 组装依赖并管理资源；业务编排放在 `internal/application`。
- `domain` 不依赖应用或基础设施；`application` 不导入 adapter / app；adapter 不导入 app；`search` 保持无 I/O。依赖检查在 `internal/architecture/layers_test.go`。
- Presence 在启动前完成依赖组装。在线和同步入口共用接入逻辑；源事件先归档，再推进派生状态。
- 群状态串行更新，同群新回合取消旧回合，跨群并发。模型意愿与人格倾向交给模型，权限、冷却、事实和发送校验由运行时执行。
- Context 读取统一快照，Composer 只组装提示词。历史来自真实收发事件，工具过程和未送达文本不写成聊天事实。
- 人物公开自述以实际送达为边界；部分送达也需要完成反馈、Canon 和冷却收尾。
- 可重放副作用使用 Outbox，保持 handler 可重入和持久化 payload 可读。普通 QQ 发送尚不保证跨重启恰好一次。
- 新代码按职责归属，不为了目录分组增加抽象；仅供单个能力使用的辅助函数留在该能力内。

## 工作区与验证

保留已有暂存、未暂存和未跟踪改动。只修改任务相关文件，不提交 `configs/config.yaml`、数据目录或前端构建产物。

首次执行 Go 构建或测试前先 `make web`，因为管理后台通过 `go:embed` 嵌入。

```bash
make build
make test
go test -race ./...
go test ./internal/architecture/... -count=1
git diff --check
```

按改动范围选择检查；文档整理只需核对代码事实、命令和链接。当前没有前端 `npm test` 脚本，使用 `make web` 验证类型与构建；数据库测试被跳过时需说明验证边界。

每次 Git 提交前必须运行 `dcc doctor`，确认 GitHooks 状态正常；提交范围需与任务一致，未经要求不推送。
