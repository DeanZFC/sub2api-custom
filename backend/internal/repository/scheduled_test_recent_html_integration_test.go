//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type recentHTMLFixture struct {
	*passRateFixture
	htmlID    int64
	accountID int64
	groupID   int64
	at        time.Time
}

func newRecentHTMLFixture(t *testing.T) *recentHTMLFixture {
	t.Helper()
	if os.Getenv("MIGRATION_TEST_DATABASE_URL") == "" {
		t.Setenv("MIGRATION_TEST_DATABASE_URL", integrationDatabaseDSN)
	}
	f := &recentHTMLFixture{
		passRateFixture: newPassRateFixture(t, 10),
		accountID:       62,
		groupID:         8,
		at:              time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond),
	}
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT id FROM scheduled_test_definitions WHERE key='pelican'`).Scan(&f.htmlID))
	f.plan.TestDefinitionID = &f.htmlID
	f.plan.TestDefinitionIDs = []int64{f.htmlID}
	f.plan.Protection = service.ScheduledTestProtectionConfig{}
	var err error
	f.plan, err = f.plans.Update(f.ctx, f.plan)
	require.NoError(t, err)
	return f
}

func (f *recentHTMLFixture) input(at time.Time, status, html string) *service.ScheduledTestResult {
	return &service.ScheduledTestResult{
		PlanID: f.plan.ID, AccountID: &f.accountID, GroupID: &f.groupID, TestDefinitionID: &f.htmlID,
		TargetMode: "all_accounts", ModelID: f.plan.ModelID, ReasoningEffort: f.plan.ReasoningEffort,
		RunID: "historical-run", Status: status, OutputKind: "html", OutputHTML: html,
		StartedAt: at, FinishedAt: at,
	}
}

func (f *recentHTMLFixture) create(input *service.ScheduledTestResult) *service.ScheduledTestResult {
	f.t.Helper()
	row, err := f.repo.Create(f.ctx, input)
	require.NoError(f.t, err)
	return row
}

func (f *recentHTMLFixture) begin(runID string, input *service.ScheduledTestResult) *service.ScheduledTestResult {
	f.t.Helper()
	rows, err := f.repo.BeginRun(f.ctx, f.plan, runID, []*service.ScheduledTestResult{input})
	require.NoError(f.t, err)
	require.Len(f.t, rows, 1)
	return rows[0]
}

func recentHTMLResultIDs(rows []*service.ScheduledTestResult) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestScheduledTestRecentHTMLIntegration(t *testing.T) {
	t.Run("opt in keeps current result and caps earlier HTML from the exact series", func(t *testing.T) {
		f := newRecentHTMLFixture(t)
		f.exec(`INSERT INTO groups(id,name) VALUES(9,'Other public group');
