# 存储层迁移到 PostgreSQL + pgvector 设计

日期:2026-08-31
状态:已确认

## 背景与目标

当前存储栈为 MySQL(关系数据,11 张表)+ Qdrant(语义向量,2 个 collection)+ Redis(运行时状态)+ MinIO(对象存储),单机 docker-compose 自部署。目标是把 MySQL 和 Qdrant 合并替换为一个 PostgreSQL + pgvector 实例,减少一个运维组件,备份恢复统一为单库 `pg_dump`。

约束与决策(已与用户确认):

- 现有数据不迁移,PG 从空库开始
- 旧 MySQL/Qdrant 栈彻底删除(代码、迁移文件、compose 服务、数据卷),不保留回退能力
- 驱动选型:pgx + pgvector-go
- 向量存 PG 单库,表内带过滤列(不做独立向量服务)
- 实施在单分支内顺序分阶段,每阶段可验证

## 架构

```
docker-compose: pgvector/pgvector:pg17(替换 mysql + qdrant 两个服务)

internal/adapters/storage/postgres/
├── store.go        # 实现 6 个 ports 接口(从 mysqlstore 改写)
├── vector.go       # 实现 VectorMemoryStore + VectorMemeStore(从 qdrantstore 重写)
├── migrate.go      # 迁移器(沿用按文件名顺序执行机制,方言改 PG)
└── store_test.go   # 集成测试(本地 PG 可达才跑,否则 skip)

migrations/         # 全部重写为 PG 方言,重新编号
装配点:internal/app/dependencies.go(关系库)
      internal/app/graphs.go(向量)
```

**vector.go 不依赖 eino-ext。** 现状是 `qdrantstore` 把 embedder 交给 eino-ext 的 qdrant indexer/retriever 编排;eino-ext 无 PG 组件,自写约 120 行(embedder 调用 + `ON CONFLICT` 写入 + `<=>` 检索),并顺手删除 eino-ext 的 qdrant/embedding 相关依赖。embedder 仍通过 `factory.EmbeddingModel()` 注入,保留 eino 本体的 `embedding.Embedder` 接口。

## 数据模型

### 关系表(11 张,结构照搬,方言转换)

| MySQL | PostgreSQL |
|---|---|
| `DATETIME(6)` | `TIMESTAMPTZ` |
| `JSON` | `JSONB` |
| `KEY idx (…)` 表内语法 | 单独 `CREATE INDEX` 语句 |
| `ON DUPLICATE KEY UPDATE` | `ON CONFLICT (主键) DO UPDATE SET x = EXCLUDED.x` |
| `DOUBLE` | `DOUBLE PRECISION` |
| `NOW(6)` | `NOW()` |

表清单:messages、memories、member_profiles、relationships、meme_assets、meme_descriptors、learning_candidates、learning_watermarks、group_working_memory、thought_records、async_outbox、schema_migrations(迁移器自建)。

### 向量表(新增 2 张)

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memory_vectors (
  memory_id VARCHAR(128) PRIMARY KEY,
  content   TEXT NOT NULL,
  embedding vector(2048) NOT NULL
);

CREATE TABLE meme_vectors (
  meme_id   VARCHAR(128) PRIMARY KEY,
  group_id  BIGINT NOT NULL,
  text      TEXT NOT NULL,
  embedding vector(2048) NOT NULL
);

CREATE INDEX ON memory_vectors USING hnsw (embedding vector_cosine_ops);
CREATE INDEX ON meme_vectors USING hnsw (embedding vector_cosine_ops);
```

`halfvec(2048)` 维度写死在 DDL。启动时校验配置的 `vector_dim` 与表结构一致,不一致拒绝启动(fail-fast,优于 Qdrant 时代的静默行为)。embedding 模型换维度时必须创建新 profile 并重嵌入，不能与旧 profile 混用。

### 检索语义

- `<=>` 是余弦距离(0=相同),现有 threshold 语义"相似度 ≥ threshold"转换为 `1 - distance >= threshold`
- **修正现存缺陷**:现在 memes 检索是全量取回再在应用层按 group_id 过滤,topK 被稀释。PG 版用 `WHERE group_id = $2 ORDER BY embedding <=> $1 LIMIT k`,取多少就是多少

## 迁移机制与配置

- `migrate.go` 逻辑不变:扫目录→按名排序→逐文件执行→记 `schema_migrations`。启动时先 `CREATE EXTENSION IF NOT EXISTS vector`,失败即报错(pgvector 未装)
- `migrations/` 清空重写:`001_init.sql`(关系表)+ `002_vectors.sql`(向量表),旧 MySQL 迁移文件删除
- `internal/config/config.go`:
  - `MySQLConfig` → `PostgresConfig`(host/port/database/user/password/sslmode),DSN 为 pgx URL 格式
  - `QdrantConfig` 删除;`vector_dim` 挪入 PG 配置段;collection 名配置删除(表名固定)
- `configs/config.yaml`、`docker-compose.yml` 同步更新;密码继续走 `.env`

## 实施阶段(单分支三步)

**阶段 A——PG 替 MySQL**:`postgresstore` 关系部分 + 迁移文件 + `dependencies.go` 切换 + 集成测试。完成标志:全量测试通过,app 连 PG 启动。

**阶段 B——PG 替 Qdrant**:`vector.go` + `graphs.go` 切换。完成标志:向量读写集成测试通过,embedding 调用路径不变。

**阶段 C——清理**:删 `mysqlstore`/`qdrantstore` 包、eino-ext 的 qdrant 与 embedding 依赖、compose 旧服务、`data/mysql` 与 `data/qdrant` 目录。

每阶段结束跑 `go build && go vet && go test ./...`。

## 测试

- 集成测试沿用现有模式:连 `127.0.0.1:5432`,连不上 skip
- 向量测试用固定输出的 fake embedder(同输入恒返回同向量),验证按余弦相似度取回,不消耗 embedding API 配额
- `ClaimOutbox` 的 `FOR UPDATE SKIP LOCKED`(PG 原生支持)靠现有并发路径覆盖

## 风险

- **HNSW 索引**:两边都建。memory_vectors 数据量小(几千条)顺序扫也够,建索引零成本
- **TIMESTAMPTZ 时区**:容器 TZ=Asia/Shanghai 下 `NOW()` 带时区,Go 侧统一 UTC 往返,`TimestampUnix` 存取不受影响
- **维度校验**:DDL 级约束 + 启动时校验,双保险
