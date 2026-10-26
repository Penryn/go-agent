# 数据库 Schema

[schema.sql](./schema.sql) 是应用启动时执行的幂等基线，使用 PostgreSQL 与 pgvector。应用会先启用 `vector` 扩展，再依次应用文件中的语句；数据库账号需具备相应权限。

[启动代码](../internal/app/dependencies.go) 从进程当前工作目录向上查找 `schema/schema.sql`。单独运行或分发二进制时，需要保留该文件及目录结构。当前没有单独加载 `migrations/` 目录的流程。

## 数据边界

- `messages` 保存真实收发事件，`group_working_memory` 是可恢复的群上下文投影。
- 普通记忆和相处偏好共用 `memories`；结构化偏好使用 `predicate`、`normalized_value`、`qualifier`，有效期使用 `expires_at`。
- `memory_vectors` 和 `meme_vectors` 是可重建投影，当前使用 `halfvec(2048)`。
- 人物事实、关系、运行状态和 Outbox 的职责见 [当前架构](../docs/ARCHITECTURE.md)。

幂等基线不等于任意历史库的自动迁移。修改表结构时，需要同时核对存储 SQL、现存数据和异步任务 payload，不能只修改 Go 类型；已有非结构化记忆不会被猜测转换为偏好。

数据库集成测试使用临时数据库；连接要求和跳过规则见 [开发与验证](../docs/DEVELOPMENT.md)。
