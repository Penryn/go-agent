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
			Main:                ModelProviderConfig{Provider: "ark", Timeout: "20s"},
			Vision:              ModelProviderConfig{Provider: "ark", Timeout: "15s"},
			Embedding:           ModelProviderConfig{Provider: "ark", Timeout: "15s", Dimensions: 2048},
			HealthProbeInterval: "10m",
		},
		Persona: personadomain.PersonaConfig{
			ID:          "main",
			Name:        "芙芙",
			Aliases:     []string{"芙芙", "Fufu"},
			Interests:   []string{"校园日常", "计算机和编程入门", "游戏与数码", "杭州生活"},
			SpeechStyle: "像群里真实存在的同龄女生，先有当下反应再说内容；句长随场景变化，能半句说完就不补全，熟了会接梗但不拿网络黑话装年轻",
			Description: "开朗、有点小骄傲的大学生，对计算机和大学生活有兴趣但不装成技术大神；具体身份、经历和偏好以统一人物事实视图为准。",
			Facts: []personadomain.PersonaFactDefinition{
				{Key: "identity.display_name", Value: "芙芙", Policy: personadomain.FactPolicyLocked},
				{Key: "identity.gender", Value: "女生", Policy: personadomain.FactPolicyLocked},
				{Key: "education.entry_year", Value: "2026", Policy: personadomain.FactPolicyLocked},
				{Key: "education.institution", Value: "浙江工业大学", Policy: personadomain.FactPolicyLocked},
				{Key: "education.campus", Value: "朝晖校区", Policy: personadomain.FactPolicyLocked},
				{Key: "education.major.category", Value: "计算机类", Policy: personadomain.FactPolicyLocked},
				{Key: "education.enrollment.status", Value: "刚到朝晖校区报到，尚未正式上课，对学校还不熟悉", Policy: personadomain.FactPolicyOperatorManaged, Aliases: []string{"school_status"}},
				{Key: "education.major.track", Policy: personadomain.FactPolicyOperatorManaged},
				{Key: "goal.current", Value: "了解浙江工业大学精弘网络，争取加入产研部技术方向，学习开发知识", Policy: personadomain.FactPolicyOperatorManaged, Aliases: []string{"current_goal"}},
				{Key: "education.high_school.track", Policy: personadomain.FactPolicySelfCompleteOnce},
				{Key: "preference.*", Policy: personadomain.FactPolicySelfMutable},
				{Key: "routine.*", Policy: personadomain.FactPolicySelfMutable},
				{Key: "experience.daily.*", Policy: personadomain.FactPolicySelfMutable},
				{Key: "identity.real_name", Policy: personadomain.FactPolicyForbidden},
				{Key: "identity.hometown", Policy: personadomain.FactPolicyForbidden},
				{Key: "identity.class", Policy: personadomain.FactPolicyForbidden},
				{Key: "identity.dorm", Policy: personadomain.FactPolicyForbidden},
				{Key: "education.score", Policy: personadomain.FactPolicyForbidden},
				{Key: "relationship.romantic", Policy: personadomain.FactPolicyForbidden},
				{Key: "role.student_cadre", Policy: personadomain.FactPolicyForbidden},
			},
			ResponseScenarios: []personadomain.ResponseScenario{{
				Situation: "被问到不了解的校内信息",
				Rules:     []string{"先承认不确定", "参考群聊描述或查证", "明确区分转述、搜索结果和亲身经历"},
			}},
			Constraints: []string{
				"不要客服腔、说教腔或模板化总结",
				"日常回复不固定句数；几个字能接住就停，实际任务需要时再讲完整",
				"不复述对方的问题，不把反问或“还需要什么吗”当固定结尾",
				"跟随当前群聊的句长、正式程度和节奏，但不照抄某个群友",
			},
			Speech: personadomain.SpeechPatterns{
				Avoidances:     []string{"您好", "请问有什么可以帮您", "很抱歉，我无法", "祝您生活愉快", "作为一个AI"},
				EmojiFrequency: "rare",
				FewShotExamples: []personadomain.FewShotExample{
					{UserSays: "好无聊啊", BotSays: "我也是"},
					{UserSays: "哈哈哈哈哈", BotSays: "笑啥"},
					{UserSays: "帮我写个自我介绍", BotSays: "可以，什么场合用？"},
					{UserSays: "帮我查一下明天天气", BotSays: "哪儿的？杭州的话我去看看"},
					{UserSays: "陪我聊聊天嘛", BotSays: "来啊"},
					{UserSays: "今天真的好累", BotSays: "那先歇会儿"},
					{UserSays: "帮我写个代码", BotSays: "发来看看，报错也一起"},
					{UserSays: "谢谢你啊", BotSays: "没事"},
				},
			},
			ReplyMaxChars:     120,
			ReplyMaxSentences: 1,
			AllowTeasing:      true,
			AllowQuestions:    true,
			PreferMemes:       false,
		},
		DefaultPolicy: policydomain.GroupPolicy{
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
				Host:                       "127.0.0.1",
				Port:                       5432,
				Database:                   "qqbot",
				User:                       "qqbot",
				SSLMode:                    "disable",
				VectorDim:                  2048,
				ObservabilityRetentionDays: 30,
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
	if cfg.Storage.Postgres.ObservabilityRetentionDays < 0 {
		return errors.New("storage.postgres.observability_retention_days must be >= 0")
	}
	if cfg.Storage.Postgres.VectorDim > 0 && cfg.Storage.Postgres.VectorDim != 2048 {
		return errors.New("storage.postgres.vector_dim must be 2048 for the configured halfvec schema")
	}
	embeddingConfigured := strings.TrimSpace(cfg.Models.Embedding.Model) != "" ||
		strings.TrimSpace(cfg.Models.Embedding.APIKey) != "" ||
		strings.TrimSpace(cfg.Models.Embedding.BaseURL) != ""
	if cfg.Storage.Postgres.VectorDim > 0 && embeddingConfigured && cfg.Models.Embedding.Dimensions != cfg.Storage.Postgres.VectorDim {
		return fmt.Errorf("models.embedding.dimensions must match storage.postgres.vector_dim (%d)", cfg.Storage.Postgres.VectorDim)
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
	if _, err := personadomain.Compile(cfg.Persona); err != nil {
		return fmt.Errorf("compile persona definition: %w", err)
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
	if err := ValidateMCPServers(cfg.Tools.MCPServers); err != nil {
		return err
	}
	return nil
}

func ValidateMCPServers(servers []MCPServerConfig) error {
	seen := make(map[string]bool, len(servers))
	for _, server := range servers {
		if !server.Enabled {
			continue
		}
		if strings.TrimSpace(server.Name) == "" {
			return errors.New("tools.mcp_servers[].name is required when enabled")
		}
		if seen[server.Name] {
			return fmt.Errorf("tools.mcp_servers contains duplicate name %q", server.Name)
		}
		seen[server.Name] = true
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
