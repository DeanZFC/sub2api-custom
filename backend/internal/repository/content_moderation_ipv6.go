package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *proxyRepository) AllocateContentModerationIPv6(ctx context.Context, keyHash string) (service.ContentModerationKeyIPv6, error) {
	var result service.ContentModerationKeyIPv6
	tx := dbent.TxFromContext(ctx)
	if tx == nil || !r.ipv6Supported {
		return result, fmt.Errorf("审计 Key IPv6 分配需要配置写事务和 OVH 出口服务")
	}
	id, err := r.allocateOVHIPv6Lease(ctx, "", false)
	if err != nil {
		return result, err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE ovh_ipv6_leases SET owner_moderation_key=$2 WHERE proxy_id=$1 AND owner_account_id IS NULL AND state='bound'`, id, keyHash); err != nil {
		return result, err
	}
	result.ProxyID = id
	err = scanSingleRow(ctx, tx.Client(), `SELECT host(address) FROM ovh_ipv6_leases WHERE proxy_id=$1`, []any{id}, &result.Address)
	return result, err
}

func (r *proxyRepository) RetireContentModerationIPv6(ctx context.Context, keyHash string, proxyID int64) error {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("审计 Key IPv6 退役需要配置写事务")
	}
	result, err := tx.Client().ExecContext(ctx, `UPDATE ovh_ipv6_leases SET state='retiring',confirmed=FALSE,next_attempt_at=NOW()
		WHERE proxy_id=$1 AND owner_moderation_key=$2 AND state='bound'`, proxyID, keyHash)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("审计 Key IPv6 绑定已变化，请刷新后重试")
	}
	_, err = tx.Client().ExecContext(ctx, `UPDATE proxies SET status='inactive',deleted_at=NOW(),updated_at=NOW() WHERE id=$1`, proxyID)
	return err
}
