//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Raw SQL cascades do not go through the Go repository's Delete method.
// Compare the SQL projection after each cascade with the normal Go projection.
func TestIntelligenceProjectionAcrossSQLCascades(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	id := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, tx.QueryRowContext(ctx, q, args...).Scan(&n))
		return n
	}
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	g := id(`INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, uuid.NewString())
	a := id(`INSERT INTO accounts(name,platform,type) VALUES('test','openai','oauth') RETURNING id`)
	exec(`INSERT INTO account_groups(group_id,account_id) VALUES($1,$2)`, g, a)
	makePlan := func(config string) int64 {
		return id(`INSERT INTO scheduled_test_plans(group_id,group_ids,enabled,model_ids,target_mode,protection)
 VALUES($1, ARRAY[$1]::bigint[],true,ARRAY['m','n'],'all_accounts',$2::jsonb) RETURNING id`, g, config)
	}
	standard := makePlan(`{"intelligence":{"version":"cascade"}}`)
	legacy := makePlan(`{}`)
	exec(`INSERT INTO intelligence_model_states(plan_id,account_id,model_key,blocked,passes,verdict,hold_version) VALUES($1,$2,'m',true,0,'confirmed','cascade')`, standard, a)
	exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,model_key,blocked,reason) VALUES($1,$2,'x',true,'legacy')`, legacy, a)
	require.NoError(t, reconcileProtectionAccount(ctx, tx, a))
	check := func(paused bool, held string) {
		t.Helper()
		var status string
		var all bool
		var holds string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT status,COALESCE((extra->>'active_test_all_models_paused')::boolean,false),COALESCE((extra->'active_test_model_holds')::text,'{}') FROM accounts WHERE id=$1`, a).Scan(&status, &all, &holds))
		require.Equal(t, paused, all)
		if paused {
			require.Equal(t, "quality_paused", status)
		} else {
			require.Equal(t, "active", status)
		}
		require.JSONEq(t, held, holds)
		var changed bool
		before := id(`SELECT (extract(epoch FROM updated_at)*1000000)::bigint FROM accounts WHERE id=$1`, a)
		require.NoError(t, reconcileProtectionAccount(ctx, tx, a))
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT (extract(epoch FROM updated_at)*1000000)::bigint<>$2 FROM accounts WHERE id=$1`, a, before).Scan(&changed))
		require.False(t, changed, "Go and database projections must agree")
	}
	exec(`DELETE FROM scheduled_test_plans WHERE id=$1`, legacy)
	check(false, `{"m":"智商监测确认降智"}`)
	// A new pause-on-any rule requires current-version evidence. Persisted
	// individual holds remain active while policy versions change.
	exec(`UPDATE scheduled_test_plans SET protection='{"intelligence":{"version":"new","pause_on_any":true}}' WHERE id=$1`, standard)
	exec(`SELECT reconcile_active_test_model_protection($1)`, a)
	check(false, `{"m":"智商监测确认降智"}`)
	exec(`UPDATE intelligence_model_states SET hold_version='new' WHERE plan_id=$1`, standard)
	exec(`SELECT reconcile_active_test_model_protection($1)`, a)
	check(true, `{"m":"智商监测确认降智"}`)
	exec(`UPDATE scheduled_test_plans SET protection='{"intelligence":{"version":"newer"}}' WHERE id=$1`, standard)
	exec(`INSERT INTO intelligence_model_states(plan_id,account_id,model_key,blocked,passes,verdict,hold_version) VALUES($1,$2,'n',true,0,'confirmed','newer')`, standard, a)
	exec(`SELECT reconcile_active_test_model_protection($1)`, a)
	check(false, `{"m":"智商监测确认降智","n":"智商监测确认降智"}`)
	exec(`UPDATE intelligence_model_states SET hold_version='newer' WHERE plan_id=$1 AND model_key='m'`, standard)
	exec(`SELECT reconcile_active_test_model_protection($1)`, a)
	check(true, `{"m":"智商监测确认降智","n":"智商监测确认降智"}`)
	// Removing a standard rule must likewise respect an unrelated legacy hold.
	legacy = makePlan(`{}`)
	exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,model_key,blocked,reason) VALUES($1,$2,'x',true,'legacy')`, legacy, a)
	exec(`DELETE FROM scheduled_test_plans WHERE id=$1`, standard)
	check(false, `{"x":"legacy"}`)
	exec(`UPDATE accounts SET schedulable=false,status='inactive' WHERE id=$1`, a)
	exec(`DELETE FROM scheduled_test_plans WHERE id=$1`, legacy)
	var status string
	var schedulable bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT status,schedulable FROM accounts WHERE id=$1`, a).Scan(&status, &schedulable))
	require.False(t, schedulable)
	require.Equal(t, "inactive", status)
}
