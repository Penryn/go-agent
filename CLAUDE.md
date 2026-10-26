# 项目协作说明

go-agent 是长期在线的 QQ 虚拟群友。入口是 `cmd/qqbotd`，主链路是 `internal/app`、`chat`、`memory`、`meme`、`model`、`qq` 和 `store`。当前架构见 [ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 实现边界

- `app` 组装依赖并管理进程、HTTP 与资源关闭；`chat` 负责按群协调和实际表达流程。
- 聊天、记忆和表情包各自维护实际需要的状态。避免增加通用任务框架、重复动作模型或数值化社交状态。
- 记忆按群隔离；表情包使用全局素材池，冷却按群计算。素材来源元数据不能进入跨群可见的描述。
- 原始入站消息先归档，只有确认送达的出站消息进入真实聊天历史。发送不确定时不自动补发。
- 笔记与素材是事实来源，向量是有版本的投影。纠正、遗忘和禁用不能被旧索引绕过。
- `search` 保持无 I/O。外部模型协议、QQ 协议与 PostgreSQL 分别归属对应模块。
- 不加入 Codex、MCP、shell 或本地任务执行入口。架构测试检查相关依赖。

## 工作区与验证

保留已有改动。不要提交 `configs/config.yaml`、本地数据、图片缓存或构建产物。运行命令遵循 `/Users/admin/.codex/RTK.md`，使用 `rtk` 前缀。

```bash
rtk go test -race ./...
rtk proxy go vet ./...
rtk go build ./...
rtk git diff --check
```

后台是 `internal/app/admin.html`，由 Go 直接嵌入，不需要前端构建。页面脚本修改后检查 JavaScript 语法并验证相关 HTTP 接口。数据库测试使用独立临时数据库；跳过数据库或未做模型、QQ 联调时，明确说明边界。

按可独立检查的功能分批提交。每次 Git 提交前必须运行 `rtk proxy dcc doctor`，确认 GitHooks 状态正常后再提交；未经要求不推送。
