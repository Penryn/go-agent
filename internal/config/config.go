package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	App struct {
		Mode     string `yaml:"mode"`
		LogLevel string `yaml:"log_level"`
	} `yaml:"app"`
	Server struct {
		HTTPListen string `yaml:"http_listen"`
		AdminToken string `yaml:"admin_token"`
	} `yaml:"server"`
	Storage struct {
		DatabaseURL string `yaml:"database_url"`
	} `yaml:"storage"`
	Models  model.Config `yaml:"models"`
	Persona chat.Persona `yaml:"persona"`
	Chat    chat.Config  `yaml:"chat"`
	Meme    meme.Config  `yaml:"meme"`
	Memory  struct {
		TopK               int           `yaml:"top_k"`
		Interval           time.Duration `yaml:"interval"`
		IndexLimit         int           `yaml:"index_limit"`
		SummaryBatch       int           `yaml:"summary_batch"`
		SummaryInterval    time.Duration `yaml:"summary_interval"`
		UsageFlushInterval time.Duration `yaml:"usage_flush_interval"`
	} `yaml:"memory"`
	Cache struct {
		MaxItems     int           `yaml:"max_items"`
		EmbeddingTTL time.Duration `yaml:"embedding_ttl"`
		IndexTTL     time.Duration `yaml:"index_ttl"`
		RecallTTL    time.Duration `yaml:"recall_ttl"`
		StickerTTL   time.Duration `yaml:"sticker_ttl"`
		ImageTTL     time.Duration `yaml:"image_ttl"`
		ContextTTL   time.Duration `yaml:"context_ttl"`
	} `yaml:"cache"`
	ModelConcurrency Concurrency `yaml:"model_concurrency"`
	QQ               struct {
		Enabled        bool    `yaml:"enabled"`
		SelfID         int64   `yaml:"self_id"`
		OutboundURL    string  `yaml:"outbound_url"`
		OutboundToken  string  `yaml:"outbound_token"`
		EventWSURL     string  `yaml:"event_ws_url"`
		EventWSToken   string  `yaml:"event_ws_token"`
		GroupWhitelist []int64 `yaml:"group_whitelist"`
	} `yaml:"qq"`
}

// Concurrency sets independent in-flight limits per request kind.
type Concurrency struct {
	Chat       int `yaml:"chat"`       // text chat
	Vision     int `yaml:"vision"`     // image requests
	Embedding  int `yaml:"embedding"`  // interactive query embeddings
	Background int `yaml:"background"` // memory, indexing and summaries
}

func (c *Concurrency) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var n int
		if err := value.Decode(&n); err != nil {
			return err
		}
		c.Chat, c.Vision, c.Embedding, c.Background = n, 1, n, 1
		return nil
	}
	type plain Concurrency
	var out plain
	if err := value.Decode(&out); err != nil {
		return err
	}
	*c = Concurrency(out)
	if c.Chat == 0 {
		c.Chat = 2
	}
	if c.Vision == 0 {
		c.Vision = 1
	}
	if c.Embedding == 0 {
		c.Embedding = c.Chat
	}
	if c.Background == 0 {
		c.Background = 1
	}
	return nil
}

func (c Concurrency) ChatSlots() int {
	if c.Chat < 1 {
		return 1
	}
	return c.Chat
}

func (c Concurrency) Budgets() (chat, vision, embedding, background int) {
	return c.ChatSlots(), max(c.Vision, 1), max(c.Embedding, 1), max(c.Background, 1)
}

