# 记忆学习重构 - 快速开始指南

## 概述

本指南说明如何使用已实现的核心组件。当前状态：**基础层已完成，应用层待集成**。

## 已实现组件使用示例

### 1. 初始化 Memory Store 和 Service

```go
import (
    "go-agent/internal/adapters/storage/postgres"
    "go-agent/internal/application/memory"
    "github.com/jmoiron/sqlx"
)

// 在 app.go 中初始化
func initializeMemorySystem(db *sqlx.DB) memory.Service {
    // 创建 Store
    memStore := postgres.NewMemoryStore(db)
    
    // 创建 Service
    memService := memory.NewService(memStore)
    
    return memService
}
```

### 2. 应用记忆候选（学习服务调用）

```go
// 从学习窗口生成候选
candidates := []*memory.MemoryCandidate{
    {
        // 用户偏好
        Scope:            "group_123456",
        SubjectKind:      memory.SubjectKindUser,
        SubjectID:        "user_789",
        Type:             memory.MemoryTypeSemantic,
        Subtype:          "preference",
        Content:          "张三喜欢乌龙茶",
        Predicate:        memory.PredicateLikesTopic,
        NormalizedValue:  "乌龙茶",
        Qualifier:        "default",
        EvidenceEventIDs: []string{"event_001"},
        SourceRole:       "primary",
        Intent:           "new",
        ExtractorVersion: "v1",
        ObservedAt:       time.Now(),
    },
    {
        // 称呼偏好
        Scope:            "group_123456",
        SubjectKind:      memory.SubjectKindUser,
        SubjectID:        "user_789",
        Type:             memory.MemoryTypeSocial,
        Subtype:          "nickname",
        Content:          "张三要求叫他老张",
        Predicate:        memory.PredicatePreferredName,
        NormalizedValue:  "老张",
        Qualifier:        "default",
        EvidenceEventIDs: []string{"event_002"},
        SourceRole:       "primary",
        Intent:           "new",
        ExtractorVersion: "v1",
        ObservedAt:       time.Now(),
    },
    {
        // 互动边界（临时）
        Scope:            "group_123456",
        SubjectKind:      memory.SubjectKindUser,
        SubjectID:        "user_789",
        Type:             memory.MemoryTypeSocial,
        Subtype:          "boundary",
        Content:          "今晚别 @ 我",
        Predicate:        memory.PredicateAllowMention,
        NormalizedValue:  "false",
        Qualifier:        "2026-09-10T18:00:00Z/2026-09-11T08:00:00Z",
        ValidUntil:       &validUntil, // 明天早上8点
        EvidenceEventIDs: []string{"event_003"},
        SourceRole:       "primary",
        Intent:           "new",
        ExtractorVersion: "v1",
        ObservedAt:       time.Now(),
    },
    {
        // 情景记忆（共同经历）
        Scope:            "group_123456",
        SubjectKind:      memory.SubjectKindGroup,
        SubjectID:        "group_123456",
        Type:             memory.MemoryTypeEpisodic,
        Subtype:          "discussion",
        Content:          "大家讨论了周末去哪里玩，最后决定去爬山",
        ParticipantIDs:   []string{"user_789", "user_790", "bot_self"},
        BotRole:          memory.BotRoleParticipant,
        AnchorEventID:    "event_004",
        EvidenceEventIDs: []string{"event_004", "event_005", "event_006"},
        SourceRole:       "primary",
        Intent:           "new",
        ExtractorVersion: "v1",
        ObservedAt:       time.Now(),
    },
}

// 应用候选
results, err := memService.ApplyCandidates(ctx, candidates)
if err != nil {
    return err
}

// 检查结果
for i, result := range results {
    if result.Success {
        log.Printf("Candidate %d saved as %s (status: %s)",
            i, result.MemoryID, result.Status)
    } else {
        log.Printf("Candidate %d failed: %s", i, result.Error)
    }
}
```

### 3. 获取约束（发送前检查）

```go
// 在 ResponseExecutor 或 ActionValidator 中
func (e *Executor) checkConstraints(ctx context.Context, action *Action) error {
    // 获取目标用户的约束
    constraints, err := e.memService.GetConstraints(ctx, 
        action.GroupID, 
        []string{action.TargetUserID})
    if err != nil {
        return err
    }
    
    // 检查约束
    for _, constraint := range constraints {
        if !constraint.IsValid() {
            continue // 已失效
        }
        
        switch constraint.Type {
        case "preferred_name":
            // 使用正确的称呼
            action.PreferredName = constraint.Value
            
        case "allow_mention":
            if constraint.Value == "false" && action.Type == "mention" {
                return fmt.Errorf("user %s does not allow mentions", 
                    constraint.SubjectID)
            }
            
        case "allow_poke":
            if constraint.Value == "false" && action.Type == "poke" {
                return fmt.Errorf("user %s does not allow pokes", 
                    constraint.SubjectID)
            }
        }
    }
    
    return nil
}
```

