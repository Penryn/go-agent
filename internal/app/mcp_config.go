package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/config"
)

func loadRuntimeMCPConfig(ctx context.Context, db *sql.DB, fallback []config.MCPServerConfig) ([]config.MCPServerConfig, error) {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT servers_json FROM runtime_mcp_config WHERE config_id = 1`).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return cloneMCPServers(fallback), nil
		}
		return nil, fmt.Errorf("load runtime MCP config: %w", err)
	}
	var servers []config.MCPServerConfig
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("decode runtime MCP config: %w", err)
	}
	return servers, nil
}

func saveRuntimeMCPConfig(ctx context.Context, db *sql.DB, servers []config.MCPServerConfig) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("encode runtime MCP config: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO runtime_mcp_config (config_id, servers_json, updated_at)
		VALUES (1, $1, $2)
		ON CONFLICT (config_id) DO UPDATE SET servers_json = EXCLUDED.servers_json, updated_at = EXCLUDED.updated_at
	`, raw, time.Now())
	if err != nil {
		return fmt.Errorf("save runtime MCP config: %w", err)
	}
	return nil
}

func cloneMCPServers(servers []config.MCPServerConfig) []config.MCPServerConfig {
	result := make([]config.MCPServerConfig, len(servers))
	copy(result, servers)
	for i := range result {
		result[i].Args = append([]string(nil), result[i].Args...)
		result[i].Tools = append([]string(nil), result[i].Tools...)
	}
	return result
}