INSERT INTO account_groups(account_id,group_id) VALUES(62,9)`)
		var successful []*service.ScheduledTestResult
		for _, age := range []time.Duration{4, 3, 2, 1} {
			successful = append(successful, f.create(f.input(f.at.Add(-age*time.Minute), "success", "<html>older</html>")))
		}
		// A passed result and equal timestamp with a smaller ID are eligible.
		successful[len(successful)-1].Status = "passed"
		require.NoError(t, f.repo.Update(f.ctx, successful[len(successful)-1]))
		sameTime := f.create(f.input(f.at, "success", "<html>same time, earlier ID</html>"))

		// These rows are newer than most valid history, so a missing predicate
		// would displace one of the expected thumbnails even with a small cap.
		for _, mutate := range []func(*service.ScheduledTestResult){
			func(row *service.ScheduledTestResult) { row.Status = "failed" },
			func(row *service.ScheduledTestResult) { row.Status = "error" },
			func(row *service.ScheduledTestResult) { row.OutputHTML = " \n\t " },
			func(row *service.ScheduledTestResult) { row.OutputKind = "text" },
			func(row *service.ScheduledTestResult) { row.AccountID = nil; row.TargetMode = "group" },
			func(row *service.ScheduledTestResult) { other := int64(63); row.AccountID = &other },
			func(row *service.ScheduledTestResult) { row.TestDefinitionID = &f.definitionID },
			func(row *service.ScheduledTestResult) { row.ModelID = "other-model" },
			func(row *service.ScheduledTestResult) { row.ReasoningEffort = "high" },
			func(row *service.ScheduledTestResult) { other := int64(9); row.GroupID = &other },
			func(row *service.ScheduledTestResult) { row.StartedAt = f.at.Add(time.Minute) },
		} {
			row := f.input(f.at.Add(-time.Second), "success", "<html>unrelated or ineligible</html>")
			mutate(row)
			f.create(row)
		}
		current := f.begin("current-html-run", f.input(f.at, "success", "<html>latest</html>"))
		f.create(f.input(f.at, "success", "<html>same time, later ID</html>"))

		for _, limit := range []int{1, 3} {
			rows, err := f.repo.ListVisible(f.ctx, 1, limit)
			require.NoError(t, err)
			require.Equal(t, []int64{current.ID}, recentHTMLResultIDs(rows), "the existing list parameter must not opt in to history")
		}
		monitor, err := f.repo.ListVisibleByGroup(f.ctx, 1)
		require.NoError(t, err)
		require.Equal(t, []int64{current.ID, current.ID}, recentHTMLResultIDs(monitor), "monitor semantics stay on the current execution for each current group")
		monitorGroups := []int64{}
		for _, row := range monitor {
			require.NotNil(t, row.GroupID)
			monitorGroups = append(monitorGroups, *row.GroupID)
		}
		require.ElementsMatch(t, []int64{8, 9}, monitorGroups)
		latestOnly, err := f.repo.ListVisibleWithRecentHTML(f.ctx, 1, 1)
		require.NoError(t, err)
		require.Equal(t, []int64{current.ID}, recentHTMLResultIDs(latestOnly))
		recent, err := f.repo.ListVisibleWithRecentHTML(f.ctx, 1, 3)
		require.NoError(t, err)
		require.ElementsMatch(t, []int64{current.ID, sameTime.ID, successful[3].ID}, recentHTMLResultIDs(recent))
		more, err := f.repo.ListVisibleWithRecentHTML(f.ctx, 1, 5)
		require.NoError(t, err)
		require.ElementsMatch(t, []int64{current.ID, sameTime.ID, successful[3].ID, successful[2].ID, successful[1].ID}, recentHTMLResultIDs(more))
		four, err := f.repo.ListVisibleWithRecentResults(f.ctx, 1, 4)
		require.NoError(t, err)
		require.ElementsMatch(t, []int64{current.ID, sameTime.ID, successful[3].ID, successful[2].ID}, recentHTMLResultIDs(four), "artwork shows the current result and exactly three earlier results")

		current.Status, current.OutputHTML = "failed", ""
		require.NoError(t, f.repo.Update(f.ctx, current))
		recent, err = f.repo.ListVisibleWithRecentHTML(f.ctx, 1, 3)
		require.NoError(t, err)
		require.ElementsMatch(t, []int64{current.ID, sameTime.ID, successful[3].ID}, recentHTMLResultIDs(recent), "an old success must not replace a failed latest result")
		for _, row := range recent {
			if row.ID == current.ID {
				require.Equal(t, "failed", row.Status)
				require.Empty(t, row.OutputHTML)
			}
		}
	})

	t.Run("running current checks never fall back to a previous success", func(t *testing.T) {
		f := newRecentHTMLFixture(t)
		f.create(f.input(f.at.Add(-time.Minute), "success", "<html>previous run</html>"))
		current := f.begin("running-html-run", f.input(f.at, "running", ""))
		earlierInRun := f.input(f.at.Add(-30*time.Second), "success", "<html>earlier in current run</html>")
		earlierInRun.RunID = current.RunID
		f.create(earlierInRun)
		for _, status := range []string{"running", "pending"} {
			current.Status = status
			require.NoError(t, f.repo.Update(f.ctx, current))
			for _, list := range []func(context.Context, int64, int) ([]*service.ScheduledTestResult, error){f.repo.ListVisibleWithRecentHTML, f.repo.ListVisibleWithRecentResults} {
				rows, err := list(f.ctx, 1, 4)
				require.NoError(t, err)
				require.Empty(t, rows, "rank the current series before filtering its completion status")
			}
		}
		current.Status = "failed"
		require.NoError(t, f.repo.Update(f.ctx, current))
		rows, err := f.repo.ListVisibleWithRecentHTML(f.ctx, 1, 3)
		require.NoError(t, err)
		require.Len(t, rows, 3, "history appears only after a completed current anchor exists")
		require.Contains(t, recentHTMLResultIDs(rows), current.ID)
	})

	t.Run("history rechecks current permissions membership and account state", func(t *testing.T) {
		f := newRecentHTMLFixture(t)
		old := f.create(f.input(f.at.Add(-time.Minute), "success", "<html>old</html>"))
		current := f.begin("permission-html-run", f.input(f.at, "success", "<html>current</html>"))
		assertVisible := func(groupID int64, visible bool) {
			t.Helper()
			for _, list := range []func(context.Context, int64, int) ([]*service.ScheduledTestResult, error){f.repo.ListVisibleWithRecentHTML, f.repo.ListVisibleWithRecentResults} {
				rows, err := list(f.ctx, 1, 4)
				require.NoError(t, err)
				if !visible {
					require.Empty(t, rows)
					continue
				}
				require.ElementsMatch(t, []int64{current.ID, old.ID}, recentHTMLResultIDs(rows))
				for _, row := range rows {
					require.NotNil(t, row.GroupID)
					require.Equal(t, groupID, *row.GroupID, "current and historical rows use the current membership projection")
				}
			}
		}
		assertVisible(8, true)
		f.exec(`UPDATE groups SET is_exclusive=true WHERE id=8`)
		assertVisible(8, false)
		f.exec(`INSERT INTO user_allowed_groups(user_id,group_id) VALUES(1,8)`)
		assertVisible(8, true)
		f.exec(`INSERT INTO groups(id,name,is_exclusive) VALUES(9,'New private group',true);