func Default() Config {
	var c Config
	c.App.Mode = "dev"
	c.App.LogLevel = "info"
	c.Server.HTTPListen = "127.0.0.1:8088"
	c.Storage.DatabaseURL = "postgres://qqbot:qqbotpass@127.0.0.1:5432/qqbot?sslmode=disable"
	c.ModelConcurrency = Concurrency{Chat: 2, Vision: 1, Embedding: 2, Background: 1}
	c.Persona = chat.Persona{Name: "芙芙", Aliases: []string{"芙芙", "Fufu"}, Description: "开朗、有一点小骄傲的虚拟群友，喜欢游戏、数码和日常闲聊。", Style: "自然口语，先接住当下的话，不客服腔，不固定总结或反问。", Interests: []string{"游戏", "数码", "日常生活"}, Examples: []string{"群友说今天好累，可以回：那先歇会儿", "群友彼此约时间，你没有参与时可以沉默"}}
	c.Chat = chat.Config{Merge: time.Second, MaxWait: 3 * time.Second, PassiveInterval: 15 * time.Second, ActiveWindow: 90 * time.Second, Timeout: 15 * time.Second, SpontaneousInterval: 20 * time.Second, MaxPerMinute: 8, History: 80, ContextRunes: 16000}
	c.Models.Main = model.Provider{Provider: "ark", Timeout: 20 * time.Second}
	c.Models.Vision = model.Provider{Provider: "ark", Timeout: 15 * time.Second}
	c.Models.Embedding = model.Provider{Provider: "ark", Timeout: 10 * time.Second, Dimensions: 2048}
	c.Memory.TopK = 6
	c.Memory.Interval = 20 * time.Second
	c.Memory.IndexLimit = 5000
	c.Memory.SummaryBatch = 8
	c.Memory.SummaryInterval = 30 * time.Second
	c.Memory.UsageFlushInterval = 30 * time.Second
	c.Cache = struct {
		MaxItems     int           `yaml:"max_items"`
		EmbeddingTTL time.Duration `yaml:"embedding_ttl"`
		IndexTTL     time.Duration `yaml:"index_ttl"`
		RecallTTL    time.Duration `yaml:"recall_ttl"`
		StickerTTL   time.Duration `yaml:"sticker_ttl"`
		ImageTTL     time.Duration `yaml:"image_ttl"`
		ContextTTL   time.Duration `yaml:"context_ttl"`
	}{MaxItems: 10000, EmbeddingTTL: 30 * time.Minute, IndexTTL: 10 * time.Minute, RecallTTL: 20 * time.Second, StickerTTL: 20 * time.Second, ImageTTL: 10 * time.Minute, ContextTTL: 10 * time.Second}
	c.Meme = meme.Config{AutoCollect: true, Path: "./memes", MaxItems: 5000, MaxBytes: 512 << 20, MaxImageBytes: 10 << 20, Cooldown: 10 * time.Minute, TopK: 5}
	return c
}
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("decode configuration: %w", err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	c.Meme.Path, err = filepath.Abs(c.Meme.Path)
	return c, err
}
func (c Config) Validate() error {
	u, err := url.Parse(c.Storage.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("storage.database_url must be a PostgreSQL URL")
	}
	if c.Persona.Name == "" || len(c.Persona.Description) > 10000 || len(c.Persona.Style) > 10000 {
		return errors.New("invalid persona")
	}
	if c.ModelConcurrency.Chat < 1 || c.ModelConcurrency.Chat > 16 || c.ModelConcurrency.Vision < 1 || c.ModelConcurrency.Vision > 16 || c.ModelConcurrency.Embedding < 1 || c.ModelConcurrency.Embedding > 16 || c.ModelConcurrency.Background < 1 || c.ModelConcurrency.Background > 16 {
		return errors.New("model_concurrency budgets must be 1..16")
	}
	if c.Chat.Merge < 0 || c.Chat.MaxWait <= 0 || c.Chat.MaxWait < c.Chat.Merge || c.Chat.Timeout <= 0 || c.Chat.ActiveWindow < 0 || c.Chat.PassiveInterval < 0 || c.Chat.SpontaneousInterval < 0 || c.Chat.MaxPerMinute < 1 || c.Chat.MaxPerMinute > 60 || c.Chat.History < 1 || c.Chat.History > 500 || c.Chat.ContextRunes < 1000 || c.Chat.ContextRunes > 100000 {
		return errors.New("invalid chat limits")
	}
	if c.Memory.TopK < 1 || c.Memory.TopK > 30 || c.Memory.Interval < time.Second || c.Memory.IndexLimit < c.Memory.TopK || c.Memory.IndexLimit > 100000 || c.Memory.SummaryBatch < 1 || c.Memory.SummaryBatch > 100 || c.Memory.SummaryInterval < time.Second || c.Memory.UsageFlushInterval < time.Second {
		return errors.New("invalid memory limits")
	}
	if c.Cache.MaxItems < 1 || c.Cache.MaxItems > 100000 || c.Cache.EmbeddingTTL <= 0 || c.Cache.IndexTTL <= 0 || c.Cache.RecallTTL <= 0 || c.Cache.StickerTTL <= 0 || c.Cache.ImageTTL <= 0 || c.Cache.ContextTTL <= 0 {
		return errors.New("invalid cache limits")
	}
	if c.Meme.TopK < 1 || c.Meme.TopK > 20 || c.Meme.MaxItems < 1 || c.Meme.MaxItems > 20000 || c.Meme.MaxBytes < c.Meme.MaxImageBytes || c.Meme.MaxImageBytes < 1 || c.Meme.MaxImageBytes > 16<<20 || c.Meme.Cooldown < 0 || c.Meme.Path == "" {
		return errors.New("invalid global sticker limits")
	}
	for _, p := range []model.Provider{c.Models.Main, c.Models.Vision, c.Models.Embedding} {
		if p.Provider != "" && p.Provider != "ark" && p.Provider != "openai" {
			return errors.New("model provider must be ark or openai")
		}
		if p.Timeout < 0 {
			return errors.New("invalid model timeout")
		}
		if p.BaseURL != "" {
			u, err := url.Parse(p.BaseURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return errors.New("invalid model base_url")
			}
		}
	}
	if c.Models.Embedding.Dimensions < 1 || c.Models.Embedding.Dimensions > 4000 {
		return errors.New("embedding dimensions must be 1..4000")
	}
	if c.QQ.Enabled {
		if c.QQ.SelfID <= 0 {
			return errors.New("qq.self_id is required when QQ is enabled")
		}
		for _, raw := range []string{c.QQ.OutboundURL, c.QQ.EventWSURL} {
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" {
				return errors.New("QQ endpoint is required")
			}
		}
		if !strings.HasPrefix(c.QQ.OutboundURL, "http://") && !strings.HasPrefix(c.QQ.OutboundURL, "https://") {
			return errors.New("QQ outbound endpoint must use HTTP")
		}
		if !strings.HasPrefix(c.QQ.EventWSURL, "ws://") && !strings.HasPrefix(c.QQ.EventWSURL, "wss://") {
			return errors.New("QQ event endpoint must use WebSocket")
		}
	}
	return nil
}
