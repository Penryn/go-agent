# 工具与文件任务

工具由 `internal/application/tools` 注册，主模型通过 AgentPlanner 使用。读取或更新状态的工具可连续调用，一轮最终选择一个终结动作；实际发送仍由 Action 执行。

## 内置工具

| 类型 | 工具 | 边界 |
| --- | --- | --- |
| 文字与引用 | `speak_text`、`quote_reply` | 回复内容受人物事实与输出校验约束 |
| 互动与素材 | `react_emoji`、`send_meme` | 使用可用消息和已批准素材 |
| 沉默 | `stay_silent` | 结束本轮，不产生消息 |
| 戳回 | `poke_member` | 仅被戳场景提供 |
| 撤回纠正 | `repair_message` | 仅存在可撤回 Bot 消息时提供，并限制目标消息 |
| 文件发送 | `send_file` | 仅存在本轮文件工作区时提供，只发送 `output/` 产物 |
| 信息读取 | `query_memory`、`search_meme`、`query_member_profile` | 按当前群、成员与可见性读取 |
| 状态更新 | `remember_memory`、`record_relationship_signal`、`update_persona_fact` | 分别执行来源、权限与次数校验；不等同于等待消息送达后统一提交 |

模型可通过文字工具的 `self_facts` 提议新的虚拟人物事实。Canon 在发送前校验，实际送达后才确认公开自述；这与普通记忆和关系工具的写入时机不同。

## 群工具白名单

- `tool_allowlist` 为空：开放当前场景可用的内置工具。
- `tool_allowlist` 非空：只开放列出的工具，仍受触发场景限制。
- MCP 和 Codex 工具始终需要显式列入白名单，仅开启服务配置不会自动对群开放。

群策略使用 `group_policies` 列表，例如：

```yaml
group_policies:
  - group_id: 987654321
    max_consecutive_bot: 3
    tool_allowlist:
      - speak_text
      - stay_silent
      - query_memory
      - delegate_codex_task
```

群覆盖中的工具列表会替换默认列表；需要的内置终结工具也应列出。人格配置当前全局共享，群策略只覆盖工具白名单和连续发言上限。

## MCP

`tools.mcp_servers` 支持 `stdio` 和 `http` 连接。发现的工具以 `mcp_<server>_<tool>` 命名；实际名称以后台发现结果为准，再加入目标群白名单。

初次启动没有数据库配置时，使用 YAML 中的 MCP 列表。后台可更新并持久化 MCP 配置；数据库已有记录时，后续启动优先使用该记录，单改 YAML 不会覆盖它。

## Codex

`delegate_codex_task` 通过本机 Codex 的 `app-server` stdio 协议执行。先确保运行 Bot 的系统账号能够正常使用 Codex，再开启 `tools.codex.enabled` 并配置群白名单。

| 字段 | 作用 |
| --- | --- |
| `binary` / `model` | 命令路径和可选模型；空模型沿用本机设置 |
| `cwd` | 普通项目任务目录；留空使用 Bot 启动目录 |
| `workspace_root` | 附件任务临时目录的父目录；留空使用系统临时目录 |
| `network_enabled` | 是否允许 Codex 任务联网，默认关闭 |
| `timeout` / `max_concurrency` | 单次委派超时与并发上限 |
| `write_user_whitelist` | 可以发起写任务的 QQ 用户；为空时拒绝写任务 |

委派默认只读。修改文件需传 `write=true` 且用户在写白名单内；普通编辑直接执行，删除、覆盖、凭据或命令执行等被识别为危险的任务，会要求用户对同一任务在 QQ 中确认。

委派还受聊天回合截止时间限制；当前回合最长 45 秒，因此将 `tools.codex.timeout` 配成更长不会延长整个回合。

## 附件与产物

启用 Codex 且当前事件包含文件附件时，运行时尝试创建隔离目录，将附件放入 `input/`，产物放入 `output/`。准备成功后，Codex 的本轮工作目录改为该临时目录。

产物可经 `send_file` 或委派结果中的文件信息送交 Action；工作区在回合结束后清理，不进入长期对话历史。新群消息和停机可能取消任务；当前没有独立、可跨重启恢复的 Codex 长任务队列。后台“任务队列”展示的是 Outbox 副作用。

字段完整定义与默认值见 [配置示例](../configs/config.example.yaml)，运行边界见 [项目架构](./ARCHITECTURE.md)。
