package repository

import "context"

func (r *accountRepository) HasRetiredVMRoute(ctx context.Context, id int64) (bool, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM accounts a LEFT JOIN accounts p ON p.id=a.parent_account_id
 WHERE a.id=$1 AND (
  a.extra->>'openai_vm_bound'='true' OR a.extra->>'openai_vm_retired'='true'
  OR p.extra->>'openai_vm_bound'='true' OR p.extra->>'openai_vm_retired'='true'
  OR EXISTS(SELECT 1 FROM openai_vm_bindings b WHERE b.account_id=COALESCE(a.parent_account_id,a.id) OR b.managed_proxy_id=a.proxy_id)
 ))`, id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	var blocked bool
	if rows.Next() {
		err = rows.Scan(&blocked)
	}
	if err != nil {
		return false, err
	}
	return blocked, rows.Err()
}

func (r *proxyRepository) IsRetiredVMProxy(ctx context.Context, id int64) (bool, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM openai_vm_bindings WHERE managed_proxy_id=$1)`, id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	var blocked bool
	if rows.Next() {
		err = rows.Scan(&blocked)
	}
	if err != nil {
		return false, err
	}
	return blocked, rows.Err()
}