### 4. 检索相关记忆（Composer 使用）

```go
// 注意：GetRelevantMemories 当前返回空列表，需要集成 retrieval service

func (c *Composer) buildContext(ctx context.Context, req *ComposeRequest) error {
    // 获取约束（必读）
    constraints, err := c.memService.GetConstraints(ctx, 
        req.GroupID, 
        req.MentionedUserIDs)
    if err != nil {
        return err
    }
    
    // 检索相关记忆（按需）
    memories, err := c.memService.GetRelevantMemories(ctx, &memory.RetrievalRequest{
        Scope:          req.GroupID,
        Query:          req.Query,
        TargetIDs:      req.MentionedUserIDs,
        Limit:          10,
        ExcludeExpired: true,
    })
    if err != nil {
        return err
    }
    
    // 构建上下文
    memCtx := &memory.MemoryContext{
        Constraints:      constraints,
        RelevantMemories: memories,
    }
    
    // 注入到 Prompt
    c.injectMemoryContext(req.Prompt, memCtx)
    
    return nil
}
```

### 5. 更正记忆

```go
// 用户说："不对，我不是喜欢乌龙茶，是喜欢红茶"
correction := &memory.MemoryCandidate{
    Scope:            "group_123456",
    SubjectKind:      memory.SubjectKindUser,
    SubjectID:        "user_789",
    Type:             memory.MemoryTypeSemantic,
    Subtype:          "preference",
    Content:          "张三喜欢红茶",
    Predicate:        memory.PredicateLikesTopic,
    NormalizedValue:  "红茶",
    Qualifier:        "default",
    EvidenceEventIDs: []string{"event_007"}, // 更正消息
    SourceRole:       "primary",
    Intent:           "correct",
    SupersedesID:     oldMemoryID, // 要更正的记忆ID
    ExtractorVersion: "v1",
    ObservedAt:       time.Now(),
}

results, err := memService.ApplyCandidates(ctx, []*memory.MemoryCandidate{correction})
// 旧记忆会被标记为 superseded，新记忆成为 active
```

### 6. 遗忘记忆

```go
// 用户说："忘记我喜欢什么茶"
err := memService.ForgetMemory(ctx, memoryID, "user requested", userID)
if err != nil {
    return err
}
// 记忆被标记为 revoked，revision++
// 所有使用该记忆的缓存和计划都会失效
```

### 7. 查询记忆和证据

```go
// 获取完整记忆信息（用于调试或展示）
memWithSource, err := memService.GetMemoryWithEvidence(ctx, memoryID)
if err != nil {
    return err
}

fmt.Printf("Memory: %s\n", memWithSource.Content)
fmt.Printf("Evidence: %d pieces from %s to %s\n",
    memWithSource.EvidenceCount,
    memWithSource.FirstObservedAt.Format("2006-01-02"),
    memWithSource.LastObservedAt.Format("2006-01-02"))
fmt.Printf("Source: %s\n", memWithSource.SourceSummary)

// 查看证据详情
for _, eventID := range memWithSource.EvidenceEventIDs {
    fmt.Printf("  - Event: %s\n", eventID)
}
```

## 数据库操作示例

### 手动查询记忆

```sql
-- 查询某用户在某群的所有有效记忆
SELECT memory_id, type, subtype, content, predicate, normalized_value
FROM memories
WHERE scope = 'group_123456'
  AND subject_kind = 'user'
  AND subject_id = 'user_789'
  AND status = 'active'
  AND (valid_until IS NULL OR valid_until > NOW())
ORDER BY last_observed_at DESC;

-- 查询某条记忆的证据
SELECT e.event_id, e.source_role, e.added_at, m.text_content
FROM memory_evidence e
JOIN messages m ON e.event_id = m.event_id
WHERE e.memory_id = 'memory_xxx'
ORDER BY e.added_at;

-- 查询某条记忆的变更历史
SELECT change_kind, reason, operator_kind, from_revision, to_revision, created_at
FROM memory_changes
WHERE memory_id = 'memory_xxx'
ORDER BY created_at DESC;

-- 查询未处理的事件
SELECT m.event_id, m.occurred_at, m.text_content
FROM messages m
WHERE m.group_id = 123456
  AND NOT EXISTS (
    SELECT 1 FROM learning_event_progress lep
    WHERE lep.event_id = m.event_id
      AND lep.extractor_version = 'v1'
  )
ORDER BY m.occurred_at ASC
LIMIT 50;
```

## 集成到现有代码

### 在 app.go 中注册

```go
// internal/app/app.go

type App struct {
    // ... 现有字段
    memoryService memory.Service
}

func NewApp(config *Config) (*App, error) {
    // ... 现有初始化
    
    // 初始化记忆系统
    memStore := postgres.NewMemoryStore(db)
    memService := memory.NewService(memStore)
    
    app := &App{
        // ... 现有字段
        memoryService: memService,
    }
    
    // 将 memService 传递给需要的组件
    // - LearningService（生成候选）
    // - Composer（读取约束和检索）
    // - ResponseExecutor（发送前校验）
    
    return app, nil
}
```

