//go:build integration

package repository

import (
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestPublicDisplayEvidenceIntegration(t *testing.T) {
	if os.Getenv("MIGRATION_TEST_DATABASE_URL") == "" {
		t.Setenv("MIGRATION_TEST_DATABASE_URL", integrationDatabaseDSN)
	}
	f := newPassRateFixture(t, 10)
	f.exec(`INSERT INTO accounts(id,name) VALUES(64,'Unauthorized account');
INSERT INTO groups(id,name,is_exclusive) VALUES(9,'Private group',true);
INSERT INTO account_groups VALUES(64,9)`)
	at := time.Now().Add(-time.Minute)
	var latest *service.ScheduledTestResult
	for _, verdict := range []string{"fail", "fail", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"} {
		latest = f.result(verdict)
	}
	create := func(accountID *int64, groupID int64, model string) {
		t.Helper()
		target := "all_accounts"
		if accountID == nil {
			target = "group"
		}
		_, err := f.repo.Create(f.ctx, &service.ScheduledTestResult{PlanID: f.plan.ID, AccountID: accountID, GroupID: &groupID, TestDefinitionID: &f.definitionID, TargetMode: target, ModelID: model, RunID: latest.RunID, Status: "success", OutputKind: "number", ResponseText: "21", StartedAt: at, FinishedAt: at})
		require.NoError(t, err)
	}
	account62, account63, account64 := int64(62), int64(63), int64(64)
	create(&account62, 8, "model-b")
	create(&account63, 8, "model-a")
	create(&account64, 9, "model-a")
	create(nil, 8, "model-a")
	// Releasing the visible query's connection before enrichment also works
	// with a one-connection pool and avoids nested-reader deadlocks.
	f.db.SetMaxOpenConns(1)
	visible, err := f.repo.ListVisible(f.ctx, 100, 3)
	require.NoError(t, err)
	require.Len(t, visible, 4)
	for _, row := range visible {
		if row.AccountID == nil {
			require.Nil(t, row.PassRate)
			continue
		}
		require.NotEqual(t, account64, *row.AccountID, "unauthorized group results stay hidden")
		switch {
		case *row.AccountID == 62 && row.ModelID == "model-a":
			require.NotNil(t, row.PassRate)
			require.Equal(t, 10, row.PassRate.Samples)
			require.Equal(t, 8, row.PassRate.Passed)
			require.Equal(t, 80., *row.PassRate.PassRate)
			require.False(t, row.PassRate.Blocked, "the configured strict pause threshold is unchanged by reads")
		case *row.AccountID == 62 && row.ModelID == "model-b":
			require.Nil(t, row.PassRate, "a different model cannot inherit capability observations")
		default:
			require.Nil(t, row.PassRate, "a different account cannot inherit capability observations")
		}
	}
	require.Equal(t, 10, f.evidenceCount(), "reading display evidence cannot create governance samples")
}
