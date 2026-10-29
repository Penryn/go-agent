# 平台交互拟人化改造实施方案

## 目标与范围

本次改造模拟机器人在 QQ 客户端中的操作顺序，而不是修改语言风格。第一阶段只处理一个明确规则：机器人准备引用一条消息时，必须先通过平台接口将该消息标记为已读，成功后才能发送引用消息。

本方案不增加动作审计表、不引入通用工作流引擎、不改变模型输出协议，也不修改 replay 的本地发送语义。

## 调研结论

OneBot 11 标准公开 API 包含发送、获取和撤回消息等接口，没有标记已读接口。[OneBot 11 公开 API](https://github.com/botuniverse/onebot-11/blob/master/api/public.md)

NapCat 提供了扩展接口：

- `mark_msg_as_read`：按 `message_id` 标记单条消息已读，适合引用前置条件。[NapCat 接口文档](https://napcat.apifox.cn/226657389e0)
- `mark_group_msg_as_read`：按群会话标记已读。不同文档版本对参数有 `group_id` 与 `group_id + time` 的差异，不用于单条消息的严格前置检查。[NapCat API 列表](https://napneko.github.io/onebot/api)
- `set_input_status`：设置输入状态，参数是 `user_id` 和 `event_type`，当前文档没有群号参数，第一阶段不接入群聊行为。[NapCat API 列表](https://napneko.github.io/onebot/api)

项目依赖的 `github.com/zjutjh/onebot-sdk v1.1.0` 已包含 `MarkMsgAsRead`、`MarkGroupMsgAsRead` 和 `SetInputStatus` 的生成客户端方法。项目自己的 [qq.Client](/Users/phlin/Public/code/go/go-agent/internal/qq/qq.go:186) 尚未封装这些方法。

OneBot HTTP API 的同步调用返回 `status=ok` 才能确认请求被接受；`async` 只表示已提交，不能作为已读前置条件。[OneBot HTTP 通信](https://github.com/botuniverse/onebot-11/blob/master/communication/http.md)

## 行为契约

模型只返回回复意图，不能决定平台 API 的调用顺序。运行时负责执行以下顺序：

```text
模型生成回复意图
→ 本地确认引用目标可见且未撤回
→ 检查发送权仍属于当前进程
→ mark_msg_as_read(message_id)
→ 收到同步成功响应
→ send_group_msg
→ 收到发送回执
→ 归档出站消息并更新话题参与状态
```

没有引用时不调用已读接口。引用目标校验失败、已读接口失败或已读状态未知时，不发送该回复，也不自动降级成无引用消息；降级会改变模型选择的动作语义。

“收到事件”或“消息已经写入本地数据库”不等于平台已读。平台已读只能由显式 API 调用确认。由于已读接口没有返回 QQ 客户端界面状态的查询结果，程序将 `status=ok` 视为平台已接受该操作，不声称已经验证客户端 UI 状态。

## 代码改造

### 1. 封装单条已读接口

在 [internal/qq/qq.go](/Users/phlin/Public/code/go/go-agent/internal/qq/qq.go) 增加 `Client.MarkMessageRead(ctx, messageID string) error`：

- 使用 SDK 的 `MarkMsgAsRead`，请求 action 为 `mark_msg_as_read`。
- 复用 `Send` 对消息 ID 的十进制 `int64` 校验；空值、零值和溢出直接返回确定性错误。
- 使用精确整数 JSON，不经过浮点数，保留项目现有对大消息 ID 的处理方式。
- 使用同步调用，不使用 `_async` 后缀。

### 2. 在真实发送边界插入前置动作

在 [internal/app/app.go](/Users/phlin/Public/code/go/go-agent/internal/app/app.go) 的 `ownedSender.Send` 中保持以下顺序：

1. `store.CheckSender` 确认当前进程仍持有发送权。
2. `quote` 非空时调用 `MarkMessageRead`。
3. 已读成功后调用现有 `qq.Send`。

已读动作放在 `ownedSender` 而不是模型层，确保发送锁检查先于任何平台可见动作。现有 [delivery.StateMachine](/Users/phlin/Public/code/go/go-agent/internal/delivery/service.go:21) 继续负责 `sending`、发送回执和 `confirmed/unknown` 边界。

已读失败需要被识别为前置条件失败：

- 平台明确拒绝时，当前回合为 `failed`。
- 网络超时且无法判断请求是否到达时，当前回合为 `unknown`。
- 两种情况都不能调用 `qq.Send`。

不需要修改 `delivery.Request`、`Reply`、配置文件或数据库结构，因为引用消息 ID 已经在 `Quote` 中传递。

### 3. 保持其他路径不变

- QQ 未启用时，preview 行为不调用平台接口。
- replay 使用本地 Sender，不调用已读接口。
- 无引用的文字、贴纸发送不增加已读调用。
- `chat.validate` 继续负责检查引用目标是否属于当前可见对话。
- 发送成功后的归档、笔记 usage 账本和话题参与更新保持现有事务边界。

## 测试计划

### `internal/qq/qq_test.go`

增加 HTTP 测试，验证：

- 请求路径是 `/mark_msg_as_read`。
- 请求体包含正确的 `message_id`。
- 大于 JavaScript 安全整数范围的消息 ID不会被转成浮点数。
- API 返回错误时原样返回错误。

### `internal/app` 或发送边界测试

使用记录调用顺序的测试 Sender/HTTP 服务，验证：

- 引用发送顺序是 `CheckSender → mark_read → send`。
- 已读失败时发送接口调用次数为零。
- 已读超时不会自动重试发送。
- 没有引用时不会调用 `mark_msg_as_read`。

### 手工验收

在 NapCat API 调试台先单独调用 `mark_msg_as_read`，再发送一条引用消息，确认目标版本支持该 action。上线后用一个普通群消息检查 HTTP 请求顺序和最终 QQ 引用效果。

## 验收标准

- 任何带引用的真实 QQ 回复，在 `mark_msg_as_read` 成功响应前都不会发出 `send_group_msg`。
- 已读接口不可用时不会悄悄发送无引用回复。
- 现有发送确认、未知状态和 replay 行为不回归。
- 不增加数据库迁移、配置项和后台页面。

## 兼容性边界

该能力依赖 NapCat 的扩展 API。切换到只实现 OneBot 11 标准的实现后，`mark_msg_as_read` 可能返回 unknown action；此时引用回复按前置条件失败处理。部署前应在目标 NapCat 实例上用同步 HTTP 调用验证接口可用。