DELETE FROM account_groups WHERE account_id=62;
INSERT INTO account_groups(account_id,group_id) VALUES(62,9)`)
		assertVisible(9, false)
		f.exec(`INSERT INTO user_allowed_groups(user_id,group_id) VALUES(1,9)`)
		assertVisible(9, true)
		for _, state := range []struct {
			status      string
			schedulable bool
			visible     bool
		}{
			{"inactive", true, false},
			{"active", false, false},
			{"quality_paused", true, true},
			{"quality_paused", false, false},
			{"active", true, true},
		} {
			f.exec(`UPDATE accounts SET status=$1,schedulable=$2 WHERE id=62`, state.status, state.schedulable)
			assertVisible(9, state.visible)
		}
		f.exec(`UPDATE accounts SET deleted_at=NOW() WHERE id=62`)
		assertVisible(9, false)
		f.exec(`UPDATE accounts SET deleted_at=NULL WHERE id=62;
DELETE FROM account_groups WHERE account_id=62`)
		assertVisible(9, false)
	})
}

func TestScheduledTestRecentNumericResultsIntegration(t *testing.T) {
	f := newRecentHTMLFixture(t)
	f.plan.TestDefinitionID = &f.definitionID
	f.plan.TestDefinitionIDs = []int64{f.definitionID}
	var err error
	f.plan, err = f.plans.Update(f.ctx, f.plan)
	require.NoError(t, err)
	f.exec(`INSERT INTO groups(id,name) VALUES(9,'Other public group');
