# 后台查询优化方案

**日期**: 2026-09-10  
**状态**: 方案设计  
**优先级**: 低

---

## 📋 问题分析

### 1. 全量快照串行加载

**当前问题**:
```go
// 一次请求加载所有数据
type AdminSnapshot struct {
    Groups      []Group
    Personas    []Persona
    Memories    []Memory
    Relationships []Relationship
    // ... 更多
}

func GetSnapshot() (*AdminSnapshot, error) {
    // 串行加载
    groups := loadGroups()      // 1s
    personas := loadPersonas()  // 1s
    memories := loadMemories()  // 2s
    // 总计 4s+
}
```

**问题**:
- 首次加载慢（4-5 秒）
- 串行加载无法并发
- 数据全量返回，前端可能只需要部分

### 2. 前端每 3 秒刷新全量

**当前问题**:
```typescript
setInterval(() => {
    fetchSnapshot() // 获取所有数据
}, 3000)
```

**问题**:
- 不区分变化频率
- 静态数据（人格配置）也频繁刷新
- 带宽浪费

### 3. 不同数据绑在同一周期

**当前问题**:
- 群列表（变化快）
- 人格配置（几乎不变）
- 记忆数据（偶尔变化）

全部 3 秒刷新

### 4. Makefile 测试触发前端构建

**当前问题**:
```makefile
test:
    npm run build  # 每次都构建前端
    go test ./...
```

**问题**:
- 测试后端也要等前端构建
- 浪费时间（2-3 分钟）

---

## 🎯 优化目标

### 1. 轻量刷新 API

**按变化频率分类**:
- **高频** (3s): 群活跃状态、最近消息
- **中频** (30s): 关系状态、成员在线
- **低频** (5min): 人格配置、记忆总数
- **按需**: 详细数据（点击查看）

### 2. 增量更新

**只返回变化**:
```json
{
  "groups": {
    "updated": [{"id": 123, "lastMessage": "..."}],
    "deleted": [456]
  },
  "timestamp": 1234567890
}
```

### 3. 页面独立加载

**按页面拆分**:
- `/admin/groups` - 只加载群列表
- `/admin/personas` - 只加载人格列表
- `/admin/group/:id` - 只加载单个群详情

### 4. 后端测试独立

```makefile
test-backend:
    go test ./internal/... ./cmd/...
    
test-frontend:
    npm run test
    
test: test-backend test-frontend
```

---

## 🔧 实现方案

### 优化 1: 拆分 API

```go
// 轻量级状态 API（高频刷新）
// GET /api/admin/status
type SystemStatus struct {
    ActiveGroups   int       `json:"active_groups"`
    TotalMessages  int       `json:"total_messages"`
    LastActivity   time.Time `json:"last_activity"`
    Timestamp      int64     `json:"timestamp"`
}

// 群列表 API（中频刷新）
// GET /api/admin/groups?since=1234567890
type GroupListResponse struct {
    Groups    []GroupSummary `json:"groups"`
    Timestamp int64          `json:"timestamp"`
    HasMore   bool           `json:"has_more"`
}

type GroupSummary struct {
    ID            int64     `json:"id"`
    Name          string    `json:"name"`
    MemberCount   int       `json:"member_count"`
    LastMessage   string    `json:"last_message"`
    LastMessageAt time.Time `json:"last_message_at"`
}

// 群详情 API（按需）
// GET /api/admin/groups/:id
type GroupDetail struct {
    Group         Group              `json:"group"`
    Members       []Member           `json:"members"`
    RecentEvents  []Event            `json:"recent_events"`
    Scene         GroupScene         `json:"scene"`
    Relationships []Relationship     `json:"relationships"`
}

// 人格列表 API（低频）
// GET /api/admin/personas
type PersonaListResponse struct {
    Personas  []PersonaSummary `json:"personas"`
    Timestamp int64            `json:"timestamp"`
}
```

### 优化 2: 增量更新

```go
type IncrementalUpdate struct {
    Since      int64                  `json:"since"`
    Updates    map[string]interface{} `json:"updates"`
    Deletes    map[string][]int64     `json:"deletes"`
    Timestamp  int64                  `json:"timestamp"`
}

// GET /api/admin/updates?since=1234567890
func (h *AdminHandler) GetIncrementalUpdates(w http.ResponseWriter, r *http.Request) {
    since := parseTimestamp(r.URL.Query().Get("since"))
    
    updates := &IncrementalUpdate{
        Since:     since,
        Updates:   make(map[string]interface{}),
        Deletes:   make(map[string][]int64),
        Timestamp: time.Now().Unix(),
    }
    
    // 获取自 since 以来的变化
    if updatedGroups := h.getUpdatedGroups(since); len(updatedGroups) > 0 {
        updates.Updates["groups"] = updatedGroups
    }
    
    if deletedGroups := h.getDeletedGroups(since); len(deletedGroups) > 0 {
        updates.Deletes["groups"] = deletedGroups
    }
    
    json.NewEncoder(w).Encode(updates)
}
```

### 优化 3: 前端分层轮询

