package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/config"
)

type storeBundle struct {
	db       *sql.DB
	memory   ports.MemoryStore
	meme     ports.MemeStore
	profile  ports.ProfileStore
	state    ports.RuntimeStateStore
	learning ports.LearningStateStore
	outbox   ports.OutboxStore
	closeFn  []func() error
	probeFn  []func(context.Context) error
}

func newStoreBundle(ctx context.Context, cfg config.Config) (*storeBundle, error) {
	if strings.EqualFold(cfg.App.Mode, "test") {
		store := inmemory.NewStore()
		return &storeBundle{
			memory:   store,
			meme:     store,
			profile:  store,
			state:    store,
			learning: store,
			outbox:   store,
		}, nil
	}

	db, err := postgresstore.Open(ctx, cfg.Storage.Postgres.DSN())
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	bundle := &storeBundle{
		db:      db,
		closeFn: []func() error{db.Close},
	}

	if err := applyPostgresMigrations(ctx, db); err != nil {
		_ = bundle.Close()
		return nil, err
	}

	persistentStore := postgresstore.NewStore(db)
	bundle.memory = persistentStore
	bundle.learning = persistentStore
	bundle.meme = persistentStore
	bundle.profile = persistentStore
	bundle.outbox = persistentStore
	bundle.probeFn = append(bundle.probeFn, func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		return nil
	})

	// 状态库与关系库共用同一 PG 连接池（阶段 A：替代 Redis StateStore）
	bundle.state = postgresstore.NewStateStore(db)

	return bundle, nil
}

func (b *storeBundle) Close() error {
	var joined error
	for i := len(b.closeFn) - 1; i >= 0; i-- {
		if err := b.closeFn[i](); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (b *storeBundle) HealthCheck(ctx context.Context) error {
	var joined error
	for _, probe := range b.probeFn {
		if err := probe(ctx); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func applyPostgresMigrations(ctx context.Context, db *sql.DB) error {
	schemaPath, err := locateSchemaFile()
	if err != nil {
		return err
	}
	if err := postgresstore.ApplySchema(ctx, db, schemaPath); err != nil {
		return fmt.Errorf("apply postgres schema: %w", err)
	}
	return nil
}

func locateSchemaFile() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	dir := wd
	for {
		candidate := filepath.Join(dir, "schema", "schema.sql")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", errors.New("locate schema file: schema/schema.sql not found")
}