### 在学习服务中使用

```go
// internal/application/learning/service.go

type Service struct {
    store         Store
    memoryService memory.Service // 新增
}

func (s *Service) ProcessWindow(ctx context.Context, window *Window) error {
    // 1. 从窗口提炼候选
    candidates := s.extractCandidates(window)
    
    // 2. 应用候选
    results, err := s.memoryService.ApplyCandidates(ctx, candidates)
    if err != nil {
        return err
    }
    
    // 3. 记录进度
    successCount := 0
    for _, r := range results {
        if r.Success {
            successCount++
        }
    }
    
    // 4. 投递向量索引任务（TODO）
    
    return nil
}
```

## 测试示例

### 单元测试

```go
// internal/application/memory/service_test.go

func TestApplyCandidates_New(t *testing.T) {
    // Mock store
    store := &mockStore{
        memories: make(map[string]*memory.Memory),
    }
    
    service := memory.NewService(store)
    
    candidates := []*memory.MemoryCandidate{
        {
            Scope:            "group_1",
            SubjectKind:      memory.SubjectKindUser,
            SubjectID:        "user_1",
            Type:             memory.MemoryTypeSemantic,
            Content:          "test content",
            EvidenceEventIDs: []string{"event_1"},
            Intent:           "new",
            ExtractorVersion: "v1",
            ObservedAt:       time.Now(),
        },
    }
    
    results, err := service.ApplyCandidates(context.Background(), candidates)
    
    assert.NoError(t, err)
    assert.Len(t, results, 1)
    assert.True(t, results[0].Success)
    assert.NotEmpty(t, results[0].MemoryID)
}
```

### 集成测试

```go
// internal/adapters/storage/postgres/memory_store_test.go
// +build integration

func TestMemoryStore_SaveAndGet(t *testing.T) {
    db := setupTestDB(t)
    defer cleanupTestDB(t, db)
    
    store := NewMemoryStore(db)
    
    mem := &memory.Memory{
        MemoryID:         uuid.New().String(),
        Scope:            "group_test",
        SubjectKind:      memory.SubjectKindUser,
        SubjectID:        "user_test",
        Type:             memory.MemoryTypeSemantic,
        Content:          "test memory",
        Status:           memory.MemoryStatusActive,
        Revision:         1,
        FirstObservedAt:  time.Now(),
        LastObservedAt:   time.Now(),
        SourceKind:       memory.SourceKindExtraction,
        ExtractorVersion: "v1",
        CreatedAt:        time.Now(),
        UpdatedAt:        time.Now(),
    }
    
    evidence := []memory.Evidence{
        {
            MemoryID:   mem.MemoryID,
            EventID:    "event_1",
            SourceRole: "primary",
            AddedAt:    time.Now(),
        },
    }
    
    change := &memory.Change{
        ChangeID:     uuid.New().String(),
        MemoryID:     mem.MemoryID,
        ChangeKind:   "created",
        FromRevision: 0,
        ToRevision:   1,
        CreatedAt:    time.Now(),
    }
    
    err := store.Save(context.Background(), mem, evidence, change)
    assert.NoError(t, err)
    
    retrieved, err := store.Get(context.Background(), mem.MemoryID)
    assert.NoError(t, err)
    assert.Equal(t, mem.Content, retrieved.Content)
}
```

## 常见问题

### Q: 如何执行 Schema 迁移？

```bash
# 1. 备份现有数据
docker compose exec postgres pg_dump -U postgres -d qqbot \
  -t memories -t learning_candidates > backup.sql

# 2. 执行迁移
docker compose exec -T postgres psql -U postgres -d qqbot \
  < schema/migrations/001_memory_refactor.sql

# 3. 验证
docker compose exec postgres psql -U postgres -d qqbot \
  -c "\d memories"
```

### Q: GetRelevantMemories 为什么返回空？

当前实现中该方法返回空列表，因为需要集成 retrieval service（BM25/vector/RRF）。这是下一步的工作。

### Q: 如何处理并发冲突？

使用 revision 检查：
```go
// 读取时记录 revision
oldRevision := mem.Revision

// 修改
mem.Content = "new content"
mem.Revision++

// 保存时检查（在 Store 层实现）
// 如果数据库中的 revision != oldRevision，拒绝保存
```

### Q: 遗忘后数据真的删除了吗？

记忆被标记为 `revoked`，但不会立即物理删除。向量索引会被清理，但原始记录保留用于审计。如需完全删除，需要额外的清理任务。

## 下一步

1. **实现学习服务窗口提炼** - 生成候选的核心逻辑
2. **集成检索服务** - 实现 GetRelevantMemories
3. **Composer 集成** - 完整上下文组装
4. **端到端测试** - 验证完整流程

参考 `docs/REFACTOR_SUMMARY.md` 了解完整实施计划。
