package app

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/config"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

type adminDashboard struct {
	db         *sql.DB
	state      ports.RuntimeStateStore
	facts      ports.PersonaFactStore
	definition personadomain.PersonaDefinition
	cfg        config.Config
}

type adminSnapshot struct {
	UpdatedAt     time.Time           `json:"updated_at"`
	SelectedGroup int64               `json:"selected_group"`
	Status        adminStatus         `json:"status"`
	Stats         adminStats          `json:"stats"`
	Persona       adminPersona        `json:"persona"`
	Groups        []adminGroup        `json:"groups"`
	Memories      []adminMemory       `json:"memories"`
	Relationships []adminRelationship `json:"relationships"`
	Activity      []adminActivity     `json:"activity"`
}

type adminStatus struct {
	Mode      string `json:"mode"`
	QQEnabled bool   `json:"qq_enabled"`
	SelfID    int64  `json:"self_id"`
}

type adminStats struct {
	Groups       int `json:"groups"`
	Members      int `json:"members"`
	Memories     int `json:"memories"`
	PendingTasks int `json:"pending_tasks"`
}

type adminPersona struct {
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Mood        string                      `json:"mood"`
	Energy      string                      `json:"energy"`
	TalkBias    float64                     `json:"talk_bias"`
	Runtime     policydomain.RuntimeState   `json:"runtime"`
	Facts       []personadomain.PersonaFact `json:"facts"`
	Interests   []string                    `json:"interests"`
}

type adminGroup struct {
	GroupID      int64     `json:"group_id"`
	Messages     int       `json:"messages"`
	Members      int       `json:"members"`
	ActiveTopic  string    `json:"active_topic"`
	LastActivity time.Time `json:"last_activity"`
}