INSERT INTO account_groups(account_id,group_id) VALUES(62,9)`)
	input := func(at time.Time, status string, value *float64) *service.ScheduledTestResult {
		row := f.input(at, status, "")
		row.TestDefinitionID = &f.definitionID
		row.OutputKind, row.OutputNumeric = "number", value
		return row
	}
	var successful []*service.ScheduledTestResult
	for _, age := range []time.Duration{4, 3, 2, 1} {
		value := float64(age)
		successful = append(successful, f.create(input(f.at.Add(-age*time.Minute), "success", &value)))
	}
	zero, latestValue := 0.0, 21.0
	sameTime := f.create(input(f.at, "passed", &zero))
	for _, mutate := range []func(*service.ScheduledTestResult){
		func(row *service.ScheduledTestResult) { row.Status = "failed" },
		func(row *service.ScheduledTestResult) { row.Status = "error" },
		func(row *service.ScheduledTestResult) { row.OutputNumeric = nil },
		func(row *service.ScheduledTestResult) {
			row.OutputKind = "html"
			row.OutputHTML = "<html>other kind</html>"
		},
		func(row *service.ScheduledTestResult) { row.AccountID = nil; row.TargetMode = "group" },
		func(row *service.ScheduledTestResult) { other := int64(63); row.AccountID = &other },
		func(row *service.ScheduledTestResult) { row.TestDefinitionID = &f.htmlID },
		func(row *service.ScheduledTestResult) { row.ModelID = "other-model" },
		func(row *service.ScheduledTestResult) { row.ReasoningEffort = "high" },
		func(row *service.ScheduledTestResult) { other := int64(9); row.GroupID = &other },
		func(row *service.ScheduledTestResult) { row.StartedAt = f.at.Add(time.Minute) },
	} {
		row := input(f.at.Add(-time.Second), "success", &latestValue)
		mutate(row)
		f.create(row)
	}
	current := f.begin("current-numeric-run", input(f.at, "success", &latestValue))
	f.create(input(f.at, "success", &latestValue))
	expected := []int64{current.ID, sameTime.ID, successful[3].ID, successful[2].ID}
	rows, err := f.repo.ListVisibleWithRecentResults(f.ctx, 1, 4)
	require.NoError(t, err)
	require.ElementsMatch(t, expected, recentHTMLResultIDs(rows), "numeric checks show the current result and exactly three earlier results from the same series")
	for _, row := range rows {
		if row.ID == sameTime.ID {
			require.NotNil(t, row.OutputNumeric)
			require.Zero(t, *row.OutputNumeric, "zero is a valid historical numeric result")
		}
	}
	for _, list := range []func(context.Context, int64, int) ([]*service.ScheduledTestResult, error){f.repo.ListVisible, f.repo.ListVisibleWithRecentHTML} {
		latest, err := list(f.ctx, 1, 4)
		require.NoError(t, err)
		require.Equal(t, []int64{current.ID}, recentHTMLResultIDs(latest), "existing list modes do not add numeric history")
	}
	latest, err := f.repo.ListVisibleWithRecentResults(f.ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{current.ID}, recentHTMLResultIDs(latest))

	current.Status, current.OutputNumeric = "failed", nil
	require.NoError(t, f.repo.Update(f.ctx, current))
	rows, err = f.repo.ListVisibleWithRecentResults(f.ctx, 1, 4)
	require.NoError(t, err)
	require.ElementsMatch(t, expected, recentHTMLResultIDs(rows), "historical numeric output never replaces a failed current result")
	for _, status := range []string{"running", "pending"} {
		current.Status = status
		require.NoError(t, f.repo.Update(f.ctx, current))
		rows, err := f.repo.ListVisibleWithRecentResults(f.ctx, 1, 4)
		require.NoError(t, err)
		require.Empty(t, rows, "unfinished numeric checks never fall back to a previous run")
	}
}
