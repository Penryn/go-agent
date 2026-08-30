package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	miniostore "github.com/phlin/go-agent/internal/adapters/storage/minio"
	mysqlstore "github.com/phlin/go-agent/internal/adapters/storage/mysql"
	redisstore "github.com/phlin/go-agent/internal/adapters/storage/redis"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
)

type storeBundle struct {
	memory   ports.MemoryStore
	meme     ports.MemeStore
	profile  ports.ProfileStore
	state    ports.RuntimeStateStore
	learning ports.LearningStateStore
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
		}, nil
	}

	db, err := mysqlstore.Open(ctx, cfg.Storage.MySQL.DSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	bundle := &storeBundle{
		closeFn: []func() error{db.Close},
	}

	if err := applyMySQLMigrations(ctx, db); err != nil {
		_ = bundle.Close()
		return nil, err
	}

	mysqlPersistentStore := mysqlstore.NewStore(db)
	bundle.memory = mysqlPersistentStore
	bundle.learning = mysqlPersistentStore
	bundle.meme = mysqlPersistentStore
	bundle.profile = mysqlPersistentStore
	bundle.probeFn = append(bundle.probeFn, func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("mysql: %w", err)
		}
		return nil
	})

	stateStore := redisstore.New(cfg.Storage.Redis.Addr, cfg.Storage.Redis.Password, cfg.Storage.Redis.DB)
	bundle.closeFn = append(bundle.closeFn, stateStore.Close)
	if err := stateStore.Ping(ctx); err != nil {
		_ = bundle.Close()
		return nil, fmt.Errorf("ping redis runtime store: %w", err)
	}
	bundle.state = stateStore
	bundle.probeFn = append(bundle.probeFn, func(ctx context.Context) error {
		if err := stateStore.Ping(ctx); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	})

	if err := ensureMinIO(ctx, cfg.Storage.MinIO); err != nil {
		_ = bundle.Close()
		return nil, err
	}

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

func applyMySQLMigrations(ctx context.Context, db *sql.DB) error {
	migrationsDir, err := locateMigrationsDir()
	if err != nil {
		return err
	}
	if err := mysqlstore.ApplyMigrations(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("apply mysql migrations: %w", err)
	}
	return nil
}

func ensureMinIO(ctx context.Context, cfg config.MinIOConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" ||
		strings.TrimSpace(cfg.Bucket) == "" ||
		strings.TrimSpace(cfg.AccessKey) == "" ||
		strings.TrimSpace(cfg.SecretKey) == "" {
		return nil
	}

	store, err := miniostore.New(cfg)
	if err != nil {
		return fmt.Errorf("init minio object store: %w", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("ensure minio bucket: %w", err)
	}
	return nil
}

func locateMigrationsDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	dir := wd
	for {
		candidate := filepath.Join(dir, "migrations")
		if _, err := os.Stat(filepath.Join(candidate, "001_init.sql")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", errors.New("locate migrations directory: migrations/001_init.sql not found")
}
