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
		TopK     int           `yaml:"top_k"`
		Interval time.Duration `yaml:"interval"`
	} `yaml:"memory"`
	ModelConcurrency int `yaml:"model_concurrency"`
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

func Default() Config {
	var c Config
	c.App.Mode = "dev"
	c.App.LogLevel = "info"
	c.Server.HTTPListen = "127.0.0.1:8088"
	c.Storage.DatabaseURL = "postgres://qqbot:qqbotpass@127.0.0.1:5432/qqbot?sslmode=disable"
	c.ModelConcurrency = 2
	c.Persona = chat.Persona{Name: "芙芙", Aliases: []string{"芙芙", "Fufu"}, Description: "开朗、有一点小骄傲的虚拟群友，喜欢游戏、数码和日常闲聊。", Style: "自然口语，先接住当下的话，不客服腔，不固定总结或反问。", Interests: []string{"游戏", "数码", "日常生活"}, Examples: []string{"群友说今天好累，可以回：那先歇会儿", "群友彼此约时间，你没有参与时可以沉默"}}
	c.Chat = chat.Config{Merge: time.Second, MaxWait: 3 * time.Second, PassiveInterval: 15 * time.Second, ActiveWindow: 90 * time.Second, Timeout: 15 * time.Second, SpontaneousInterval: 20 * time.Second, MaxPerMinute: 8, History: 80, ContextRunes: 16000}
	c.Models.Main = model.Provider{Provider: "ark", Timeout: 20 * time.Second}
	c.Models.Vision = model.Provider{Provider: "ark", Timeout: 15 * time.Second}
	c.Models.Embedding = model.Provider{Provider: "ark", Timeout: 10 * time.Second, Dimensions: 2048}
	c.Memory.TopK = 6
	c.Memory.Interval = 20 * time.Second
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
	if c.ModelConcurrency < 1 || c.ModelConcurrency > 16 {
		return errors.New("model_concurrency must be 1..16")
	}
	if c.Chat.Merge < 0 || c.Chat.MaxWait <= 0 || c.Chat.MaxWait < c.Chat.Merge || c.Chat.Timeout <= 0 || c.Chat.ActiveWindow < 0 || c.Chat.PassiveInterval < 0 || c.Chat.SpontaneousInterval < 0 || c.Chat.MaxPerMinute < 1 || c.Chat.MaxPerMinute > 60 || c.Chat.History < 1 || c.Chat.History > 500 || c.Chat.ContextRunes < 1000 || c.Chat.ContextRunes > 100000 {
		return errors.New("invalid chat limits")
	}
	if c.Memory.TopK < 1 || c.Memory.TopK > 30 || c.Memory.Interval < time.Second {
		return errors.New("invalid memory limits")
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
