package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/phlin/go-agent/internal/cache"
	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/replay"
)

type step struct {
	Events      []json.RawMessage `json:"events"`
	State       string            `json:"state"`
	ActiveNotes *int              `json:"active_notes,omitempty"`
	Group       int64             `json:"group,omitempty"`
	Learn       bool              `json:"learn,omitempty"`
}
type fixture struct {
	Name      string            `json:"name"`
	Responses []json.RawMessage `json:"responses"`
	Steps     []step            `json:"steps"`
}
type script struct {
	responses []json.RawMessage
	calls     int
}

func (s *script) Generate(context.Context, string, any, []string) (model.Result, error) {
	if s.calls >= len(s.responses) {
		return model.Result{}, errors.New("scripted response exhausted")
	}
	raw := s.responses[s.calls]
	s.calls++
	return model.Result{Content: string(raw)}, nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("fixture", "tests/replay/conversation.json", "fixture JSON")
	configPath := flag.String("config", "", "optional model configuration; empty uses scripted outputs without network")
	timeout := flag.Duration("timeout", 90*time.Second, "entire replay timeout")
	flag.Parse()
	b, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	if len(f.Steps) == 0 {
		return errors.New("fixture has no steps")
	}
	cfg := config.Default()
	var generator model.Generator
	var embedding model.Embedder
	hub := cache.NewHub(cfg.Cache.MaxItems)
	mode := "scripted"
	scripted := &script{responses: f.Responses}
	generator = scripted
	if *configPath != "" {
		cfg, err = config.Load(*configPath)
		if err != nil {
			return err
		}
		hub = cache.NewHub(cfg.Cache.MaxItems)
		client := model.New(cfg.Models, cfg.ModelConcurrency.Chat, cfg.ModelConcurrency.Vision, cfg.ModelConcurrency.Embedding, cfg.ModelConcurrency.Background)
		gateway := model.NewGateway(client, client, hub, cfg.Cache.EmbeddingTTL, cfg.ModelConcurrency.Chat, cfg.ModelConcurrency.Vision, cfg.ModelConcurrency.Embedding, cfg.ModelConcurrency.Background)
		gateway.ModelVersion = func(images []string) string {
			if len(images) > 0 && cfg.Models.Vision.Model != "" {
				return cfg.Models.Vision.Model
			}
			return cfg.Models.Main.Model
		}
		generator = gateway
		embedding = gateway
		mode = "live-model"
	}
	// Replays use production coordination and validation with isolated storage and simulated sends.
	cfg.Chat.Merge = 5 * time.Millisecond
	cfg.Chat.MaxWait = 20 * time.Millisecond
	cfg.Chat.PassiveInterval = 0
	cfg.Chat.SpontaneousInterval = 0
	cfg.Chat.Cache = chat.CacheTTL{Recall: cfg.Cache.RecallTTL, Context: cfg.Cache.ContextTTL, Sticker: cfg.Cache.StickerTTL, Image: cfg.Cache.ImageTTL}
	store := replay.NewStore()
	sender := &replay.Sender{}
	memories := &memory.Service{Store: store, Model: generator, Embedding: embedding, TopK: cfg.Memory.TopK, IndexLimit: cfg.Memory.IndexLimit, Cache: hub, IndexTTL: cfg.Cache.IndexTTL}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	runtime := chat.New(ctx, &chat.Runtime{Store: store, Model: generator, Embedding: embedding, Cache: hub, Memory: memories, Sender: sender, Persona: cfg.Persona, Config: cfg.Chat, SelfID: 0})
	defer runtime.Close()
	outcomes := []chat.Attempt{}
	for i, s := range f.Steps {
		if len(s.Events) == 0 {
			return fmt.Errorf("step %d has no events", i+1)
		}
		for _, raw := range s.Events[:len(s.Events)-1] {
			if err := runtime.Submit(ctx, raw); err != nil {
				return err
			}
		}
		a, err := runtime.Process(ctx, s.Events[len(s.Events)-1])
		if err != nil {
			return err
		}
		outcomes = append(outcomes, a)
		if s.Learn {
			if err := memories.Tick(ctx); err != nil {
				return err
			}
		}
		if mode == "scripted" && s.State != "" && a.State != s.State {
			return fmt.Errorf("step %d: wanted %s, got %s (%s)", i+1, s.State, a.State, a.Error)
		}
		if mode == "scripted" && s.ActiveNotes != nil {
			notes, err := store.Notes(ctx, s.Group, 1000)
			if err != nil {
				return err
			}
			active := 0
			for _, n := range notes {
				if n.State == "active" {
					active++
				}
			}
			if active != *s.ActiveNotes {
				return fmt.Errorf("step %d: wanted %d active notes, got %d", i+1, *s.ActiveNotes, active)
			}
		}
	}
	report := map[string]any{"fixture": f.Name, "mode": mode, "steps": len(outcomes), "simulated_sends": len(sender.Messages), "outcomes": outcomes}
	if mode == "scripted" {
		report["scripted_calls"] = scripted.calls
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
