package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	"gopkg.in/yaml.v3"
)

func Default() Config {
	return Config{
		App: AppConfig{
			Name: "qq-group-bot",
			Mode: "dev",
		},
		Server: ServerConfig{
			HTTPListen: ":8088",
		},
		Runtime: RuntimeConfig{
			WorkerCount:  2,
			QueueLength:  128,
			ActorIdleTTL: "30m",
		},
		Models: ModelsConfig{
			Main:      ModelProviderConfig{Provider: "ark", Timeout: "20s"},
			Vision:    ModelProviderConfig{Provider: "ark", Timeout: "15s"},
			Embedding: ModelProviderConfig{Provider: "ark", Timeout: "15s"},
		},
		Persona: personadomain.PersonaConfig{
			ID:                "main",
			Name:              "芙宁娜",
			Aliases:           []string{"芙宁娜", "芙芙", "Furina"},
			Interests:         []string{"日常闲聊", "甜点和水果通心粉", "音乐剧与舞台", "旅行故事"},
			SpeechStyle:       "像熟人聊天，短句，少解释，有一点戏剧感但不过度表演",
			Description:       "退位后的芙宁娜·德·枫丹：曾经扮演水神五百年的普通人，如今是演员与戏剧顾问。她活泼骄傲、嘴硬爱热闹，也敏感善良。",
			ReplyMaxChars:     110,
			ReplyMaxSentences: 2,
			AllowTeasing:      true,
			AllowQuestions:    true,
			PreferMemes:       false,
		},
		DefaultPolicy: policydomain.GroupPolicy{
			Enabled:            true,
			PresenceLevel:      "balanced",
			ToolAllowlist:      nil,
			MaxConsecutiveBot:  1,
			ReplyToImageChance: 0.25,
		},
		Autonomy: policydomain.AutonomyPolicy{
			ObserveWindowSize:        20,
			FollowUpWindowSec:        120,
			MinReplyIntervalSec:      30,
			ProactiveBaseProbability: 0.05,
			ProactiveScoreThreshold:  0.65,
			MaxRepliesPer10Min:       6,
			MaxRepliesPerHour:        24,
			SuppressOnFlood:          true,
			BotDominanceSuppressSec:  60,
		},
		Memory: MemoryConfig{
			TopK:           6,
			DefaultTTL:     "720h",
			WriteThreshold: 0.65,
		},
		Meme: MemeConfig{
			AutoCollect:        true,
			CandidateThreshold: 0.6,
			PerGroupLimit:      5000,
			SearchTopK:         5,
			RepeatCooldown:     "10m",
			PreferGroupScoped:  true,
		},
		Multimodal: MultimodalConfig{
			DownloadTimeout: "10s",
			MaxVideoBytes:   50 << 20,
			MaxVideoSeconds: 60,
		},
		Tools: ToolsConfig{Codex: CodexConfig{
			Binary:         "codex",
			Timeout:        "5m",
			MaxConcurrency: 1,
		}},
		Storage: StorageConfig{
			Postgres: PostgresConfig{
				Host:      "127.0.0.1",
				Port:      5432,
				Database:  "qqbot",
				User:      "qqbot",
				SSLMode:   "disable",
				VectorDim: 2048,
			},
		},
		QQ: QQConfig{
			Enabled:     false,
			EventWSURL:  "ws://127.0.0.1:3001/event",
			OutboundURL: "http://127.0.0.1:3000",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if err := loadDotEnv(path); err != nil {
		return Config{}, err
	}
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		if len(content) > 0 {
			if err := yaml.Unmarshal(content, &cfg); err != nil {
				return Config{}, fmt.Errorf("unmarshal config: %w", err)
			}
		}
	}

	overrideWithEnvSecrets(&cfg)

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadDotEnv(configPath string) error {
	candidates := dotenvCandidates(configPath)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat dotenv file %q: %w", candidate, err)
		}
		if err := godotenv.Load(candidate); err != nil {
			return fmt.Errorf("load dotenv file %q: %w", candidate, err)
		}
	}
	return nil
}

func dotenvCandidates(configPath string) []string {
	candidates := []string{".env"}
	if configPath == "" {
		return candidates
	}

	configDir := filepath.Dir(configPath)
	candidates = append(candidates, filepath.Join(configDir, ".env"))
	parentDir := filepath.Dir(configDir)
	if parentDir != configDir {
		candidates = append(candidates, filepath.Join(parentDir, ".env"))
	}
	return candidates
}

func overrideWithEnvSecrets(cfg *Config) {
	stringOverride("QQBOT_MAIN_MODEL_API_KEY", &cfg.Models.Main.APIKey)
	stringOverride("QQBOT_VISION_MODEL_API_KEY", &cfg.Models.Vision.APIKey)
	stringOverride("QQBOT_EMBEDDING_MODEL_API_KEY", &cfg.Models.Embedding.APIKey)
	stringOverride("QQBOT_STORAGE_POSTGRES_PASSWORD", &cfg.Storage.Postgres.Password)
	stringOverride("QQBOT_QQ_ACCESS_TOKEN", &cfg.QQ.AccessToken)
}

func Validate(cfg Config) error {
	if strings.TrimSpace(cfg.Persona.ID) == "" {
		return errors.New("persona.id is required")
	}
	if strings.TrimSpace(cfg.Persona.Name) == "" {
		return errors.New("persona.name is required")
	}
	if cfg.Autonomy.ObserveWindowSize <= 0 {
		return errors.New("autonomy.observe_window_size must be positive")
	}
	if cfg.DefaultPolicy.MaxConsecutiveBot <= 0 {
		return errors.New("default_policy.max_consecutive_bot must be positive")
	}
	if strings.TrimSpace(cfg.Server.HTTPListen) == "" {
		return errors.New("server.http_listen is required")
	}
	if cfg.QQ.Enabled && strings.TrimSpace(cfg.QQ.OutboundURL) == "" {
		return errors.New("qq.outbound_url is required when qq.enabled=true")
	}
	if cfg.QQ.Enabled && strings.TrimSpace(cfg.QQ.EventWSURL) == "" {
		return errors.New("qq.event_ws_url is required when qq.enabled=true")
	}
	if cfg.QQ.Enabled && cfg.QQ.SelfID <= 0 {
		return errors.New("qq.self_id must be a positive QQ number when qq.enabled=true")
	}
	for _, server := range cfg.Tools.MCPServers {
		if !server.Enabled {
			continue
		}
		if strings.TrimSpace(server.Name) == "" {
			return errors.New("tools.mcp_servers[].name is required when enabled")
		}
		switch server.Transport {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				return fmt.Errorf("tools.mcp_servers[%s].command is required for stdio", server.Name)
			}
		case "http":
			if strings.TrimSpace(server.URL) == "" {
				return fmt.Errorf("tools.mcp_servers[%s].url is required for http", server.Name)
			}
		default:
			return fmt.Errorf("tools.mcp_servers[%s].transport must be stdio or http", server.Name)
		}
	}
	return nil
}

func stringOverride(key string, target *string) {
	if value, ok := os.LookupEnv(key); ok {
		*target = value
	}
}