```typescript
// 状态栏（高频）
const statusPoller = new Poller('/api/admin/status', 3000)

// 当前页面数据（中频）
const dataPoller = new Poller('/api/admin/groups', 10000)

// 配置数据（低频或按需）
const configPoller = new Poller('/api/admin/config', 300000) // 5分钟

class Poller {
    constructor(
        private url: string,
        private interval: number,
        private incremental: boolean = false
    ) {}
    
    start() {
        this.fetch()
        this.timer = setInterval(() => this.fetch(), this.interval)
    }
    
    async fetch() {
        const url = this.incremental && this.lastTimestamp
            ? `${this.url}?since=${this.lastTimestamp}`
            : this.url
            
        const data = await fetchJSON(url)
        
        if (this.incremental) {
            this.applyIncrementalUpdate(data)
        } else {
            this.applyFullUpdate(data)
        }
        
        this.lastTimestamp = data.timestamp
    }
}
```

### 优化 4: admin.go 按资源拆分

```go
// 拆分前：一个大文件
// internal/adapter/http/admin.go (1000+ 行)

// 拆分后：按资源分文件
internal/adapter/http/admin/
├── handler.go          // 主 handler
├── groups.go           // 群组相关 API
├── personas.go         // 人格相关 API
├── memories.go         // 记忆相关 API
├── relationships.go    // 关系相关 API
└── status.go           // 状态相关 API

// handler.go
type AdminHandler struct {
    groupService       *GroupService
    personaService     *PersonaService
    memoryService      *MemoryService
    relationshipService *RelationshipService
}

func (h *AdminHandler) RegisterRoutes(r *mux.Router) {
    // 状态
    r.HandleFunc("/status", h.GetStatus).Methods("GET")
    
    // 群组
    r.HandleFunc("/groups", h.ListGroups).Methods("GET")
    r.HandleFunc("/groups/{id}", h.GetGroup).Methods("GET")
    
    // 人格
    r.HandleFunc("/personas", h.ListPersonas).Methods("GET")
    r.HandleFunc("/personas/{id}", h.GetPersona).Methods("GET")
    
    // ... 其他路由
}

// groups.go
func (h *AdminHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
    // 实现群组列表
}

func (h *AdminHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
    // 实现群组详情
}
```

### 优化 5: Makefile 拆分

```makefile
# 只测试后端
.PHONY: test-backend
test-backend:
	@echo "Running backend tests..."
	go test ./internal/... ./cmd/... -v -cover

# 只测试前端
.PHONY: test-frontend
test-frontend:
	@echo "Running frontend tests..."
	cd web && npm test

# 快速测试（只后端）
.PHONY: test
test: test-backend

# 完整测试（包括前端）
.PHONY: test-all
test-all: test-backend test-frontend

# 构建前端
.PHONY: build-frontend
build-frontend:
	@echo "Building frontend..."
	cd web && npm run build

# 开发模式（前端热重载）
.PHONY: dev-frontend
dev-frontend:
	cd web && npm run dev

# 开发模式（后端热重载）
.PHONY: dev-backend
dev-backend:
	air # 或其他热重载工具
```

---

## 📝 实现步骤

### Step 1: 拆分 admin.go（2h）

- [ ] 创建 admin/ 目录
- [ ] 按资源拆分文件
- [ ] 更新路由注册
- [ ] 测试无回归

### Step 2: 实现轻量 API（2h）

- [ ] 实现 /status API
- [ ] 实现增量更新逻辑
- [ ] 添加 timestamp 跟踪
- [ ] 编写测试

### Step 3: 前端分层轮询（1.5h）

- [ ] 创建 Poller 类
- [ ] 实现增量更新合并
- [ ] 按页面配置轮询
- [ ] 测试

### Step 4: 更新 Makefile（0.5h）

- [ ] 拆分 test 目标
- [ ] 添加 test-backend
- [ ] 添加 test-frontend
- [ ] 更新 CI 配置

### Step 5: 测试和验证（1h）

- [ ] 性能对比测试
- [ ] 带宽使用对比
- [ ] 响应时间对比

---

## 📊 效果预期

### 性能对比

| 指标 | 优化前 | 优化后 |
|------|--------|--------|
| 首屏加载 | 4-5s | 1-2s |
| 刷新耗时 | 4s（全量） | 0.1s（增量） |
| 带宽使用 | 100 KB/3s | 5 KB/3s |
| 后端测试 | 3min（含前端构建） | 30s |

### API 响应大小

| API | 数据量 |
|-----|--------|
| /status | ~500 B |
| /groups (摘要) | ~10 KB |
| /groups/:id (详情) | ~50 KB |
| /updates (增量) | ~2 KB |

---

## 📋 检查清单

- [ ] 拆分 admin.go
- [ ] 实现 /status API
- [ ] 实现增量更新
- [ ] 前端 Poller 类
- [ ] 更新 Makefile
- [ ] 性能测试
- [ ] 更新文档

---

## 🔗 相关文件

- `internal/adapter/http/admin.go` - 当前实现
- `web/src/services/api.ts` - 前端 API 调用
- `Makefile` - 测试目标

---

**预计工作量**: 6-8 小时  
**优先级**: 低（不影响核心功能）  
**依赖**: 无

---

**作者**: 架构优化团队  
**最后更新**: 2026-09-10
