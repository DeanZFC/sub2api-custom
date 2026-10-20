package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// GPTDirectLotteryReward reports a server-confirmed grant, never a browser claim.
type GPTDirectLotteryReward struct {
	Chances int    `json:"chances"`
	Status  string `json:"status"`
}

type gptDirectRewardStore struct {
	db        *sql.DB
	encryptor SecretEncryptor
	ready     bool
}

type gptDirectAttempt struct {
	ID          int64
	Scope       string
	BaseURL     string
	Tenant      string
	UserID      int64
	CodeHash    string
	Chances     int
	TaskID      string
	TokenCipher string
	CreatedAt   time.Time
}

func gptDirectHash(value string) string       { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }
func gptDirectScope(c GPTDirectConfig) string { return gptDirectHash(c.BaseURL + ":" + c.TenantSlug) }

func (r *gptDirectRewardStore) canResume(ctx context.Context, c GPTDirectConfig, uid int64, code, key string) bool {
	if uid <= 0 || !gptDirectKeyPattern.MatchString(key) {
		return false
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gpt_direct_redemptions WHERE scope=$1 AND user_id=$2 AND request_key=$3 AND code_hash=$4)`, gptDirectScope(c), uid, gptDirectHash(fmt.Sprintf("%d:%s", uid, key)), gptDirectHash(code)).Scan(&exists)
	return err == nil && exists
}

func (r *gptDirectRewardStore) prepare(ctx context.Context, c GPTDirectConfig, uid int64, code, key string) (gptDirectAttempt, error) {
	a := gptDirectAttempt{Scope: gptDirectScope(c), BaseURL: c.BaseURL, Tenant: c.TenantSlug, UserID: uid, CodeHash: gptDirectHash(code)}
	chances := 0
	if c.LotteryRewardEnabled {
		chances = c.LotteryChances
	}
	// UPSERT returns the original snapshot on replay, including a disabled reward.
	err := r.db.QueryRowContext(ctx, `INSERT INTO gpt_direct_redemptions(scope,service_url,tenant_slug,user_id,request_key,code_hash,chances)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(scope,request_key) DO UPDATE SET request_key=EXCLUDED.request_key
 RETURNING id,code_hash,chances,created_at`, a.Scope, a.BaseURL, a.Tenant, uid, gptDirectHash(fmt.Sprintf("%d:%s", uid, key)), a.CodeHash, chances).Scan(&a.ID, &a.CodeHash, &a.Chances, &a.CreatedAt)
	if err == nil && a.CodeHash != gptDirectHash(code) {
		return a, errors.New("idempotency conflict")
	}
	return a, err
}

func (r *gptDirectRewardStore) record(ctx context.Context, a gptDirectAttempt, receipt *GPTDirectReceipt) error {
	cipher := ""
	if a.Chances > 0 {
		var err error
		cipher, err = r.encryptor.Encrypt(receipt.Token)
		if err != nil {
			return err
		}
	}
	// Old requests made before this feature was installed must not earn rewards
	// merely because their original upstream idempotency key is replayed.
	created, err := time.Parse(time.RFC3339Nano, receipt.Task.CreatedAt)
	if err != nil || created.Before(a.CreatedAt.Add(-30*time.Second)) {
		a.Chances = 0
		cipher = ""
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize receipt attachment across processes, including upstream replays
	// that return the same task under a different request key.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,279))`, a.Scope+":"+receipt.Task.ID); err != nil {
		return err
	}
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM gpt_direct_redemptions WHERE scope=$1 AND task_id=$2`, a.Scope, receipt.Task.ID).Scan(&owner)
	if err == nil {
		if owner != a.UserID {
			return errors.New("task belongs to another user")
		}
		// Keep the first receipt's entitlement and token binding.
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE gpt_direct_redemptions SET task_id=$2,task_token_cipher=$3,token_hash=$4,chances=$5,status=$6,updated_at=NOW() WHERE id=$1 AND task_id IS NULL`, a.ID, receipt.Task.ID, cipher, gptDirectHash(receipt.Token), a.Chances, receipt.Task.Status)
	if err != nil {
		return err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("request already bound to another task")
	}
	return tx.Commit()
}

