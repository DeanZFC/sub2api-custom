//go:build integration

package qqbot_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestQQBotFullMigrationChain(t *testing.T) {
	dsn := os.Getenv("QQBOT_TEST_DSN")
	if dsn == "" {
		t.Skip("QQBOT_TEST_DSN is required")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "qqbot_mig_" + uuid.New().String()[:8]
	quoted := `"` + schema + `"`
	_, err = admin.Exec(`CREATE DATABASE ` + quoted)
	require.NoError(t, err)
	defer admin.Exec(`DROP DATABASE ` + quoted + ` WITH (FORCE)`)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + schema
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, repository.ApplyMigrations(context.Background(), db))
	require.NoError(t, repository.ApplyMigrations(context.Background(), db))
	var rewardTables int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('qq_bot_reward_codes','qq_bot_reward_attempts','qq_bot_reward_limits')`).Scan(&rewardTables))
	require.Equal(t, 3, rewardTables)
	var exists bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='qq_bot_rewards')`).Scan(&exists))
	require.True(t, exists)
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='qq_bot_lottery_draws')`).Scan(&exists))
	require.True(t, exists)
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='qq_bot_message_references')`).Scan(&exists))
	require.True(t, exists)
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='qq_bot_group_messages')`).Scan(&exists))
	require.True(t, exists)
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='qq_bot_message_references' AND column_name='shareable')`).Scan(&exists))
	require.True(t, exists)
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='qq_bot_price_snapshots')`).Scan(&exists))
	require.True(t, exists)
}
