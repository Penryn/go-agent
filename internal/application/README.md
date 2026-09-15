# Application packages

`internal/application` 按用例和运行能力拆分。目录保持一层扁平结构，避免为了目录分组引入跨包循环依赖；具体职责按下面几组理解：

## 运行主链路

- `normalizer`：输入标准化
- `presence`：群组运行时、感知、审议、反馈和事件归档
- `context`：构建单次回合的统一上下文快照
- `prompting`：提示词、规划和工具循环
- `action`、`outputguard`：动作校验与发送前安全控制

## 业务能力

- `memory`、`retrieval`、`learning`：记忆写入、检索和学习
- `persona`、`profile`、`relationship`、`scene`：人格、成员、关系和群场景
- `meme`、`multimodal`：素材和多模态处理

## 支撑能力

- `policy`、`ports`、`tools`：策略、外部能力接口和工具实现
- `modelusage`、`textutil`：模型用量记录和通用文本工具
- `runtime`：outbox、scheduler 等进程运行设施

## 命名约定

目录使用 Go 包的短名，优先使用小写单词；多词目录只有在现有领域边界明确时才使用下划线（例如 `group_actor`）。不为了形式统一改名，除非目录职责或公共 API 同时发生变化。
