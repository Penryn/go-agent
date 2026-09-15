# Database schema

- `schema.sql` 是当前启动时应用的幂等基线脚本。
- `migrations/`（如存在迁移文件）用于需要单独管理的增量变更；新增变更前先确认启动流程是否会自动加载它。
- 应用通过 `internal/app/dependencies.go` 从仓库根目录定位 `schema/schema.sql`，因此不要随意改名或移动该文件而不同时更新启动代码和配置文档。
