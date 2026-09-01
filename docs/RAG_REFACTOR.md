# RAG 重构设计

状态：第一阶段已落地，生产 BM25 索引仍需按数据规模选型

## 目标

本项目的 RAG 面向两类短文本事实：长期记忆和表情包描述。当前实现已经有 pgvector 语义检索，但检索编排分散在 `context`、`meme` 和工具路径中。目标是统一为：

```text
query + session visibility
        -> normalize
        -> BM25 candidates || vector candidates
        -> RRF
        -> hard filter + authoritative hydrate
        -> domain ranking
        -> context/tool result
```

BM25 和向量只负责候选召回。RRF 只比较 rank，不直接相加 BM25 分数和 cosine 分数。权限、过期、状态等硬过滤必须在召回 adapter 中尽量前置；重要性衰减、群范围偏好、冷却和哑弹率属于最终业务排序。

## 目标模块

### `internal/search/bm25`

纯 Go、无存储依赖的 BM25 implementation。默认 tokenizer 面向中文短文本：保留英文/数字词，中文按字符和相邻双字片段生成 token。tokenizer 是 implementation 细节，未来可以替换为更成熟的中文 analyzer。

### `internal/application/retrieval`

统一混合检索 module，负责：

- 规范化 query 和候选数量
- 从 session 派生 memory visibility
- 并行调用 BM25 和 vector adapter
- RRF、去重和候选排序
- 失败降级和结果来源标记
- 将完整字段回查交给权威 store

当前 Store 直接对可见语料执行进程内 BM25，适合验证行为和小规模语料，
不应直接当作大规模生产倒排索引。

memory、meme 和 `query_memory` 只能提供领域查询条件，不再各自复制混合召回骨架。

### Projection

`memories`、`meme_assets` 和 descriptor 表是权威事实。BM25/vector index 都是可重建 projection，必须携带 source revision、content hash 和 model/profile 信息。memory 和 PostgreSQL meme 写入都已经支持事实与 vector outbox 的原子提交；仍需补齐 index 健康检查、reconcile/backfill 和 dead-letter 运维能力。

## 检索规则

### Memory

- hard filter：global/current group/current user、type、expiry
- candidate：BM25 + vector，各取最终 top-k 的 4 到 10 倍
- fusion：RRF
- soft rank：importance、confidence、time decay
- prompt：只注入最终预算内的结果，并保留来源元数据供 trace 使用

### Meme

- hard filter：approved、group/global、emotion/scene
- candidate：BM25 + vector，不能因为 vector 返回候选就跳过 BM25
- fusion：RRF
- soft rank：group preference、cooldown、dud rate
- 过滤后为空时扩大候选或执行 lexical fallback

## 中文 BM25 部署决策

当前 `pgvector/pg17` 镜像只保证向量能力，不自动提供严格 BM25 或中文分词。第一阶段使用 Store 内的 Go implementation 验证检索行为；生产部署前需要用真实中文 query 比较：

1. PostgreSQL-compatible BM25 extension + Chinese analyzer
2. 独立或嵌入式 lexical index
3. 中文分词后写入 PostgreSQL FTS 的过渡方案

如果使用 PostgreSQL `ts_rank`，文档中必须称为 FTS 近似，而不是严格 BM25。

## 实施阶段

1. [x] 修复 scope 可见性，统一 `query_memory` 和自动上下文的 memory 查询；补充 meme 过滤后 fallback。
2. [x] 接入 `retrieval` module，替换 context、meme 和 tool 的重复编排。
3. [x] 将现有关键词查询替换为 BM25，增加 BM25/vector/RRF 测试。
4. [x] 为 memory/meme projection 增加 revision 和可用的原子 outbox 路径。
5. [ ] 增加 retrieval trace 和中文 golden set，校准 candidate-k、RRF 权重、threshold 和上下文预算。
6. [ ] 按 golden set 结果选择 PostgreSQL BM25 extension、独立 lexical index 或 FTS 过渡方案，并实现 durable lexical projection。

第一阶段不引入 reranker，也不对短事实强行 chunking。未来接入外部知识库时，另建 document/chunk projection，不污染现有 memory domain。

## 验收标准

- 跨群 `scope` 不可通过工具或 adapter 读取
- context、`query_memory` 和 meme 搜索共享同一混合召回规则
- BM25/vector 任一失败时仍有明确的可用降级语义
- RRF 结果稳定、去重、有限且可解释
- memory/meme 的索引缺失或陈旧时可以重建
- 覆盖权限、fallback、重复任务、重启恢复和真实中文 query 质量测试

## 已知限制

- 当前 BM25 adapter 为全量扫描，复杂度随可见语料线性增长；必须在生产数据量上升前替换为持久化倒排索引或数据库侧实现。
- RRF 只做候选融合，不保证最终业务质量；候选召回之后仍由 memory importance/time decay 和 meme cooldown/dud rate 做领域排序。
- meme 的向量查询按群和 global 分轨后按相似度重新合并，再进入统一 RRF；不能把两次独立查询的 rank 直接拼接。
