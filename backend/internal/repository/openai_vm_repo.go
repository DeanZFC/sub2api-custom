package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type openAIVMRepository struct{ db *sql.DB }

func NewOpenAIVMRepository(db *sql.DB) service.OpenAIVMRepository { return &openAIVMRepository{db: db} }

func (r *openAIVMRepository) GetConfig(ctx context.Context) (*service.OpenAIVMConfig, error) {
	c := &service.OpenAIVMConfig{}
	err := r.db.QueryRowContext(ctx, `SELECT enabled,base_url,proxy_url,encrypted_secret FROM openai_vm_config WHERE id=1`).Scan(&c.Enabled, &c.BaseURL, &c.ProxyURL, &c.EncryptedSecret)
	c.SecretConfigured = c.EncryptedSecret != ""
	return c, err
}
func (r *openAIVMRepository) SaveConfig(ctx context.Context, c *service.OpenAIVMConfig) error {
	_, err := r.db.ExecContext(ctx, `UPDATE openai_vm_config SET enabled=$1,base_url=$2,proxy_url=$3,encrypted_secret=$4,updated_at=NOW() WHERE id=1`, c.Enabled, c.BaseURL, c.ProxyURL, c.EncryptedSecret)
	return err
}
func (r *openAIVMRepository) CountBindings(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_vm_bindings`).Scan(&n)
	return n, err
}

// Transaction-scoped advisory locks cannot leak when a process/request dies.
// The global shared lock prevents config changes during account operations.
func (r *openAIVMRepository) WithLock(ctx context.Context, id int64, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep ownership until cancellation cleanup has finished. A transaction
	// using the HTTP request context would auto-unlock before fail() can persist
	// the operation outcome, allowing it to overwrite a newer retry.
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 150*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(lockCtx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var ok bool
	globalSQL := `SELECT pg_try_advisory_xact_lock_shared(1828121101,0)`
	if id == 0 {
		globalSQL = `SELECT pg_try_advisory_xact_lock(1828121101,0)`
	}
	if err = tx.QueryRowContext(lockCtx, globalSQL).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return infraerrors.Conflict("OPENAI_VM_BUSY", "VM configuration is being changed; retry shortly")
	}
	if id != 0 {
		if err = tx.QueryRowContext(lockCtx, `SELECT pg_try_advisory_xact_lock($1)`, int64(0x564d000000000000)+id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return infraerrors.Conflict("OPENAI_VM_BUSY", "An operation is already running for this account")
		}
	}
	if err = fn(); err != nil {
		return err
	}
	return tx.Commit()
}

const openAIVMBindingSelect = `SELECT account_id,managed_proxy_id,original_proxy_id,original_proxy_ids,original_fallback_id,runtime_type,slot_id,status,last_error,created_at,updated_at FROM openai_vm_bindings`

func scanOpenAIVMBinding(row interface{ Scan(...any) error }) (*service.OpenAIVMBinding, error) {
	b := &service.OpenAIVMBinding{}
	var ids []byte
	err := row.Scan(&b.AccountID, &b.ManagedProxyID, &b.OriginalProxyID, &ids, &b.OriginalFallbackID, &b.RuntimeType, &b.SlotID, &b.Status, &b.LastError, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(ids, &b.OriginalProxyIDs); err != nil {
		return nil, err
	}
	return b, nil
}
func (r *openAIVMRepository) GetBinding(ctx context.Context, id int64) (*service.OpenAIVMBinding, error) {
	return scanOpenAIVMBinding(r.db.QueryRowContext(ctx, openAIVMBindingSelect+` WHERE account_id=$1`, id))
}

func (r *openAIVMRepository) Reserve(ctx context.Context, id int64, runtime string, p *service.Proxy) (*service.OpenAIVMBinding, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var platform, kind string
	var parent, original, fallback *int64
	err = tx.QueryRowContext(ctx, `SELECT platform,type,parent_account_id,proxy_id,proxy_fallback_origin_id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&platform, &kind, &parent, &original, &fallback)
	if err != nil {
		return nil, err
	}
	if platform != service.PlatformOpenAI || kind != service.AccountTypeOAuth || parent != nil {
		return nil, infraerrors.BadRequest("OPENAI_VM_INELIGIBLE", "Only OpenAI OAuth parent accounts support VM slots")
	}
	if original != nil {
		var protocol, status string
		var expiry *time.Time
		if err = tx.QueryRowContext(ctx, `SELECT protocol,status,expires_at FROM proxies WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, *original).Scan(&protocol, &status, &expiry); err != nil {
			return nil, err
		}
		if (protocol != "socks5" && protocol != "socks5h") || status != service.StatusActive || (expiry != nil && !expiry.After(time.Now())) {
			return nil, infraerrors.BadRequest("OPENAI_VM_PROXY_UNAVAILABLE", "A live SOCKS5 proxy or explicitly direct route is required")
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT proxy_id FROM account_proxies WHERE account_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var pid int64
		if err = rows.Scan(&pid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, pid)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 1 {
		return nil, infraerrors.BadRequest("OPENAI_VM_PROXY_POOL", "Select one outbound proxy before creating a dedicated VM slot")
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO proxies(name,protocol,host,port,username,password,status,fallback_mode,expiry_warn_days,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'active','none',7,NOW(),NOW()) RETURNING id`, fmt.Sprintf("OpenAI VM account %d", id), p.Protocol, p.Host, p.Port, p.Username, p.Password).Scan(&p.ID)
	if err != nil {
		return nil, err
	}
	// Remove pools before installing the binding, whose trigger forbids pool edits.
	_, err = tx.ExecContext(ctx, `DELETE FROM account_proxies WHERE account_id IN(SELECT id FROM accounts WHERE id=$1 OR parent_account_id=$1)`, id)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_vm_bindings(account_id,managed_proxy_id,original_proxy_id,original_proxy_ids,original_fallback_id,runtime_type) VALUES($1,$2,$3,$4::jsonb,$5,$6)`, id, p.ID, original, raw, fallback, runtime)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=NULL,extra=COALESCE(extra,'{}'::jsonb)||'{"openai_vm_bound":true}'::jsonb,updated_at=NOW() WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, p.ID)
	if err != nil {
		return nil, err
	}
	if err = enqueueOpenAIVMSnapshots(ctx, tx, id); err != nil {
		return nil, err
	}
	b, err := scanOpenAIVMBinding(tx.QueryRowContext(ctx, openAIVMBindingSelect+` WHERE account_id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return b, nil
}

