package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountRateLimit429RetryCountMigrationDefaultsAndConstrainsValues(t *testing.T) {
	content, err := FS.ReadFile("233_add_account_rate_limit_429_retry_count.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS rate_limit_429_retry_count INTEGER NOT NULL DEFAULT 5")
	require.Contains(t, sql, "ADD CONSTRAINT accounts_rate_limit_429_retry_count_check CHECK (rate_limit_429_retry_count >= 0 AND rate_limit_429_retry_count <= 10)")
}
