# 开发与验证

## 后台前端

后台管理页使用 React、TypeScript 和 Vite，源码位于 `web/admin/`。生产构建输出到 [`internal/app/admin-dist/`](../internal/app/admin-dist/)，由 Go `embed` 嵌入并从 `/admin/` 提供。运行时和普通 Go 构建不需要 Node.js；修改前端源码时需要 Node.js 20.19 或更高版本。

首次安装或依赖变化后执行：

```bash
make admin-install
```

类型检查与生产构建：

```bash
make admin-typecheck
make admin-build
```

`make check` 会先执行类型检查和前端生产构建，再执行 Go vet、竞态测试和 Go 构建。没有 Node.js 环境时可运行 `make check-go`，只检查 Go 侧。生产构建具有确定性；提交前应确认构建后的 `git diff --exit-code -- internal/app/admin-dist` 没有额外差异，说明源码和嵌入产物一致。

本地联调时，先启动 Go 服务，再运行：

```bash
make admin-dev
```

Vite 在 `http://127.0.0.1:5173/admin/` 提供热更新，并将 `/admin/api` 代理到 `http://127.0.0.1:8088`。生产环境仍由同一个 Go 进程提供页面和 API，没有独立前端服务。

后台令牌只保存在 React 内存中，刷新页面后需要重新输入。笔记和表情包保存携带版本号；`409` 时应保留当前草稿并重新读取最新版本，`401`/`403` 表示鉴权或来源校验失败。

## 检查与验证

完整检查：

```bash
make admin-install
make check
rtk git diff --check
```

只检查 Go 侧：

```bash
make check-go
rtk git diff --check
```

`make build` 生成 `bin/qqbotd`，使用仓库中已提交的后台静态资源，不要求 Node.js。

## 数据库测试

[存储测试](../internal/store/store_test.go) 和 [启动测试](../internal/app/app_test.go) 创建独立临时数据库，结束后删除，不使用现有业务表。默认连接本机示例 PostgreSQL；连接不可用时会跳过数据库测试。

设置 `TEST_POSTGRES_DSN` 可指定测试管理连接。此时连接失败会让测试失败，账号需要创建临时数据库和安装 pgvector 的权限。该变量仅用于测试，不是应用配置。

数据库测试覆盖消息身份、撤回、成员证据、群范围、索引失效、游标事务、全局素材去重与按群冷却、自动采集与禁用，以及发送所有权和启动恢复。聊天测试检查普通新消息不取消生成、直接互动使草稿失效、追问、超时发送与退出等待。

## 离线回放

```bash
go run ./cmd/replay -fixture tests/replay/conversation.json
go run ./cmd/replay -fixture tests/replay/memory.json
```

默认使用预录模型输出、内存存储与模拟发送，不读取数据库、不连接 QQ、不请求外部模型。回放复用聊天、证据和输出校验流程；合并和插话等待参数为回放缩短，不能用它衡量线上延迟。

[对话样例](../tests/replay/conversation.json) 覆盖旁听、分条理解和去重，[记忆样例](../tests/replay/memory.json) 覆盖称呼、群范围和遗忘。每步可包含多条 OneBot 事件、预期结果状态与有效笔记数量；`learn: true` 可额外执行一次后台整理。输出 JSON 包含实际回应尝试和模拟发送数，预期不符时命令失败。

```bash
go run ./cmd/replay -config configs/config.yaml -fixture tests/replay/conversation.json
```

显式提供配置后进入 `live-model` 模式，调用配置的模型，仍然使用隔离内存与模拟 QQ 发送。此模式会产生模型请求费用；脚本中预录的文案与结果状态不再充当正确答案，需要人工读完整上下文评估。

## 验证边界

测试与脚本回放证明程序边界和状态流转，不证明模型读懂了群内梗，也不证明平台侧发送成功。真实模型回放需要可用配置；真实 QQ 验证还需要 NapCat、账号和目标群。没有进行这些验证时，在交付中明确说明。

新增能力要能指出它改善的实际群聊场景，并留下针对该场景的检查。提交前运行 `dcc doctor`，确认 GitHooks 正常；按独立功能分批提交。
