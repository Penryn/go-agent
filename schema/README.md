# 数据库

运行时通过 [store.Initialize](../internal/store/store.go) 应用嵌入的 [schema.sql](schema.sql)，使用独立的 `groupfriend` 命名空间。旧版本的公开表不参与读写，启动过程不删除旧数据，也不自动导入旧数据。

| 表 | 职责 |
| --- | --- |
| `messages` | 去重后的入站事件、撤回状态与确认送达的出站消息 |
| `notes` | 有来源的群内笔记、明确偏好、经历话题、修订、有效期、遗忘状态与向量投影 |
| `note_usage` | 已确认回复使用过的笔记账本，按 `(attempt_id, note_id)` 幂等去重 |
| `learning_runs` | 每次后台学习的状态、失败分类、阶段耗时、token、候选与变更数 |
| `memory_events` | 运维事件，例如被遗忘证据拦截的重建尝试 |
| `conversation_topics` | 每群话题状态、参与者、序号范围、版本与最后活跃时间 |
| `conversation_topic_events` | 最近事件的话题归属窗口，用于重启恢复引用链和词面索引 |
| `topic_summaries` | 按话题版本保存的摘要、证据事件与覆盖序号 |
| `stickers` | 全局素材、内容哈希、内部来源、缓存文件、描述、处理状态和向量投影 |
| `group_progress` | 每群后台整理与话题摘要的独立游标、重试时间 |
| `reply_attempts` | 规划结果、检索摘要、用量、发送状态；也提供按群素材冷却的依据 |
| `settings` | 当前数据库的向量维度 |

## 结构与索引

`schema.sql` 是启动模板，其中 `__DIM__` 由通过校验的 `models.embedding.dimensions` 替换，不直接把模板交给 psql。向量使用 pgvector `halfvec` 和 HNSW 索引，默认维度见配置示例。已有数据库的维度不匹配时拒绝启动，不隐式转换现有列。

索引写入同时检查记录修订与有效状态。更换同维度的 Embedding 模型时，查询排除旧模型投影，后台重新索引。不同维度需要显式建立新的数据库基线或迁移对应投影，启动流程不会代替维护者执行这种变更。

记忆按群过滤，素材按内容哈希全局唯一。素材源群与源事件只作内部追溯。冷却由 `reply_attempts` 中该群、该素材的确认发送记录计算，不采用素材全局最后发送时间。

## 事务与恢复

笔记更新和学习游标推进在同一事务完成，修订冲突时整批回滚。话题视图、事件归属窗口与非空摘要同事务保存；后台摘要先用独立事务提交摘要和覆盖序号，再推进摘要游标，失败只延后重试，不影响聊天。遗忘、纠正和禁用使旧投影失效，乱序到达的旧索引不能覆盖新状态。

成功发送、出站消息与 `note_usage` 账本在同一事务完成，账本冲突时忽略重复的 `(attempt_id, note_id)`；`notes.use_count` 和 `last_used_at` 只由实际新增的账本行推进。数据库与 OneBot 仍无法原子提交。启动发送所有权检查会将上次未完成的发送标为不确定，将未完成的规划标为放弃；不会自动补发旧聊天。

首次启动会执行一次记忆基线迁移，并在 `settings` 写入 `memory_rework_migration`：旧版硬编码的 `confidence=1` 改为 0.7，未进入账本时代的 `use_count` 清零，`usage_version=0` 保留为历史基线标记。迁移只执行一次，不把旧次数转换为账本行；之后的 `confidence=1` 不再被该迁移改写。

本地 PostgreSQL 服务参数见 [docker-compose.yml](../docker-compose.yml)。应用连接由 `storage.database_url` 配置。测试库创建与清理规则见 [开发说明](../docs/DEVELOPMENT.md)。
