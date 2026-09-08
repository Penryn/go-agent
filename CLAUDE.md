# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

这是一个使用 Go 1.25.8 + Eino 框架构建的 QQ 群聊 AI Bot，通过 NapCat（OneBot 11）接入 QQ。Bot 采用事件驱动架构，能够自主判断参与时机，而非逐条应答。

核心技术栈：
- **语言**: Go 1.25.8
- **AI 框架**: Cloudwego Eino
- **数据库**: PostgreSQL + pgvector（语义检索）
- **QQ 接入**: NapCat（OneBot 11 协议）
- **模型支持**: 火山方舟 Ark / OpenAI 兼容接口
- **前端**: Vue 3 + Tailwind CSS + Element Plus

## 常用命令

### 开发与构建

```bash
# 构建（先 npm run build，再 go build）
make build

# 运行测试（会先打包前端；裸 go test 需要已有 adminui/dist）
make test
go test ./...
go test -race ./...  # 带竞态检测

# 运行 Bot
make run
# 或指定配置（需先 make web，否则 go:embed 找不到 dist）
go run ./cmd/qqbotd -config configs/config.yaml

# 仅构建前端
make web

# 本地事件验证（不连接 QQ）
go run ./cmd/qqbotd -config configs/config.yaml -once-event tests/testdata/mention_event.json
```

### 依赖服务

```bash
# 启动 PostgreSQL 和 NapCat
docker compose up -d

# 查看服务状态
docker compose ps

# 查看日志
docker compose logs -f

# 停止服务
docker compose down

# 健康检查
curl http://127.0.0.1:8088/healthz
```

### 架构验证

项目包含依赖方向测试，防止违反架构规则：

```bash
go test ./internal/architecture/...
```

该测试会检查：
- domain 层不依赖 application 或 adapters
- application 层不依赖 adapters
- adapters 不依赖 app

## 架构核心概念

### 六边形架构分层

项目严格遵循 Ports and Adapters 模式：

```text
cmd/qqbotd          # 程序入口
    ↓
internal/app        # Composition Root（唯一依赖组装点）
    ↓
application ——→ domain ←—— adapters
    ↓               ↑          ↓
  ports ←———————————+——————————+
    ↓
  search（无 I/O 算法内核）
```

**依赖规则**（由 `internal/architecture/layers_test.go` 强制执行）：
- `domain`: 纯业务模型，零外部依赖
- `application`: 编排业务逻辑，依赖 domain 和 ports 接口
- `adapters`: 实现 ports 接口，依赖 domain
- `app`: 创建具体实现并完成依赖注入

### Presence Runtime 与 Group Actor

**核心流程**：
```text
NapCat 事件
  → 归一化 & 去重
  → 归档到 messages 表
  → Group Actor 处理
      → 更新工作记忆
      → 生成 ThoughtCandidate
  → Presence Runtime 调度
      → 同群串行、跨群并发
      → 模型并发限制
      → 时机判断（到期、抢占、冷却）
  → Deliberation（上下文 + 检索 + Agent 规划）
  → Action Executor（校验 + 发送）
  → Reflection（记录思考、更新状态）
```

**关键特性**：
- 每个群一个 Group Actor，维护独立的工作记忆
- 同群消息串行处理，避免竞态
- 跨群可并发，模型调用有全局槽位限制
- 候选回复可过期、取消或沉默
- Actor 空闲后可回收，下次消息到达时从持久化状态恢复

### Outbox 可靠性模式

异步任务（视觉理解、向量索引、学习等）通过持久化 Outbox 执行：

```text
业务事实 + Outbox task
  ↓（同一事务）
async_outbox 表
  ↓
worker claim + lease
  ├── completed
  ├── retry（指数退避）
  └── dead_letter
```

**Outbox 任务类型**：
- `perception_event`: 图片/视频/表情包感知
- `memory_vector_index` / `meme_vector_index`: 向量投影
- `learning_extract`: 群聊增量学习
- `meme_mark_sent`: 发送计数更新

### 检索架构

统一检索入口在 `internal/application/retrieval/service.go`：

```text
query + 可见性规则
  → 硬过滤（scope / expiry / type）
  → BM25 候选 || 向量候选（可独立降级）
  → RRF 融合去重
  → 多维排序（重要性 / 置信度 / 新鲜度 / 冷却）
  → 返回结果 + trace
```

**注意**：当前 BM25 是进程内全量扫描，适合中小规模数据。生产规模增长后需持久化倒排索引。

## 目录职责与变更入口

### 按职责归属代码

新增代码时按以下顺序判断：

1. **纯业务事实/状态** → `domain/<concept>/`
   - 例：消息模型、关系状态、策略定义
2. **业务编排/用例** → `application/<capability>/`
   - 例：记忆检索、表情包推荐、Agent 规划
3. **应用需要的外部能力接口** → `application/ports/`
   - 例：MemoryStore、OutboundSender
