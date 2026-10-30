package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadMainlandWebBlockDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.Security.MainlandWebBlock.Enabled)
	require.Empty(t, cfg.Security.MainlandWebBlock.CIDRFile)
}

func TestLoadMainlandWebBlockEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("SECURITY_MAINLAND_WEB_BLOCK_ENABLED", "true")
	t.Setenv("SECURITY_MAINLAND_WEB_BLOCK_CIDR_FILE", "/app/data/mainland-cn.zone")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Security.MainlandWebBlock.Enabled)
	require.Equal(t, "/app/data/mainland-cn.zone", cfg.Security.MainlandWebBlock.CIDRFile)
}

func TestLoadMainlandWebBlockYAML(t *testing.T) {
	resetViperWithJWTSecret(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("security:\n  mainland_web_block:\n    enabled: true\n    cidr_file: /srv/mainland-cn.zone\n"), 0600))
	t.Setenv("CONFIG_FILE", path)
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Security.MainlandWebBlock.Enabled)
	require.Equal(t, "/srv/mainland-cn.zone", cfg.Security.MainlandWebBlock.CIDRFile)
}