type adminMemory struct {
	ID         string     `json:"id"`
	Scope      string     `json:"scope"`
	Type       string     `json:"type"`
	Subject    string     `json:"subject"`
	Content    string     `json:"content"`
	Confidence float64    `json:"confidence"`
	Importance float64    `json:"importance"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type adminRelationship struct {
	GroupID        int64     `json:"group_id"`
	UserID         int64     `json:"user_id"`
	Name           string    `json:"name"`
	Affinity       float64   `json:"affinity"`
	Familiarity    float64   `json:"familiarity"`
	TeaseTolerance float64   `json:"tease_tolerance"`
	GrudgeScore    float64   `json:"grudge_score"`
	MessageCount   int64     `json:"message_count"`
	LastInteractAt time.Time `json:"last_interact_at"`
}

type adminActivity struct {
	At      time.Time `json:"at"`
	GroupID int64     `json:"group_id"`
	Type    string    `json:"type"`
	Label   string    `json:"label"`
	Subject string    `json:"subject"`
	Detail  string    `json:"detail"`
}

func newAdminHandler(db *sql.DB, state ports.RuntimeStateStore, facts ports.PersonaFactStore, definition personadomain.PersonaDefinition, cfg config.Config) http.Handler {
	dashboard := &adminDashboard{db: db, state: state, facts: facts, definition: definition, cfg: cfg}
	assets, _ := fs.Sub(adminAssets, "adminui/dist")
	return &adminHandler{
		token:  strings.TrimSpace(cfg.Server.AdminToken),
		load:   dashboard.snapshot,
		assets: http.StripPrefix("/admin/", http.FileServer(http.FS(assets))),
	}
}

type adminHandler struct {
	token  string
	load   func(context.Context, int64) (adminSnapshot, error)
	assets http.Handler
}

//go:embed adminui/dist
var adminAssets embed.FS

func (h *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin":
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	case "/admin/api/snapshot":
		h.handleSnapshot(w, r)
	default:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/admin/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self' data:")
		h.assets.ServeHTTP(w, r)
	}
}

func (h *adminHandler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="bot-admin"`)
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	if r.URL.Query().Get("group_id") != "" && (err != nil || groupID < 0) {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	snapshot, err := h.load(ctx, groupID)
	if err != nil {
		http.Error(w, "load dashboard: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (h *adminHandler) authorized(r *http.Request) bool {
	if h.token == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		return net.ParseIP(host).IsLoopback()
	}
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return len(provided) == len(h.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

func (d *adminDashboard) snapshot(ctx context.Context, selectedGroup int64) (adminSnapshot, error) {
	groups, err := d.loadGroups(ctx)
	if err != nil {
		return adminSnapshot{}, err
	}
	if selectedGroup == 0 && len(groups) > 0 {
		selectedGroup = groups[0].GroupID
	}
	stats, err := d.loadStats(ctx)
	if err != nil {
		return adminSnapshot{}, err
	}
	persona, err := d.loadPersona(ctx, selectedGroup)
	if err != nil {
		return adminSnapshot{}, err
	}
	memories, err := d.loadMemories(ctx, selectedGroup)
	if err != nil {
		return adminSnapshot{}, err
	}
	relationships, err := d.loadRelationships(ctx, selectedGroup)
	if err != nil {
		return adminSnapshot{}, err
	}
	activity, err := d.loadActivity(ctx, selectedGroup)
	if err != nil {
		return adminSnapshot{}, err
	}
	return adminSnapshot{
		UpdatedAt: time.Now(), SelectedGroup: selectedGroup,
		Status: adminStatus{Mode: d.cfg.App.Mode, QQEnabled: d.cfg.QQ.Enabled, SelfID: d.cfg.QQ.SelfID},
		Stats:  stats, Persona: persona, Groups: groups, Memories: memories,
		Relationships: relationships, Activity: activity,
	}, nil
}

func (d *adminDashboard) loadGroups(ctx context.Context) ([]adminGroup, error) {
	rows, err := d.db.QueryContext(ctx, `
		WITH ids AS (
			SELECT group_id FROM messages UNION SELECT group_id FROM member_profiles
			UNION SELECT group_id FROM relationships UNION SELECT group_id FROM group_working_memory
			UNION SELECT group_id FROM thought_records
		), message_stats AS (
			SELECT group_id, COUNT(*) AS messages, MAX(occurred_at) AS last_activity FROM messages GROUP BY group_id
		), member_stats AS (
			SELECT group_id, COUNT(*) AS members FROM member_profiles GROUP BY group_id
		)
		SELECT ids.group_id, COALESCE(ms.messages, 0), COALESCE(ps.members, 0),
		       COALESCE(gwm.state_json->>'active_topic', ''), ms.last_activity
		FROM ids
		LEFT JOIN message_stats ms USING (group_id)
		LEFT JOIN member_stats ps USING (group_id)
		LEFT JOIN group_working_memory gwm USING (group_id)
	`)
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	defer rows.Close()
	byID := make(map[int64]adminGroup)
	for rows.Next() {
		var group adminGroup
		var last sql.NullTime
		if err := rows.Scan(&group.GroupID, &group.Messages, &group.Members, &group.ActiveTopic, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			group.LastActivity = last.Time
		}
		byID[group.GroupID] = group
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, groupID := range d.cfg.QQ.GroupWhitelist {
		if _, ok := byID[groupID]; !ok {
			byID[groupID] = adminGroup{GroupID: groupID}
		}
	}
	groups := make([]adminGroup, 0, len(byID))
	for _, group := range byID {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].LastActivity.Equal(groups[j].LastActivity) {
			return groups[i].GroupID < groups[j].GroupID
		}
		return groups[i].LastActivity.After(groups[j].LastActivity)
	})
	return groups, nil
}

func (d *adminDashboard) loadStats(ctx context.Context) (adminStats, error) {
	var stats adminStats
	err := d.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(DISTINCT group_id) FROM messages),
			(SELECT COUNT(*) FROM member_profiles),
			(SELECT COUNT(*) FROM memories WHERE expires_at IS NULL OR expires_at > NOW()),
			(SELECT COUNT(*) FROM async_outbox WHERE status IN ('pending', 'running', 'retry'))
	`).Scan(&stats.Groups, &stats.Members, &stats.Memories, &stats.PendingTasks)
	return stats, err
}

func (d *adminDashboard) loadPersona(ctx context.Context, groupID int64) (adminPersona, error) {
	runtimeFacts, err := d.facts.CurrentPersonaFacts(ctx, d.definition.Config.ID, time.Now())
	if err != nil {
		return adminPersona{}, fmt.Errorf("persona facts: %w", err)
	}
	view := personadomain.ResolveView(d.definition, runtimeFacts, time.Now())
	persona := adminPersona{
		ID: d.definition.Config.ID, Name: d.definition.Config.Name,
		Description: d.definition.Config.Description, Facts: view.Facts,
		Interests: append([]string(nil), d.definition.Config.Interests...),
		Mood:      "steady", Energy: "normal",
	}
	if groupID == 0 {
		return persona, nil
	}
	state, err := d.state.GetPersonaState(ctx, persona.ID, groupID)
	if err != nil {
		return adminPersona{}, fmt.Errorf("persona state: %w", err)
	}
	runtime, err := d.state.GetRuntimeState(ctx, groupID)
	if err != nil {
		return adminPersona{}, fmt.Errorf("runtime state: %w", err)
	}
	persona.Mood, persona.Energy, persona.TalkBias, persona.Runtime = state.Mood, state.Energy, state.TalkBias, runtime
	return persona, nil
}

func (d *adminDashboard) loadMemories(ctx context.Context, groupID int64) ([]adminMemory, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT memory_id, scope, type, subject, content, confidence, importance, created_at, expires_at
		FROM memories
		WHERE (expires_at IS NULL OR expires_at > NOW())
		  AND ($1 = 0 OR scope = 'global' OR scope = 'group:' || $1::text OR scope LIKE 'group:' || $1::text || ':user:%')
		ORDER BY created_at DESC LIMIT 60
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("memories: %w", err)
	}
	defer rows.Close()
	memories := []adminMemory{}
	for rows.Next() {
		var memory adminMemory
		var expires sql.NullTime
		if err := rows.Scan(&memory.ID, &memory.Scope, &memory.Type, &memory.Subject, &memory.Content,
			&memory.Confidence, &memory.Importance, &memory.CreatedAt, &expires); err != nil {
			return nil, err
		}
		if expires.Valid {
			memory.ExpiresAt = &expires.Time
		}
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}

func (d *adminDashboard) loadRelationships(ctx context.Context, groupID int64) ([]adminRelationship, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT r.group_id, r.user_id,
		       COALESCE(NULLIF(p.group_card, ''), NULLIF(p.nickname, ''), NULLIF(p.qq_nickname, ''), r.user_id::text),
		       r.affinity, r.familiarity, r.tease_tolerance, r.grudge_score,
		       COALESCE(p.message_count, 0), r.last_interact_at
		FROM relationships r
		LEFT JOIN member_profiles p ON p.group_id = r.group_id AND p.user_id = r.user_id
		WHERE r.persona_id = $1 AND ($2 = 0 OR r.group_id = $2)
		ORDER BY r.affinity DESC, r.last_interact_at DESC LIMIT 100
	`, d.definition.Config.ID, groupID)
	if err != nil {
		return nil, fmt.Errorf("relationships: %w", err)
	}
	defer rows.Close()
	relationships := []adminRelationship{}
	for rows.Next() {
		var relationship adminRelationship
		if err := rows.Scan(&relationship.GroupID, &relationship.UserID, &relationship.Name,
			&relationship.Affinity, &relationship.Familiarity, &relationship.TeaseTolerance,
			&relationship.GrudgeScore, &relationship.MessageCount, &relationship.LastInteractAt); err != nil {
			return nil, err
		}
		relationships = append(relationships, relationship)
	}
	return relationships, rows.Err()
}

