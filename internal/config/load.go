package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
			MinReplyIntervalSec:      30,
			ProactiveBaseProbability: 0.05,
			ProactiveScoreThreshold:  0.65,
		},
		Memory: MemoryConfig{
			TopK:       6,
			DefaultTTL: "720h",
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

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func Validate(cfg Config) error {
	if cfg.Storage.Postgres.VectorDim > 0 && cfg.Storage.Postgres.VectorDim != 2048 {
		return errors.New("storage.postgres.vector_dim must be 2048 for the configured halfvec schema")
	}
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