func (r *openAIVMRepository) SetState(ctx context.Context, id int64, status, slotID, message string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE openai_vm_bindings SET status=$2,slot_id=CASE WHEN $3='' THEN slot_id ELSE $3 END,last_error=$4,updated_at=NOW() WHERE account_id=$1`, id, status, slotID, message)
	return err
}

func (r *openAIVMRepository) Restore(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Same parent-first lock order as Reserve and the shadow INSERT guard.
	var lockedID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		return err
	}
	b, err := scanOpenAIVMBinding(tx.QueryRowContext(ctx, openAIVMBindingSelect+` WHERE account_id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	if b.OriginalProxyID != nil {
		var usable bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM proxies WHERE id=$1 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()))`, *b.OriginalProxyID).Scan(&usable)
		if err != nil {
			return err
		}
		if !usable {
			return infraerrors.Conflict("OPENAI_VM_ORIGINAL_PROXY_UNAVAILABLE", "Restore the original proxy before detaching this VM slot")
		}
	}
	// The route guard makes this CAS invariant explicit even for manual DB edits.
	var mismatch bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL AND proxy_id IS DISTINCT FROM $2)`, id, b.ManagedProxyID).Scan(&mismatch)
	if err != nil {
		return err
	}
	if mismatch {
		return infraerrors.Conflict("OPENAI_VM_ROUTE_CHANGED", "Account route changed; refusing to overwrite it")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM openai_vm_bindings WHERE account_id=$1`, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=$3,extra=COALESCE(extra,'{}'::jsonb)-'openai_vm_bound',updated_at=NOW() WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, b.OriginalProxyID, b.OriginalFallbackID)
	if err != nil {
		return err
	}
	for i, pid := range b.OriginalProxyIDs {
		_, err = tx.ExecContext(ctx, `INSERT INTO account_proxies(account_id,proxy_id,position) SELECT id,$2,$3 FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, pid, i)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE proxies SET deleted_at=NOW(),status='disabled',updated_at=NOW() WHERE id=$1`, b.ManagedProxyID)
	if err != nil {
		return err
	}
	if err = enqueueOpenAIVMSnapshots(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func enqueueOpenAIVMSnapshots(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id) SELECT $2,id FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, service.SchedulerOutboxEventAccountChanged)
	return err
}
