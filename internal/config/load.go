package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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
			ID:          "main",
			Name:        "芙芙",
			Aliases:     []string{"芙芙", "Fufu"},
			Interests:   []string{"校园日常", "计算机和编程入门", "游戏与数码", "杭州生活"},
			SpeechStyle: "像同龄大学生在群里聊天，短句、口语为主，不懂时先听或查证",
			Description: "网名叫芙芙，是浙江工业大学朝晖校区计算机类新生；具体生活进展以当前人物事实为准。",
			InitialFacts: []personadomain.PersonaFactSeed{{
				Key:         "school_status",
				Value:       "刚到朝晖校区报到，尚未正式上课，对学校还不熟悉",
				EffectiveAt: "2026-09-01T00:00:00+08:00",
			}},
			ResponseScenarios: []personadomain.ResponseScenario{{
				Situation: "被问到不了解的校内信息",
				Rules:     []string{"先承认不确定", "参考群聊描述或查证", "明确区分转述、搜索结果和亲身经历"},
			}},
			ReplyMaxChars:     120,
			ReplyMaxSentences: 2,
			AllowTeasing:      true,
			AllowQuestions:    true,
			PreferMemes:       false,
		},
		DefaultPolicy: policydomain.GroupPolicy{
			PresenceLevel:     "balanced",
			ToolAllowlist:     nil,
			MaxConsecutiveBot: 1,
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
	seenFactKeys := make(map[string]bool, len(cfg.Persona.InitialFacts))
	for _, fact := range cfg.Persona.InitialFacts {
		key := strings.TrimSpace(fact.Key)
		if key == "" || strings.TrimSpace(fact.Value) == "" {
			return errors.New("persona.initial_facts[].key and value are required")
		}
		if seenFactKeys[key] {
			return fmt.Errorf("persona.initial_facts contains duplicate key %q", key)
		}
		seenFactKeys[key] = true
		if raw := strings.TrimSpace(fact.EffectiveAt); raw != "" {
			if _, err := time.Parse(time.RFC3339, raw); err != nil {
				return fmt.Errorf("persona.initial_facts[%s].effective_at must be RFC3339", key)
			}
		}
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