// observe both records terminal status and issues the immutable grant in one
// transaction. Scope, owner and token must match a receipt we accepted ourselves.
func (r *gptDirectRewardStore) observe(ctx context.Context, scope string, uid int64, token string, task *GPTDirectTask) (*GPTDirectLotteryReward, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	var codeHash string
	var chances int
	var createdAt time.Time
	var previousStatus string
	err = tx.QueryRowContext(ctx, `SELECT id,code_hash,chances,created_at,status FROM gpt_direct_redemptions WHERE scope=$1 AND task_id=$2 AND user_id=$3 AND token_hash=$4 FOR UPDATE`, scope, task.ID, uid, gptDirectHash(token)).Scan(&id, &codeHash, &chances, &createdAt, &previousStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A slower poll may return an old state after success was already settled.
	if previousStatus == "success" {
		task.Status = "success"
		task.Progress = 100
	}
	reward := &GPTDirectLotteryReward{Chances: chances, Status: "pending"}
	terminal := task.Status == "success" || task.Status == "failed" || task.Status == "cancelled"
	if task.Status == "success" && chances > 0 {
		source := "gpt-direct:" + scope + ":" + codeHash
		_, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES($1,$2,$3) ON CONFLICT(source_key) DO NOTHING`, source, uid, chances)
		if err != nil {
			return nil, err
		}
		var owner int64
		if err = tx.QueryRowContext(ctx, `SELECT user_id,chances FROM qq_bot_lottery_chance_grants WHERE source_key=$1`, source).Scan(&owner, &reward.Chances); err != nil {
			return nil, err
		}
		if owner == uid {
			reward.Status = "awarded"
		} else {
			reward.Status = "not_eligible"
			reward.Chances = 0
		}
	} else if terminal {
		reward.Status = "not_eligible"
	}
	// CTAI operators may retry failed tasks. Keep checking them hourly for 30
	// days; successful settlement erases the capability immediately.
	delay := 60
	if terminal {
		delay = 3600
	}
	clearToken := task.Status == "success" || chances == 0 || (terminal && time.Since(createdAt) > 30*24*time.Hour)
	_, err = tx.ExecContext(ctx, `UPDATE gpt_direct_redemptions SET status=$2,task_token_cipher=CASE WHEN $3 THEN '' ELSE task_token_cipher END,poll_after=NOW()+($4 * INTERVAL '1 second'),updated_at=NOW() WHERE id=$1`, id, task.Status, clearToken, delay)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if chances == 0 {
		return nil, nil
	}
	return reward, nil
}

func (r *gptDirectRewardStore) claim(ctx context.Context) (gptDirectAttempt, error) {
	var a gptDirectAttempt
	err := r.db.QueryRowContext(ctx, `UPDATE gpt_direct_redemptions SET poll_after=NOW()+INTERVAL '2 minutes'
 WHERE id=(SELECT id FROM gpt_direct_redemptions WHERE chances>0 AND task_token_cipher<>'' AND poll_after<=NOW()
 ORDER BY poll_after,id LIMIT 1 FOR UPDATE SKIP LOCKED)
 RETURNING id,scope,service_url,tenant_slug,user_id,code_hash,chances,task_id,task_token_cipher`).Scan(&a.ID, &a.Scope, &a.BaseURL, &a.Tenant, &a.UserID, &a.CodeHash, &a.Chances, &a.TaskID, &a.TokenCipher)
	return a, err
}

// One persisted lease is claimed at a time so slow upstream requests cannot
// cause other processes to immediately reclaim the rest of a large batch.
func (s *GPTDirectService) pollRewards(ctx context.Context) {
	for range 10 {
		if ctx.Err() != nil {
			return
		}
		a, err := s.rewards.claim(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			logger.L().Warn("gpt_direct.reward_poll_store_unavailable")
			return
		}
		token, err := s.rewards.encryptor.Decrypt(a.TokenCipher)
		if err != nil {
			logger.L().Warn("gpt_direct.reward_poll_decrypt_failed")
			continue
		}
		result, failure := s.call(ctx, GPTDirectConfig{Enabled: true, BaseURL: a.BaseURL, TenantSlug: a.Tenant}, "task", GPTDirectInput{ID: a.TaskID, Token: token}, a.UserID, "", "")
		if failure != nil {
			delay := 120
			if failure.RetryAfter > delay {
				delay = failure.RetryAfter
			}
			if _, err = s.rewards.db.ExecContext(ctx, `UPDATE gpt_direct_redemptions SET poll_after=NOW()+($2 * INTERVAL '1 second') WHERE id=$1`, a.ID, delay); err != nil {
				logger.L().Warn("gpt_direct.reward_poll_backoff_failed")
			}
			continue
		}
		task, ok := result.(GPTDirectTask)
		if !ok {
			continue
		}
		if _, err = s.rewards.observe(ctx, a.Scope, a.UserID, token, &task); err != nil {
			logger.L().Warn("gpt_direct.reward_settlement_failed")
		}
	}
}

// ProvideGPTDirectService uses the same persistent encryption key as other
// protected settings. Only task lookup capabilities are stored, never Sessions.
func ProvideGPTDirectService(settings *SettingService, db *sql.DB, encryptor SecretEncryptor, cfg *config.Config) *GPTDirectService {
	s := NewGPTDirectService(settings)
	s.rewards = &gptDirectRewardStore{db: db, encryptor: encryptor, ready: cfg.Totp.EncryptionKeyConfigured}
	s.startRewards()
	return s
}
func (s *GPTDirectService) RewardsReady() bool { return s.rewards != nil && s.rewards.ready }

type gptDirectRewardWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
	stop   sync.Once
}

func (s *GPTDirectService) startRewards() {
	ctx, cancel := context.WithCancel(context.Background())
	s.worker = &gptDirectRewardWorker{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.worker.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.RewardsReady() {
					s.pollRewards(ctx)
				}
			}
		}
	}()
}
func (s *GPTDirectService) Stop() {
	if s == nil || s.worker == nil {
		return
	}
	s.worker.stop.Do(s.worker.cancel)
	<-s.worker.done
}
