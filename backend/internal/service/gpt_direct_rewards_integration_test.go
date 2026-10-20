//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type gptRewardTestCipher struct{}

func (gptRewardTestCipher) Encrypt(v string) (string, error) { return "encrypted:" + v, nil }
func (gptRewardTestCipher) Decrypt(v string) (string, error) {
	return strings.TrimPrefix(v, "encrypted:"), nil
}

func gptRewardDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GPT_DIRECT_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_DIRECT_TEST_DSN is required")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "gpt_reward_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); _ = admin.Close() })
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY); INSERT INTO users VALUES(1),(2)`)
	require.NoError(t, err)
	raw, err := migrations.FS.ReadFile("279_gpt_direct_lottery_rewards.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(raw))
		require.NoError(t, err)
	}
	return db
}
func rewardFixture(t *testing.T) (*GPTDirectService, *sql.DB, *atomic.Value) {
	t.Helper()
	db := gptRewardDB(t)
	state := &atomic.Value{}
	state.Store("queued")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		task := fmt.Sprintf(`{"id":"task_reward","product":"gpt","status":%q,"createdAt":%q}`, fmt.Sprint(state.Load()), time.Now().UTC().Format(time.RFC3339Nano))
		if r.URL.Path == "/api/v1/public/redeem" {
			fmt.Fprintf(w, `{"task":%s,"token":%q}`, task, strings.Repeat("a", 43))
		} else {
			fmt.Fprint(w, task)
		}
	}))
	t.Cleanup(server.Close)
	s, settings := newGPTDirectTestService(t, server)
	cfg, err := settings.GetGPTDirectConfig(context.Background())
	require.NoError(t, err)
	cfg.LotteryRewardEnabled = true
	cfg.LotteryChances = 2
	_, err = settings.SaveGPTDirectConfig(context.Background(), cfg)
	require.NoError(t, err)
	s.rewards = &gptDirectRewardStore{db: db, encryptor: gptRewardTestCipher{}, ready: true}
	return s, db, state
}
func rewardRedeem(t *testing.T, s *GPTDirectService, key string) GPTDirectReceipt {
	t.Helper()
	result, failure := s.Call(context.Background(), "redeem", GPTDirectInput{Code: "CODE-123", Session: "fake-session"}, 1, "", key)
	require.Nil(t, failure)
	receipt, ok := result.(GPTDirectReceipt)
	require.True(t, ok)
	return receipt
}
func rewardCount(t *testing.T, db *sql.DB, uid int64) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT COALESCE(SUM(chances),0) FROM qq_bot_lottery_chance_grants WHERE user_id=$1`, uid).Scan(&count))
	return count
}
func TestGPTDirectRewardsPersistAndSettleWithoutBrowser(t *testing.T) {
	s, db, state := rewardFixture(t)
	receipt := rewardRedeem(t, s, "request-reward")
	require.Equal(t, "pending", receipt.Task.LotteryReward.Status)
	require.Zero(t, rewardCount(t, db, 1))
	var ciphertext string
	require.NoError(t, db.QueryRow(`SELECT task_token_cipher FROM gpt_direct_redemptions`).Scan(&ciphertext))
	require.True(t, strings.HasPrefix(ciphertext, "encrypted:"))
	// Bearer access to a task does not transfer its reward to another account.
	state.Store("success")
	result, failure := s.Call(context.Background(), "task", GPTDirectInput{ID: receipt.Task.ID, Token: receipt.Token}, 2, "", "")
	require.Nil(t, failure)
	foreign, ok := result.(GPTDirectTask)
	require.True(t, ok)
	require.Nil(t, foreign.LotteryReward)
	require.Zero(t, rewardCount(t, db, 2))
	cfg, err := s.settings.GetGPTDirectConfig(context.Background())
	require.NoError(t, err)
	originalScope := gptDirectScope(cfg)
	cfg.LotteryChances = 9
	cfg.LotteryRewardEnabled = false
	cfg.Enabled = false
	cfg.BaseURL = "https://new-service.invalid"
	_, err = s.settings.SaveGPTDirectConfig(context.Background(), cfg)
	require.NoError(t, err)
	// Restarted worker uses the accepted receipt's original service and snapshot.
	restarted := NewGPTDirectService(s.settings)
	restarted.rewards = s.rewards
	_, err = db.Exec(`UPDATE gpt_direct_redemptions SET poll_after=NOW()`)
	require.NoError(t, err)
	restarted.pollRewards(context.Background())
	require.Equal(t, 2, rewardCount(t, db, 1))
	stale := receipt.Task
	stale.Status = "queued"
	reward, err := s.rewards.observe(context.Background(), originalScope, 1, receipt.Token, &stale)
	require.NoError(t, err)
	require.Equal(t, "success", stale.Status)
	require.Equal(t, "awarded", reward.Status)
	require.NoError(t, db.QueryRow(`SELECT task_token_cipher FROM gpt_direct_redemptions`).Scan(&ciphertext))
	require.Empty(t, ciphertext)
	var wg sync.WaitGroup
	var failures atomic.Int32
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task := receipt.Task
			task.Status = "success"
			reward, err := s.rewards.observe(context.Background(), originalScope, 1, receipt.Token, &task)
			if err != nil || reward == nil || reward.Status != "awarded" {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, failures.Load())
	// Repeated polls and process restarts cannot grant twice.
	restarted.pollRewards(context.Background())
	require.Equal(t, 2, rewardCount(t, db, 1))
}
func TestGPTDirectRewardSnapshotsAndReceiptOwnership(t *testing.T) {
	s, db, state := rewardFixture(t)
	receipt := rewardRedeem(t, s, "request-snapshot")
	cfg, err := s.settings.GetGPTDirectConfig(context.Background())
	require.NoError(t, err)
	cfg.LotteryChances = 7
	_, err = s.settings.SaveGPTDirectConfig(context.Background(), cfg)
	require.NoError(t, err)
	replay := rewardRedeem(t, s, "request-snapshot")
	require.Equal(t, 2, replay.Task.LotteryReward.Chances)
	_, failure := s.Call(context.Background(), "redeem", GPTDirectInput{Code: "CODE-123", Session: "fake-session"}, 2, "", "request-other-user")
	require.NotNil(t, failure)
	require.Zero(t, rewardCount(t, db, 2))
	// A failed task does not grant; a later operator retry can still complete it.
	state.Store("failed")
	_, failure = s.Call(context.Background(), "task", GPTDirectInput{ID: receipt.Task.ID, Token: receipt.Token}, 1, "", "")
	require.Nil(t, failure)
	require.Zero(t, rewardCount(t, db, 1))
	state.Store("success")
	_, err = db.Exec(`UPDATE gpt_direct_redemptions SET poll_after=NOW()`)
	require.NoError(t, err)
	s.pollRewards(context.Background())
	require.Equal(t, 2, rewardCount(t, db, 1))
	// Another request key returning the same upstream task keeps the first grant.
	replay = rewardRedeem(t, s, "request-new-key")
	require.Equal(t, 2, replay.Task.LotteryReward.Chances)
	require.Equal(t, 2, rewardCount(t, db, 1))
}
func TestGPTDirectRewardTransactionFailureCanRecover(t *testing.T) {
	s, db, state := rewardFixture(t)
	receipt := rewardRedeem(t, s, "request-recovery")
	state.Store("success")
	_, err := db.Exec(`ALTER TABLE qq_bot_lottery_chance_grants RENAME TO hidden_grants`)
	require.NoError(t, err)
	_, failure := s.Call(context.Background(), "task", GPTDirectInput{ID: receipt.Task.ID, Token: receipt.Token}, 1, "", "")
	require.NotNil(t, failure)
	var status, token string
	require.NoError(t, db.QueryRow(`SELECT status,task_token_cipher FROM gpt_direct_redemptions`).Scan(&status, &token))
	require.Equal(t, "queued", status)
	require.NotEmpty(t, token)
	_, err = db.Exec(`ALTER TABLE hidden_grants RENAME TO qq_bot_lottery_chance_grants`)
	require.NoError(t, err)
	result, failure := s.Call(context.Background(), "task", GPTDirectInput{ID: receipt.Task.ID, Token: receipt.Token}, 1, "", "")
	require.Nil(t, failure)
	task, ok := result.(GPTDirectTask)
	require.True(t, ok)
	require.Equal(t, "awarded", task.LotteryReward.Status)
	require.Equal(t, 2, rewardCount(t, db, 1))
}

