package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

type Config struct {
	App           AppConfig                   `yaml:"app"`
	Server        ServerConfig                `yaml:"server"`
	Runtime       RuntimeConfig               `yaml:"runtime"`
	Models        ModelsConfig                `yaml:"models"`
	Persona       personadomain.PersonaConfig `yaml:"persona"`
	DefaultPolicy policydomain.GroupPolicy    `yaml:"default_policy"`
	GroupPolicies []policydomain.GroupPolicy  `yaml:"group_policies"`
	Autonomy      policydomain.AutonomyPolicy `yaml:"autonomy"`
	Memory        MemoryConfig                `yaml:"memory"`
	Meme          MemeConfig                  `yaml:"meme"`
	Multimodal    MultimodalConfig            `yaml:"multimodal"`
	Tools         ToolsConfig                 `yaml:"tools"`
	Storage       StorageConfig               `yaml:"storage"`
	QQ            QQConfig                    `yaml:"qq"`
}

type AppConfig struct {
	Name     string `yaml:"name"`
	Mode     string `yaml:"mode"`
	LogLevel string `yaml:"log_level"` // debug / info / warn / error，默认 info
}

type ServerConfig struct {
	HTTPListen string `yaml:"http_listen"`
}

type RuntimeConfig struct {
	WorkerCount  int    `yaml:"worker_count"`
	ActorIdleTTL string `yaml:"actor_idle_ttl"`
}

type ModelProviderConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"-"`
	Timeout  string `yaml:"timeout"`
	// APIType 仅对 embedding 模型生效：text（默认）或 multimodal。
	// doubao-embedding-vision 系列多模态模型须设为 multimodal。
	APIType string `yaml:"api_type"`
}

type ModelsConfig struct {
	Main      ModelProviderConfig `yaml:"main"`
	Vision    ModelProviderConfig `yaml:"vision"`
	Embedding ModelProviderConfig `yaml:"embedding"`
}

type MemoryConfig struct {
	TopK       int    `yaml:"top_k"`
	DefaultTTL string `yaml:"default_ttl"`
	// TypeTTL 按记忆类型指定差异化 TTL，覆盖 DefaultTTL。
	// 例：topic_keyword: 168h, reaction_pattern: 360h, group_slang: 720h, user_catchphrase: 2160h
	TypeTTL           map[string]string `yaml:"type_ttl"`
	SemanticThreshold float64           `yaml:"semantic_threshold"` // 向量相似度过滤阈值，低于此值的结果被丢弃
}

type MemeConfig struct {
	AutoCollect        bool    `yaml:"auto_collect"`
	CandidateThreshold float64 `yaml:"candidate_threshold"`
	PerGroupLimit      int     `yaml:"per_group_limit"`
	SearchTopK         int     `yaml:"search_top_k"`
	RepeatCooldown     string  `yaml:"repeat_cooldown"`
	PreferGroupScoped  bool    `yaml:"prefer_group_scoped"`
	SemanticThreshold  float64 `yaml:"semantic_threshold"` // 向量相似度过滤阈值，低于此值的结果被丢弃
}

type MultimodalConfig struct {
	DownloadTimeout string `yaml:"download_timeout"`
}

type ToolsConfig struct {
	MCPServers []MCPServerConfig `yaml:"mcp_servers"`
	Codex      CodexConfig       `yaml:"codex"`
}

type MCPServerConfig struct {
	Name      string   `yaml:"name"`
	Enabled   bool     `yaml:"enabled"`
	Required  bool     `yaml:"required"`
	Transport string   `yaml:"transport"` // stdio / http
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args"`
	URL       string   `yaml:"url"`
	Tools     []string `yaml:"tools"`
	Timeout   string   `yaml:"timeout"`
}

type CodexConfig struct {
	Enabled            bool    `yaml:"enabled"`
	Binary             string  `yaml:"binary"`
	Model              string  `yaml:"model"`
	CWD                string  `yaml:"cwd"`
	NetworkEnabled     bool    `yaml:"network_enabled"`
	Timeout            string  `yaml:"timeout"`
	MaxConcurrency     int     `yaml:"max_concurrency"`
	WriteUserWhitelist []int64 `yaml:"write_user_whitelist"`
}

type StorageConfig struct {
	Postgres PostgresConfig `yaml:"postgres"`
}

type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	Password string `yaml:"-"`
	SSLMode  string `yaml:"ssl_mode"`
	// VectorDim 是 pgvector 向量列维度，须与 embedding 模型输出严格一致。
	// 启动时校验与表结构一致，不一致拒绝启动。
	VectorDim int `yaml:"vector_dim"`
}

func (c PostgresConfig) DSN() string {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s",
		url.PathEscape(c.User), url.PathEscape(c.Password),
		net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		url.PathEscape(c.Database), sslMode)
}

type QQConfig struct {
	Enabled        bool    `yaml:"enabled"`
	SelfID         int64   `yaml:"self_id"`
	AccessToken    string  `yaml:"-"`
	OutboundURL    string  `yaml:"outbound_url"`
	EventWSURL     string  `yaml:"event_ws_url"`
	GroupWhitelist []int64 `yaml:"group_whitelist"`
}
