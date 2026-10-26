# Application packages

`internal/application` 的顶层包按用例和运行能力划分；`presence` 与 `runtime` 在包内继续细分生命周期职责。完整调用链见 [当前架构](../../docs/ARCHITECTURE.md)，日常变更入口见 [开发说明](../../docs/DEVELOPMENT.md)。

## 运行主链路

- `normalizer`：输入标准化
- `presence`：统一群事件与模型回合生命周期；`ingestion.go` 管接入，`turn.go` 管规划至送达，`lifecycle.go` 管取消与关闭
- `context`：构建单次回合的统一上下文快照
- `prompting`：Composer 只消费上下文快照；AgentPlanner 负责模型与工具循环
- `action`、`outputguard`：动作校验与发送前安全控制

## 业务能力

- `memory`、`retrieval`、`learning`：记忆写入、检索和学习
- `persona`、`profile`、`relationship`、`scene`：人格、成员、关系和群场景
- `meme`、`multimodal`：素材和多模态处理

## 支撑能力

- `policy`、`ports`、`tools`：策略、外部能力接口和工具实现
- `workspace`：按回合隔离输入与产物的临时工作区
- `modelusage`、`textutil`：模型用量记录和通用文本工具
- `runtime`：outbox、scheduler 等进程运行设施

## 命名约定

目录使用 Go 包的短名，优先使用小写单词；多词目录只有在现有领域边界明确时才使用下划线（例如 `group_actor`）。不为了形式统一改名，除非目录职责或公共 API 同时发生变化。
