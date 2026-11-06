package service

import "context"

type LatestSchedulerAccountsRepository interface {
	WithLatestSchedulerAccounts(context.Context, []int64, func([]Account) error) error
}
type outboxAuthoritativeSchedulerCache struct {
	SchedulerCache
	source LatestSchedulerAccountsRepository
}

func (c *outboxAuthoritativeSchedulerCache) GetAccounts(ctx context.Context, ids []int64) (map[int64]*Account, error) {
	if r, ok := c.SchedulerCache.(schedulerAccountBatchReader); ok {
		return r.GetAccounts(ctx, ids)
	}
	out := make(map[int64]*Account, len(ids))
	for _, id := range ids {
		a, err := c.GetAccount(ctx, id)
		if err != nil {
			return nil, err
		}
		if a != nil {
			out[id] = a
		}
	}
	return out, nil
}
func (c *outboxAuthoritativeSchedulerCache) SetAccount(ctx context.Context, a *Account) error {
	if !isOutboxRetryPublication(ctx) || a == nil {
		return c.SchedulerCache.SetAccount(ctx, a)
	}
	return c.source.WithLatestSchedulerAccounts(ctx, []int64{a.ID}, func(fresh []Account) error {
		if len(fresh) == 0 {
			return c.SchedulerCache.DeleteAccount(ctx, a.ID)
		}
		return c.SchedulerCache.SetAccount(ctx, &fresh[0])
	})
}
func (c *outboxAuthoritativeSchedulerCache) SetSnapshot(ctx context.Context, b SchedulerBucket, t SchedulerBucketWriteToken, accounts []Account) error {
	if !isOutboxRetryPublication(ctx) {
		return c.SchedulerCache.SetSnapshot(ctx, b, t, accounts)
	}
	ids := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	return c.source.WithLatestSchedulerAccounts(ctx, ids, func(fresh []Account) error {
		return c.SchedulerCache.SetSnapshot(ctx, b, t, filterOutboxRetryBucketAccounts(ctx, b, fresh))
	})
}
func (c *outboxAuthoritativeSchedulerCache) SetSnapshotAndReturnAccountIDs(ctx context.Context, b SchedulerBucket, t SchedulerBucketWriteToken, accounts []Account) ([]int64, error) {
	if w, ok := c.SchedulerCache.(schedulerSnapshotAccountIDWriter); ok && !isOutboxRetryPublication(ctx) {
		return w.SetSnapshotAndReturnAccountIDs(ctx, b, t, accounts)
	}
	var published []int64
	publish := func(fresh []Account) error {
		if isOutboxRetryPublication(ctx) {
			fresh = filterOutboxRetryBucketAccounts(ctx, b, fresh)
		}
		if err := c.SchedulerCache.SetSnapshot(ctx, b, t, fresh); err != nil {
			return err
		}
		for _, a := range fresh {
			published = append(published, a.ID)
		}
		return nil
	}
	if !isOutboxRetryPublication(ctx) {
		err := publish(accounts)
		return published, err
	}
	ids := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	err := c.source.WithLatestSchedulerAccounts(ctx, ids, publish)
	return published, err
}
func (c *outboxAuthoritativeSchedulerCache) SetSnapshotByAccountIDs(ctx context.Context, b SchedulerBucket, t SchedulerBucketWriteToken, ids []int64) error {
	if w, ok := c.SchedulerCache.(schedulerSnapshotAccountIDWriter); ok && !isOutboxRetryPublication(ctx) {
		return w.SetSnapshotByAccountIDs(ctx, b, t, ids)
	}
	return c.source.WithLatestSchedulerAccounts(ctx, ids, func(fresh []Account) error {
		return c.SchedulerCache.SetSnapshot(ctx, b, t, filterOutboxRetryBucketAccounts(ctx, b, fresh))
	})
}