func TestGPTDirectRewardDisabledSnapshotAndHistoricalReceipts(t *testing.T) {
	s, db, state := rewardFixture(t)
	ctx := context.Background()
	cfg, err := s.settings.GetGPTDirectConfig(ctx)
	require.NoError(t, err)
	cfg.LotteryRewardEnabled = false
	_, err = s.settings.SaveGPTDirectConfig(ctx, cfg)
	require.NoError(t, err)
	receipt := rewardRedeem(t, s, "disabled-request")
	require.Nil(t, receipt.Task.LotteryReward)
	cfg.LotteryRewardEnabled = true
	cfg.LotteryChances = 9
	_, err = s.settings.SaveGPTDirectConfig(ctx, cfg)
	require.NoError(t, err)
	state.Store("success")
	replay := rewardRedeem(t, s, "disabled-request")
	require.Nil(t, replay.Task.LotteryReward)
	require.Zero(t, rewardCount(t, db, 1))
	// A previously untracked historical successful receipt is not a new recharge.
	attempt, err := s.rewards.prepare(ctx, cfg, 1, "OLDER-CDK", "old-request")
	require.NoError(t, err)
	old := GPTDirectReceipt{Task: GPTDirectTask{ID: "historical-task", Status: "success", Product: "gpt", CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, Token: strings.Repeat("b", 43)}
	require.NoError(t, s.rewards.record(ctx, attempt, &old))
	reward, err := s.rewards.observe(ctx, gptDirectScope(cfg), 1, old.Token, &old.Task)
	require.NoError(t, err)
	require.Nil(t, reward)
	require.Zero(t, rewardCount(t, db, 1))
}

func TestGPTDirectRewardSameCDKCannotGrantUnderAnotherTaskOrUser(t *testing.T) {
	s, db, state := rewardFixture(t)
	state.Store("success")
	first := rewardRedeem(t, s, "first-request")
	require.Equal(t, "awarded", first.Task.LotteryReward.Status)
	ctx := context.Background()
	cfg, err := s.settings.GetGPTDirectConfig(ctx)
	require.NoError(t, err)
	for _, uid := range []int64{1, 2} {
		attempt, err := s.rewards.prepare(ctx, cfg, uid, "CODE-123", fmt.Sprintf("another-%d", uid))
		require.NoError(t, err)
		next := first
		next.Task.ID = fmt.Sprintf("another_task_%d", uid)
		next.Task.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		require.NoError(t, s.rewards.record(ctx, attempt, &next))
		reward, err := s.rewards.observe(ctx, gptDirectScope(cfg), uid, next.Token, &next.Task)
		require.NoError(t, err)
		if uid == 1 {
			require.Equal(t, "awarded", reward.Status)
		} else {
			require.Equal(t, "not_eligible", reward.Status)
		}
	}
	require.Equal(t, 2, rewardCount(t, db, 1))
	require.Zero(t, rewardCount(t, db, 2))
}

func TestGPTDirectRewardResponseLossReusesSnapshot(t *testing.T) {
	s, db, _ := rewardFixture(t)
	ctx := context.Background()
	cfg, err := s.settings.GetGPTDirectConfig(ctx)
	require.NoError(t, err)
	// Reserve an intent as if the upstream accepted it but its response was lost.
	_, err = s.rewards.prepare(ctx, cfg, 1, "CODE-123", "lost-response")
	require.NoError(t, err)
	cfg.LotteryChances = 8
	_, err = s.settings.SaveGPTDirectConfig(ctx, cfg)
	require.NoError(t, err)
	receipt := rewardRedeem(t, s, "lost-response")
	require.Equal(t, 2, receipt.Task.LotteryReward.Chances)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM gpt_direct_redemptions`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestGPTDirectLostReceiptVerificationRequiresOriginalOwnerAndKey(t *testing.T) {
	s, _, _ := rewardFixture(t)
	ctx := context.Background()
	var queryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/public/verify" {
			w.WriteHeader(409)
			_, _ = fmt.Fprint(w, `{"error":{"code":"CODE_USED"}}`)
			return
		}
		queryCalls.Add(1)
		_, _ = fmt.Fprint(w, `{"valid":true,"product":"gpt","plan":"plus","status":"used"}`)
	}))
	defer server.Close()
	cfg, err := s.settings.GetGPTDirectConfig(ctx)
	require.NoError(t, err)
	cfg.BaseURL = server.URL
	_, err = s.settings.SaveGPTDirectConfig(ctx, cfg)
	require.NoError(t, err)
	_, err = s.rewards.prepare(ctx, cfg, 1, "CODE-123", "original-request")
	require.NoError(t, err)
	for _, tc := range []struct {
		uid int64
		key string
	}{{2, "original-request"}, {1, "different-request"}, {1, ""}} {
		_, failure := s.Call(ctx, "verify", GPTDirectInput{Code: "CODE-123"}, tc.uid, "", tc.key)
		require.NotNil(t, failure)
		require.Equal(t, "CODE_USED", failure.Reason)
	}
	require.Zero(t, queryCalls.Load())
	_, failure := s.Call(ctx, "verify", GPTDirectInput{Code: "CODE-123"}, 1, "", "original-request")
	require.Nil(t, failure)
	require.Equal(t, int32(1), queryCalls.Load())
}
