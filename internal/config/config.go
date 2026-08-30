package config

import (
	"net"
	"strconv"

	mysqlcfg "github.com/go-sql-driver/mysql"
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
	Tools         ToolsConfig                 `yaml:"tools"`
	Memory        MemoryConfig                `yaml:"memory"`
	Meme          MemeConfig                  `yaml:"meme"`
	Multimodal    MultimodalConfig            `yaml:"multimodal"`
	Storage       StorageConfig               `yaml:"storage"`
	QQ            QQConfig                    `yaml:"qq"`
}

type AppConfig struct {
	Name     string `yaml:"name"`
	Mode     string `yaml:"mode"`
	LogLevel string `yaml:"log_level"` // debug / info / warn / error，默认 info
}

type ServerConfig struct {
	HTTPListen   string `yaml:"http_listen"`
	ReadTimeout  string `yaml:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout"`
}

type RuntimeConfig struct {
	WorkerCount int `yaml:"worker_count"`
	QueueLength int `yaml:"queue_length"`
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

type ToolsConfig struct {
	Allowlist []string          `yaml:"allowlist"`
	Timeouts  map[string]string `yaml:"timeouts"`
	Budgets   map[string]int    `yaml:"budgets"`
}

type MemoryConfig struct {
	TopK       int    `yaml:"top_k"`
	DefaultTTL string `yaml:"default_ttl"`
	// TypeTTL 按记忆类型指定差异化 TTL，覆盖 DefaultTTL。
	// 例：topic_keyword: 168h, reaction_pattern: 360h, group_slang: 720h, user_catchphrase: 2160h
	TypeTTL           map[string]string `yaml:"type_ttl"`
	TypeEnabled       map[string]bool   `yaml:"type_enabled"`
	WriteThreshold    float64           `yaml:"write_threshold"`
	SemanticTopK      int               `yaml:"semantic_top_k"`     // 语义检索返回数量，0 时使用 TopK 值
	SemanticThreshold float64           `yaml:"semantic_threshold"` // 向量相似度过滤阈值，低于此值的结果被丢弃
}

type MemeConfig struct {
	AutoCollect        bool    `yaml:"auto_collect"`
	CandidateThreshold float64 `yaml:"candidate_threshold"`
	DedupThreshold     float64 `yaml:"dedup_threshold"`
	PerGroupLimit      int     `yaml:"per_group_limit"`
	SearchTopK         int     `yaml:"search_top_k"`
	RepeatCooldown     string  `yaml:"repeat_cooldown"`
	PreferGroupScoped  bool    `yaml:"prefer_group_scoped"`
	SemanticTopK       int     `yaml:"semantic_top_k"`     // 向量搜索返回数量，0 时禁用向量搜索
	SemanticThreshold  float64 `yaml:"semantic_threshold"` // 向量相似度过滤阈值，低于此值的结果被丢弃
}

type MultimodalConfig struct {
	DownloadTimeout string `yaml:"download_timeout"`
	MaxVideoBytes   int64  `yaml:"max_video_bytes"`
	MaxVideoSeconds int    `yaml:"max_video_seconds"`
	Keyframes       int    `yaml:"keyframes"`
	VisionBudget    int    `yaml:"vision_budget"`
}

type StorageConfig struct {
	MySQL  MySQLConfig  `yaml:"mysql"`
	Redis  RedisConfig  `yaml:"redis"`
	Qdrant QdrantConfig `yaml:"qdrant"`
	MinIO  MinIOConfig  `yaml:"minio"`
}

type MySQLConfig struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	Database  string `yaml:"database"`
	User      string `yaml:"user"`
	Password  string `yaml:"-"`
	ParseTime bool   `yaml:"parse_time"`
	Charset   string `yaml:"charset"`
}

func (c MySQLConfig) DSN() string {
	cfg := mysqlcfg.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.Database
	cfg.ParseTime = c.ParseTime
	if c.Charset != "" {
		cfg.Params = map[string]string{"charset": c.Charset}
	}
	return cfg.FormatDSN()
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"-"`
	DB       int    `yaml:"db"`
}

type QdrantConfig struct {
	URL            string `yaml:"url"`
	APIKey         string `yaml:"-"`
	Collection     string `yaml:"collection"`
	VectorDim      int    `yaml:"vector_dim"`      // 嵌入向量维度，需与 embedding 模型匹配，默认 1536
	MemeCollection string `yaml:"meme_collection"` // 表情包向量集合名，为空时禁用 meme 向量搜索
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"-"`
	SecretKey string `yaml:"-"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type QQConfig struct {
	Enabled        bool    `yaml:"enabled"`
	SelfID         int64   `yaml:"self_id"`
	AccessToken    string  `yaml:"-"`
	OutboundURL    string  `yaml:"outbound_url"`
	EventWSURL     string  `yaml:"event_ws_url"`
	InboundRoute   string  `yaml:"inbound_route"`
	GroupWhitelist []int64 `yaml:"group_whitelist"`
}
