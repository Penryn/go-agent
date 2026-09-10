package app

import "embed"

// adminAssets is produced by make web and is not committed.
//go:embed adminui/dist
var adminAssets embed.FS

// GetAdminAssets returns the embedded admin assets for testing
func GetAdminAssets() embed.FS {
	return adminAssets
}