4. **协议/数据库/模型接入** → `adapters/<technology>/`
   - 例：NapCat 适配器、PostgreSQL 实现
5. **对象创建和生命周期** → `app/`
   - 依赖注入、服务启动/关闭
6. **无 I/O 的纯算法** → `search/` 等独立内核

### 常见变更场景

| 需求 | 主要涉及目录 |
|------|--------------|
| 改变参与时机或策略 | `application/presence/`, `domain/presence/`, `domain/policy/` |
| 修改 Prompt 或工具 | `application/prompting/`, `application/tools/` |
| 调整记忆/表情检索 | `application/retrieval/`, `search/`, `adapters/storage/postgres/` |
| 改 QQ 协议接入 | `adapters/inbound/napcat/`, `adapters/outbound/napcat/` |
| 修改数据库 Schema | `schema/schema.sql`, `adapters/storage/postgres/` |
| 调整启动流程 | `app/app.go`, `cmd/qqbotd/main.go` |

## 配置与环境

### 配置文件

主配置位于 `configs/config.yaml`（从 `config.example.yaml` 复制）：

```yaml
models:           # 主模型、视觉模型、Embedding 模型
persona:          # 人格、语气、按群覆盖
default_policy:   # 默认策略
group_policies:   # 群级策略、工具白名单
autonomy:         # 主动参与概率和限流
memory:           # 记忆检索配置
meme:             # 表情包策略
tools:            # MCP、Codex 配置
storage:          # PostgreSQL 连接
qq:               # NapCat 地址、Token、群白名单
```

**重要**：`configs/config.yaml` 已被 Git 忽略，不要提交真实密钥。

### 数据库 Schema

Schema 位于 `schema/schema.sql`，启动时自动执行幂等迁移。主要表：

| 表名 | 用途 |
|------|------|
| `messages` | 消息事实（入站 + 出站） |
| `memories` | 长期记忆权威数据 |
| `meme_assets` | 表情包权威数据 |
| `group_working_memory` | 群工作记忆（可恢复的运行状态） |
| `relationships` | 群友关系投影 |
| `async_outbox` | 可靠异步任务队列 |
| `thought_records` | AI 决策记录 |
| `memory_vectors` / `meme_vectors` | pgvector 语义索引 |

## 工具与扩展

### 内置工具

Bot 提供三类工具：

1. **最终动作**（每轮只能选一个）：
   - `speak_text`, `quote_reply`, `send_meme`, `react_emoji`
   - `poke_member`（仅被戳场景）
   - `repair_message`（撤回并纠正）
   - `stay_silent`

2. **信息读取**（可多次调用）：
   - `query_memory`, `search_meme`, `query_member_profile`

3. **状态更新**（提交候选，不直接修改）：
   - `stage_memory_claim`, `record_relationship_signal`, `update_persona_fact`

### 可选扩展

- **MCP**：配置 `tools.mcp_servers`，工具名格式为 `mcp_<server>_<tool>`
- **Codex**：通过 `delegate_codex_task` 执行复杂本地任务，默认只读、需白名单

外部工具必须显式加入群的 `tool_allowlist` 才可用。

## 管理后台

启动 Bot 后访问 [http://127.0.0.1:8088/admin/](http://127.0.0.1:8088/admin/)：

- 查看身份、人格、记忆
- 监控群友关系投影
- 审查 Outbox 任务状态
- 分析检索 trace 和模型用量

前端源码在 `web/`，修改后用 `make web` 重新构建。产物输出到 `internal/app/adminui/dist/`，编译时嵌入二进制，该目录不入库。

## 重要约束与最佳实践

### 并发与可靠性

1. **入站事实先归档**：消息必须先写入 `messages` 表，再更新内存游标
2. **同群串行、跨群并发**：Group Actor 内有群锁
3. **候选必须终态化**：stale/expired/cancelled/silent/sent/error 都要记录
4. **可重放副作用入 Outbox**：进程内队列不作为事实来源
5. **关闭顺序**：Scheduler → Runtime → Outbox → 外部连接

### 代码规范

- **包拆分**：优先按职责拆包，不按"一个类型一个包"
- **单文件阈值**：约 700 行；优先拆文件，出现独立依赖边界时才拆包
- **避免 utils**：工具函数必须有明确调用方，仅被一个能力用的留在内部
- **测试覆盖**：关键业务逻辑需要单元测试，尤其是 domain 和 application 层

### 性能考虑

- BM25 是全量扫描，数据量大时考虑持久化索引
- 向量查询依赖 pgvector，注意索引类型和参数
- Outbox worker 数量可配置，避免任务积压

## 参考文档

- [架构详细说明](docs/ARCHITECTURE.md)
- [配置示例](configs/config.example.yaml)
- [测试事件数据](tests/testdata/)
- ADR（架构决策记录）见 `docs/adr/`