func (d *adminDashboard) loadActivity(ctx context.Context, groupID int64) ([]adminActivity, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT at, group_id, type, label, subject, detail FROM (
			SELECT occurred_at AS at, group_id, 'message' AS type, kind AS label,
			       COALESCE(NULLIF(sender_group_card, ''), NULLIF(sender_qq_nickname, ''), user_id::text) AS subject,
			       LEFT(text_content, 300) AS detail
			FROM messages WHERE $1 = 0 OR group_id = $1
			UNION ALL
			SELECT created_at, group_id, 'decision', chosen_action, outcome, LEFT(interpretation, 300)
			FROM thought_records WHERE $1 = 0 OR group_id = $1
			UNION ALL
			SELECT updated_at, 0, 'task', kind, status, LEFT(COALESCE(last_error, ''), 300)
			FROM async_outbox
		) activity
		ORDER BY at DESC LIMIT 80
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	defer rows.Close()
	activity := []adminActivity{}
	for rows.Next() {
		var item adminActivity
		if err := rows.Scan(&item.At, &item.GroupID, &item.Type, &item.Label, &item.Subject, &item.Detail); err != nil {
			return nil, err
		}
		activity = append(activity, item)
	}
	return activity, rows.Err()
}

const adminHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Bot 实时控制台</title>
<link rel="stylesheet" href="/admin/theme.css">
<style>
:root{color-scheme:dark;--bg:#0b0d12;--panel:#131720;--line:#252b38;--muted:#8d97a8;--text:#f3f5f7;--accent:#8bf0c8;--purple:#a99cff;--amber:#f4c675;--red:#ff7f8d}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 12% 0,#19232a 0,transparent 32%),var(--bg);font:14px/1.55 ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:var(--text)}button,input,select{font:inherit}.shell{max-width:1480px;margin:auto;padding:26px}.top{display:flex;align-items:center;justify-content:space-between;gap:20px;margin-bottom:22px}.brand{display:flex;align-items:center;gap:13px}.mark{width:42px;height:42px;border-radius:14px;display:grid;place-items:center;background:linear-gradient(135deg,var(--accent),var(--purple));color:#07110e;font-weight:900;font-size:20px}.brand h1{font-size:18px;margin:0}.brand p{margin:1px 0 0;color:var(--muted);font-size:12px}.tools{display:flex;align-items:center;gap:10px}.live{display:flex;align-items:center;gap:7px;color:var(--accent);font-size:12px}.dot{width:7px;height:7px;border-radius:50%;background:var(--accent);box-shadow:0 0 14px var(--accent)}select,.tokenbox input{background:#11151d;border:1px solid var(--line);color:var(--text);border-radius:10px;padding:9px 12px}.stats{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin-bottom:12px}.stat,.panel{background:color-mix(in srgb,var(--panel) 94%,transparent);border:1px solid var(--line);border-radius:16px}.stat{padding:17px}.stat small{color:var(--muted)}.stat strong{display:block;font-size:27px;margin-top:3px;letter-spacing:-1px}.grid{display:grid;grid-template-columns:1fr 1.25fr;gap:12px}.panel{padding:18px;min-width:0}.wide{grid-column:1/-1}.panelhead{display:flex;align-items:center;justify-content:space-between;margin-bottom:15px}.panelhead h2{font-size:14px;margin:0}.panelhead span{font-size:11px;color:var(--muted)}.persona{display:flex;gap:15px;align-items:flex-start}.avatar{flex:none;width:62px;height:62px;border-radius:20px;display:grid;place-items:center;background:linear-gradient(145deg,#283237,#171b27);font-size:24px;border:1px solid #3a4450}.persona h3{font-size:20px;margin:0 0 3px}.persona p{color:var(--muted);margin:0;max-width:640px}.pills{display:flex;gap:7px;flex-wrap:wrap;margin-top:12px}.pill,.tag{padding:4px 8px;border-radius:999px;background:#1b222c;color:#c9d0d9;font-size:11px;border:1px solid #2a3340}.pill b{color:var(--accent);font-weight:600}.facts{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:16px}.fact{padding:10px 11px;border-radius:11px;background:#0e1219}.fact small{display:block;color:var(--muted);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.fact div{margin-top:2px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.relations{display:grid;gap:9px;max-height:390px;overflow:auto}.relation{display:grid;grid-template-columns:minmax(110px,1fr) 1.4fr auto;align-items:center;gap:12px;padding:10px 0;border-bottom:1px solid var(--line)}.relation:last-child{border:0}.name{min-width:0}.name b,.name small{display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.name small{color:var(--muted)}.bar{height:6px;border-radius:9px;background:#262d37;overflow:hidden}.bar i{display:block;height:100%;background:linear-gradient(90deg,var(--purple),var(--accent));border-radius:9px}.score{font-variant-numeric:tabular-nums;color:var(--accent)}.tabs{display:flex;gap:7px}.tabs button{border:1px solid var(--line);color:var(--muted);background:transparent;border-radius:9px;padding:6px 10px;cursor:pointer}.tabs button.on{color:#0c1411;background:var(--accent);border-color:var(--accent)}.feed{display:grid;gap:8px;max-height:520px;overflow:auto}.entry{display:grid;grid-template-columns:82px 76px minmax(110px,.6fr) 2fr;gap:10px;align-items:start;padding:10px 0;border-bottom:1px solid var(--line)}.entry time,.entry small{color:var(--muted)}.entry .detail{color:#d8dde4;white-space:pre-wrap;overflow-wrap:anywhere}.badge{justify-self:start;padding:2px 7px;border-radius:6px;font-size:10px;background:#222a35;color:#bfc7d2}.badge.decision{background:#2b2741;color:#c9c0ff}.badge.task{background:#352d1f;color:#f2cc87}.memories{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;max-height:520px;overflow:auto}.memory{border:1px solid var(--line);border-radius:12px;padding:12px;background:#0f131a}.memory .meta{display:flex;justify-content:space-between;gap:8px;color:var(--muted);font-size:10px}.memory h4{margin:9px 0 4px;font-size:13px}.memory p{margin:0;color:#c8cfd8;display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden}.empty{padding:38px 12px;text-align:center;color:var(--muted)}.tokenbox{position:fixed;inset:0;background:#080a0dcc;backdrop-filter:blur(10px);display:none;place-items:center;z-index:5}.tokenbox.show{display:grid}.tokenbox form{width:min(390px,calc(100vw - 32px));padding:24px;background:var(--panel);border:1px solid var(--line);border-radius:18px}.tokenbox h2{margin:0 0 5px}.tokenbox p{color:var(--muted);margin:0 0 16px}.tokenbox input{width:100%;margin-bottom:10px}.tokenbox button{width:100%;border:0;border-radius:10px;padding:10px;background:var(--accent);color:#07110e;font-weight:700;cursor:pointer}.error{color:var(--red);font-size:12px;margin-top:10px}@media(max-width:900px){.stats{grid-template-columns:repeat(2,1fr)}.grid{grid-template-columns:1fr}.wide{grid-column:auto}.memories{grid-template-columns:1fr 1fr}.entry{grid-template-columns:70px 65px 1fr}.entry .detail{grid-column:2/-1}}@media(max-width:560px){.shell{padding:16px}.top{align-items:flex-start}.tools{align-items:flex-end;flex-direction:column}.memories,.facts{grid-template-columns:1fr}.relation{grid-template-columns:1fr 1fr auto}.entry{grid-template-columns:62px 1fr}.entry .detail{grid-column:1/-1}.entry .subject{display:none}}
</style>
</head>
<body>
<main class="shell">
  <header class="top"><div class="brand"><div class="mark" id="avatarTop">芙</div><div><span class="eyebrow">FUFU OBSERVATORY · LIVE</span><h1>Bot 实时控制台</h1><p id="subtitle">身份、记忆与群聊关系</p></div></div><div class="tools"><div class="live"><i class="dot"></i><span id="updated">连接中</span></div><select id="group" aria-label="选择群聊"></select></div></header>
  <section class="stats"><article class="stat"><small>群聊</small><strong id="groups">—</strong></article><article class="stat"><small>群友</small><strong id="members">—</strong></article><article class="stat"><small>有效记忆</small><strong id="memoryCount">—</strong></article><article class="stat"><small>后台任务</small><strong id="tasks">—</strong></article></section>
  <section class="grid">
    <article class="panel"><div class="panelhead"><h2>此刻身份</h2><span id="mode"></span></div><div id="persona"></div></article>
    <article class="panel"><div class="panelhead"><h2>群友好感度</h2><span>主观关系状态</span></div><div class="relations" id="relations"></div></article>
    <article class="panel wide"><div class="panelhead"><h2>实时记录</h2><div class="tabs"><button class="on" data-tab="activity">运行日志</button><button data-tab="memory">记忆</button></div></div><div id="activity" class="feed"></div><div id="memories" class="memories" hidden></div></article>
  </section>
</main>
<div class="tokenbox" id="tokenbox"><form id="tokenform"><h2>访问令牌</h2><p>后台数据受保护，请输入 server.admin_token。</p><input id="token" type="password" autocomplete="current-password" placeholder="Admin token"><button>进入后台</button><div class="error" id="autherror"></div></form></div>
<script>
const $=id=>document.getElementById(id), esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
let token=sessionStorage.getItem('bot-admin-token')||'', group=0, busy=false;
const ago=value=>{if(!value||value.startsWith('0001-'))return '暂无';const d=new Date(value),s=Math.max(0,(Date.now()-d)/1000);if(s<60)return Math.floor(s)+' 秒前';if(s<3600)return Math.floor(s/60)+' 分钟前';if(s<86400)return Math.floor(s/3600)+' 小时前';return d.toLocaleDateString('zh-CN')};
const mood=v=>({happy:'开心',steady:'平稳',withdrawn:'低落',aggro:'有点烦'}[v]||v||'平稳'), energy=v=>({high:'充沛',normal:'正常',low:'偏低',tired:'疲惫'}[v]||v||'正常');
function render(d){
  $('updated').textContent='刚刚更新';$('groups').textContent=d.stats.groups;$('members').textContent=d.stats.members;$('memoryCount').textContent=d.stats.memories;$('tasks').textContent=d.stats.pending_tasks;
  $('mode').textContent=(d.status.qq_enabled?'QQ 在线':'QQ 未连接')+' · '+esc(d.status.mode);$('subtitle').textContent=(d.persona.name||'Bot')+' / '+(d.selected_group?'群 '+d.selected_group:'暂无群聊');$('avatarTop').textContent=(d.persona.name||'B').slice(0,1);
  const old=String(group||d.selected_group||0);$('group').innerHTML=(d.groups.length?d.groups:[{group_id:0,members:0,messages:0}]).map(g=>'<option value="'+g.group_id+'">'+(g.group_id?'群 '+g.group_id:'暂无群聊')+' · '+g.members+' 人</option>').join('');$('group').value=old;$('group').disabled=!d.groups.length;group=Number($('group').value)||0;
  const p=d.persona, facts=(p.facts||[]).map(f=>'<div class="fact"><small>'+esc(f.key)+'</small><div title="'+esc(f.value)+'">'+esc(f.value)+'</div></div>').join('');
  $('persona').innerHTML='<div class="persona"><div class="avatar">'+esc((p.name||'B').slice(0,1))+'</div><div><h3>'+esc(p.name)+'</h3><p>'+esc(p.description)+'</p><div class="pills"><span class="pill">情绪 <b>'+esc(mood(p.mood))+'</b></span><span class="pill">精力 <b>'+esc(energy(p.energy))+'</b></span><span class="pill">发言倾向 <b>'+Number(p.talk_bias||0).toFixed(2)+'</b></span><span class="pill">状态 <b>'+esc(p.runtime.state||'observing')+'</b></span></div></div></div><div class="facts">'+facts+'</div>';
  $('relations').innerHTML=(d.relationships||[]).map(r=>'<div class="relation"><div class="name"><b>'+esc(r.name)+'</b><small>'+esc(r.user_id)+' · '+r.message_count+' 条消息</small></div><div><div class="bar"><i style="width:'+Math.round(r.affinity*100)+'%"></i></div><small>熟悉度 '+Number(r.familiarity).toFixed(2)+'</small></div><div class="score">'+Math.round(r.affinity*100)+'</div></div>').join('')||'<div class="empty">还没有形成群友关系</div>';
  $('activity').innerHTML=(d.activity||[]).map(a=>'<div class="entry"><time>'+ago(a.at)+'</time><span class="badge '+esc(a.type)+'">'+esc(a.type==='message'?'消息':a.type==='decision'?'决策':'任务')+'</span><div class="subject"><b>'+esc(a.subject||a.label)+'</b><br><small>'+esc(a.label)+(a.group_id?' · 群 '+a.group_id:'')+'</small></div><div class="detail">'+esc(a.detail||'—')+'</div></div>').join('')||'<div class="empty">暂无运行记录</div>';
  $('memories').innerHTML=(d.memories||[]).map(m=>'<article class="memory"><div class="meta"><span class="tag">'+esc(m.type)+'</span><span>'+ago(m.created_at)+'</span></div><h4>'+esc(m.subject||m.scope)+'</h4><p>'+esc(m.content)+'</p></article>').join('')||'<div class="empty">暂无有效记忆</div>';
}
async function load(){if(busy)return;busy=true;try{const headers=token?{Authorization:'Bearer '+token}:{};const url='/admin/api/snapshot'+(group?'?group_id='+group:'');const r=await fetch(url,{headers});if(r.status===401){$('tokenbox').classList.add('show');return}if(!r.ok)throw new Error(await r.text());render(await r.json());$('autherror').textContent='';$('tokenbox').classList.remove('show')}catch(e){$('updated').textContent='连接异常';$('autherror').textContent=String(e.message||e)}finally{busy=false}}
$('group').addEventListener('change',()=>{group=Number($('group').value)||0;load()});$('tokenform').addEventListener('submit',e=>{e.preventDefault();token=$('token').value.trim();sessionStorage.setItem('bot-admin-token',token);load()});document.querySelectorAll('[data-tab]').forEach(b=>b.addEventListener('click',()=>{document.querySelectorAll('[data-tab]').forEach(x=>x.classList.toggle('on',x===b));const memory=b.dataset.tab==='memory';$('activity').hidden=memory;$('memories').hidden=!memory}));load();setInterval(load,3000);
</script>
</body></html>`
