# 生成与上下文系统统一方案

**日期**: 2026-09-10  
**任务**: 3/8 - 高优先级

---

## 📋 当前问题分析

### 1. 两套生成系统并存

**AgentPlanner** (`agent_planner.go`):
- ✅ 完整的工具运行时集成
- ✅ 支持多轮对话和工具调用
- ✅ 会话管理 (PromptSessionStore)
- ✅ 上下文快照 (ContextSnapshot)
- ✅ 媒体描述符、检索记忆
- ❌ 创建后被丢弃 (`_ = NewAgentPlanner(...)`)

**Composer** (`composer.go`):
- ✅ 简单的 Prompt 构建
- ✅ 单次 LLM 调用
- ✅ 记忆检索集成
- ❌ 没有工具支持
- ❌ 没有会话管理
- ❌ PersonaContext 未使用
- ❌ 群聊历史未接入
- ❌ 媒体上下文未接入

**ResponsePlanner** (`response_planner.go`):
- ✅ 调用 Composer 生成文本
- ✅ 创建结构化计划
- ❌ 只传递单个事件，不传历史

### 2. 上下文信息碎片化

**ContextSnapshot** (AgentPlanner 使用):
```go
type ContextSnapshot struct {
    Event             ConversationEvent
    History           []ConversationEvent    // ✅ 群聊历史
    MediaDescriptors  []MediaDescriptor      // ✅ 媒体上下文
    RelevantMemories  []MemoryRecord         // ✅ 检索记忆
    GroupPolicy       GroupPolicy
    // ... 更多字段
}
```

**PersonaContext** (未使用):
```go
type PersonaContext struct {
    Posture           EphemeralPosture
    Mood              GlobalMood
    EnergyLevel       float64
    RecentActivities  []Activity
    GroupContext      GroupContext
    // ... 更多字段
}
```

**当前 Composer** 只接收：
- `PersonaContext` (忽略)
- `ConversationEvent` (单个事件)
- `intent` (字符串)

### 3. 功能缺失

- ❌ 工具调用未接入当前流程
- ❌ 群聊历史未传递
- ❌ 媒体上下文未传递
- ❌ 人格状态未使用
- ❌ 失败时返回 intent 字符串 (如 "direct_answer")

---

## 🎯 统一方案

### 阶段 1: 增强 Composer（保守方案）

**目标**: 在现有 Composer 基础上增强，避免大规模重构

**步骤**:

1. **接入 PersonaContext**
   ```go
   func (c *Composer) buildResponsePrompt(
       personaCtx *personadomain.PersonaContext,  // 使用它！
       evt *conversationdomain.ConversationEvent,
       history []conversationdomain.ConversationEvent, // 新增
       memories []memorydomain.MemoryRecord,
   ) string
   ```

2. **添加群聊历史参数**
   - 从 WorkingMemory 获取最近 N 条消息
   - 格式化为对话上下文

3. **添加媒体上下文**
   - 传递媒体描述符
   - 在 Prompt 中说明图片/表情含义

4. **使用人格状态**
   - 在 Prompt 中说明当前心情、精力
   - 影响回复风格

5. **改进错误处理**
   - 失败时返回友好的错误提示
   - 不返回内部 intent 字符串

### 阶段 2: 工具集成（可选）

**如果需要工具调用**:

1. **创建统一入口**
   ```go
   type UnifiedGenerator struct {
       composer     *Composer      // 普通对话
       agentPlanner *AgentPlanner  // 工具任务
   }
   ```

2. **根据场景选择**
   - 普通聊天 → Composer
   - 需要工具 → AgentPlanner
   - 决策逻辑：检查 GroupPolicy.ToolAllowlist

---

## 📐 实施计划

### Step 1: 增强 Composer 接口

**修改**:
1. `ComposeResponse` 添加 `history` 参数
2. `buildResponsePrompt` 使用 PersonaContext
3. 添加 `buildConversationHistory` 辅助方法
4. 添加 `buildPersonaState` 辅助方法

### Step 2: 更新 ResponsePlanner

**修改**:
1. 从 WorkingMemory 获取历史
2. 传递给 Composer
3. 改进错误处理

### Step 3: 集成测试

**验证**:
1. 历史正确传递
2. PersonaContext 生效
3. 媒体上下文生效
4. 错误处理正确

### Step 4: 清理 AgentPlanner（可选）

**如果不需要工具**:
- 删除 AgentPlanner 创建
- 删除工具运行时初始化
- 简化依赖

**如果需要工具**:
- 保留 AgentPlanner
- 创建统一入口
- 根据场景路由

---

## 🔍 关键决策点

### 决策 1: 是否需要工具调用？

**如果是**:
- 保留 AgentPlanner
- 创建路由层
- 工具场景使用 AgentPlanner
- 普通场景使用 Composer

**如果否**:
- 删除 AgentPlanner
- 删除工具运行时
- 只用增强的 Composer

### 决策 2: 历史长度？

**建议**:
- 最近 10-20 条消息
- 或最近 5 分钟内
- 字符预算：2000-3000 字符
- 优先保留：@机器人、回复机器人的消息

### 决策 3: PersonaContext 使用哪些字段？

**建议使用**:
- `Posture.Stance`: 当前姿态（友好/中立/疏远）
- `Mood.Valence`: 情绪倾向
- `EnergyLevel`: 精力水平
- `RecentActivities`: 最近活动摘要

**可选**:
- `GroupContext`: 群氛围
- `Constraints`: 互动约束（已由 ConstraintIntegration 处理）

---

## 📊 影响评估

### 优点

✅ 渐进式改进，风险低
✅ 保留现有架构
✅ 历史上下文提升质量
✅ 人格状态更真实
✅ 错误处理更友好

### 缺点

⚠️ 如果后续需要工具，需要再次重构
⚠️ AgentPlanner 的投入未利用

### 建议

**短期**: 增强 Composer（Step 1-3）
**中期**: 根据实际需求决定是否接入工具
**长期**: 如果工具成为核心，再统一到 AgentPlanner

---

## 🚀 实施顺序

1. ✅ **Step 1.1**: 添加历史参数到 Composer
2. ✅ **Step 1.2**: 使用 PersonaContext
3. ✅ **Step 1.3**: 改进 Prompt 构建
4. ✅ **Step 2**: 更新 ResponsePlanner
5. ✅ **Step 3**: 集成测试
6. ⏸️ **Step 4**: 决策工具集成（待定）

---

## 📝 待确认

❓ **工具调用是否是核心功能？**
- 如果是 → 统一到 AgentPlanner
- 如果否 → 只用 Composer

❓ **历史长度偏好？**
- 建议: 最近 15 条或 5 分钟

❓ **媒体上下文优先级？**
- 高 → 立即接入
- 低 → 后续迭代

---

**下一步**: 开始实施 Step 1.1 - 增强 Composer 接口
