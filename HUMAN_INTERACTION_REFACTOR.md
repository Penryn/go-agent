# 真人交互改造方案

## 目标

本轮改造聚焦四件事：

1. 接通事件后的画像与记忆学习链。
2. 让群 Actor 的工作状态在进程重启后可恢复。
3. 将“思考”从一次性回复规划升级为可审计的结构化认知记录。
4. 借鉴 Koishi 的 `sendQueued()`，让多气泡回复具有可取消、可调节的发送节奏。

暂不实现 follow-up 唤醒；`OpenLoops` 继续作为上下文信息保留。

## 目标链路

```text
Inbound
  -> Normalize
  -> Archive + Group Actor
  -> Profile Projection
  -> Thought Candidate
  -> Presence Claim
  -> Deliberation
       -> ThoughtRecord
       -> ReplyPlan
  -> Rhythm Action Queue
  -> Outbound
  -> Reflection
       -> Curator
       -> Review
       -> Memory / Profile
```

## 改造内容

### 1. 学习链

- 每条有效入站事件调用 `profile.Service.ObserveEvent`。
- 每次完成审议和出站动作后异步运行 `curator.Service`。
- Curator 结果统一经过 `review.Service.ApplyCurator`，再写入 MemoryService。
- 既有定时 Learning 保留，用于群黑话和高频表达的批处理提炼。

### 2. Actor 状态恢复

- 新增 `WorkingMemoryStore` 投影接口。
- Actor 创建时加载最近的 `GroupWorkingMemory`。
- observe、claim、complete、media enrichment 后保存状态。
- MySQL 使用 `group_working_memory` 表；in-memory store 用于测试。
- Event Log 仍是事实来源，工作状态是可重建的运行时投影。

### 3. 结构化认知

- 新增 `ThoughtRecord`，记录候选、解释、证据、置信度、动作和结果。
- 不保存模型的原始 chain-of-thought，不把隐藏推理暴露给用户。
- ThoughtRecord 单独存储，便于调试、评估和后续学习。

### 4. 输出节奏

- Reply 计划包含多个 bubble 时，按队列逐条发送。
- 每条之间使用可配置延迟，新的同群发送会取消上一条尚未发送的队列。
- quote 只附在第一条 bubble 上；最终 receipt 代表最后一条实际发送结果。
- meme、react、recall、poke 保持单动作发送。

## 不在本轮范围

- follow-up 定时唤醒和承诺管理。
- 跨平台用户 binding。
- 插件热加载和控制台。
- 将所有模块改造成 Agent；Runtime Kernel 仍由确定性代码负责。

## 验收标准

- 新消息能更新 member profile，完成回合能产生 curator memory。
- 重启后同群 Actor 能恢复 recent tail、candidate 和 media enrichment。
- 每个完成的 deliberation 都有可检索的 ThoughtRecord。
- 两条以上文本 bubble 会按间隔分开发送，新消息可取消未发送 bubble。
- `go test ./...` 全部通过，单条回复行为不发生回归。
