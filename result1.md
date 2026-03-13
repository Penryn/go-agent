# AI 模块现状报告

## 1. Main Persona Agent

### 现状
- **实现位置**: `internal/services/prompting/agent_planner.go`
- **使用框架**: `adk.NewChatModelAgent` + `adk.ToolsConfig`（Eino ADK）
- **工具配置**: 使用 `ReturnDirectly` 标记终止动作工具（`speak_text`、`stay_silent`）
- **最大迭代**: 4 轮（`MaxIterations: 4`）
- **回退链路**: Tool 结果 → 助手文本 → DeterministicPlanner

### 与 DESIGN.md 的符合度
- ✅ 使用 `adk.ChatModelAgent + Runner` 落地（符合设计）
- ✅ 工具分为普通工具和终止动作工具（符合设计）
- ✅ 不负责限流/权限/后台学习（符合设计）
- ✅ 存在 fallback 路径到确定性计划器

### 优点
- 工具链完整：`speak_text`、`stay_silent`、`query_memory`、`search_meme`、`send_meme`、`quote_reply`、`poke_member`、`recall_recent_message`、`mark_memory_intent`
- ADK 集成规范，使用 `Runner` 管理多轮循环
- Tool allowlist 由群策略控制，不硬编码

### 限制与欠缺
- DeterministicPlanner 回退响应过于固定（"群友Bot 在，啥事"等硬编码字符串）
- Agent 运行错误静默回退到确定性计划器，无审计日志
- web_search 工具存在但未实际实现

---

## 2. Gate Agent

### 现状
- **实现位置**: `internal/services/gate/service.go`
- **使用框架**: 直接调用 `BaseChatModel.Generate`
- **输出**: 结构化 JSON（`CueBot`、`NaturalHook`、`Score`、`Reason`）
- **默认状态**: 被禁用（`llm_gate_enabled: false`），使用启发式回退

### 与 DESIGN.md 的符合度
- ✅ 直接调用 `BaseChatModel.Generate`，不走 ADK ReAct（符合设计）
- ✅ 输出受结构化 schema 约束
- ✅ 作为低成本小模型门控

### 优点
- 启发式回退逻辑合理（mention → 1.0, attachment → 0.7, question → 0.6, default → 0.1）
- 在模型不可用时不会阻塞主链路

### 限制
- JSON 输出未使用 JSON schema 强约束（依赖 LLM 自行遵守格式）
- 系统提示词中包含完整格式说明，无结构化输出模式

---

## 3. Vision Agent

### 现状
- **实现位置**: `internal/services/multimodal/service.go`
- **使用框架**: 直接调用多模态 `ChatModel`
- **输出**: `MediaDescriptor`（`Summary`、`SceneTags`、`Entities`、`EmotionHints`、`MemeSignals`、`Confidence`）

### 与 DESIGN.md 的符合度
- ✅ 直接调用多模态 ChatModel（符合设计）
- ✅ 输出结构化 `MediaDescriptor`
- ✅ 不走 ADK 工具循环

### 优点
- 每个附件独立理解，失败不影响其他附件
- 有 fallback descriptor（模型不可用时返回基础描述）

### 限制
- 未使用 `UserInputMultiContent`（DESIGN.md 推荐的 Eino 方式），而是通过 URL 传递
- 无独立预算控制（DESIGN.md 要求按 group 维度限制 Vision 预算）
- 无缓存机制（相同图片重复理解）

---

## 4. Curator / Learning

### 现状
- **Curator**: `internal/services/curator/service.go` — 使用 `compose.Graph` 构建
- **Learning**: `internal/services/learning/service.go` — 使用 `compose.Workflow` 构建
- **Review**: `internal/services/review/service.go` — 审核流水线

### 与 DESIGN.md 的符合度
- ✅ Curator 使用 `compose.Graph`（符合设计）
- ✅ Learning 使用 `compose.Workflow`（符合设计）
- ⚠️ 这三个模块均存在但**未在 bootstrap 中集成到主链路**

### 限制
- Scheduler 已初始化但未启动（`Start()` 未被调用）
- 后台定时任务未接入：Curator 提炼、Learning 扫描、Review 审核均未运行
- 这意味着长期记忆提炼、画像更新、黑话学习等后台能力当前不工作

---

## 5. Tool Runtime

### 现状
- **实现位置**: `internal/services/tools/runtime.go`
- **工具列表**: `speak_text`、`stay_silent`、`query_memory`、`search_meme`、`send_meme`、`quote_reply`、`query_member_profile`、`web_search`、`recall_recent_message`、`poke_member`、`mark_memory_intent`

### 与 DESIGN.md 的符合度
- ✅ 工具由 allowlist 控制
- ✅ 终止动作工具配置为 `ReturnDirectly`

### 优点
- 工具通过 `ToolContext` 传递会话上下文，不污染全局状态
- allowlist 支持空列表（全部允许）和指定列表

### 限制
- `web_search` 工具存在但未实现真正搜索（返回空结果或模拟）
- 工具超时和预算控制在配置中定义但未在运行时强制执行

---

## 6. Prompt 组织方式

### 现状
- **实现位置**: `internal/services/prompting/composer.go`
- **层次结构**: 长期人格层 → 群策略层 → 回合任务层 → 输出约束层

### 与 DESIGN.md 的符合度
- ✅ 多层 prompt 组装（符合设计：Prompt Composer 拼装多层 prompt）
- ✅ 人格信息（名称、描述、风格）注入 system instruction

### 优点
- Prompt 结构清晰，四层分离
- 上下文快照信息（最近对话、记忆、媒体摘要）组装到 user message

### 限制
- PersonaState（动态状态：情绪、能量）未注入 prompt
- RelationshipState（与特定用户的关系）未注入 prompt
- 媒体描述仅使用 Summary，丢失了 SceneTags/Entities/EmotionHints 等细节

---

## 7. Model Provider Wiring

### 现状
- **实现位置**: `internal/adapters/model/factory.go`
- **支持 Provider**: `ark`（默认）、`openai_compat`
- **缓存**: 已创建的模型实例会被缓存

### 与 DESIGN.md 的符合度
- ✅ 四个模型位（Main、Gate、Vision、Embedding）均有配置位
- ✅ API Key 通过环境变量注入，不写入 YAML
- ⚠️ Embedding 模型已配置但**未被使用**（无 embedding provider 被创建）

### 优点
- Provider 归一化逻辑完善（多种 OpenAI 兼容名称均可识别）
- 超时配置可定制，有合理默认值
- Warmup 机制在启动时验证模型可达

### 限制
- Embedding Provider 未实现（Qdrant 向量索引链路未打通）
- 模型缓存在创建失败时也会缓存错误（需要重启才能恢复）
- Warmup 仅初始化模型，未做真实测试请求

---

## 当前整体与 DESIGN.md 的符合度总结

| 模块 | 符合度 | 说明 |
|------|--------|------|
| Main Persona Agent | 🟢 高 | ADK 集成正确，工具完整，有回退链路 |
| Gate Agent | 🟢 高 | 直接调用模型，有启发式回退 |
| Vision Agent | 🟡 中 | 功能实现但缺少预算控制和缓存 |
| Curator/Learning | 🔴 低 | 代码存在但未集成到运行时 |
| Tool Runtime | 🟡 中 | 框架完整但部分工具未实现 |
| Prompt Composer | 🟡 中 | 多层结构正确但部分上下文未注入 |
| Model Provider | 🟡 中 | 前台模型完整但 Embedding 未接通 |
