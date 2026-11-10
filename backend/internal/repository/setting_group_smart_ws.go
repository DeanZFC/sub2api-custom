package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CompareAndSwapGroupSmartWS locks the live group first, then changes one settings
// row. This prevents deletion/platform changes between validation and persistence.
func (r *settingRepository) CompareAndSwapGroupSmartWS(ctx context.Context, groupID int64, key string, previous *string, next string) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	if err := lockLiveGroups(ctx, client, []int64{groupID}); err != nil {
		return false, err
	}
	g, err := client.Group.Query().Where(group.IDEQ(groupID), group.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, service.ErrGroupNotFound
		}
		return false, err
	}
	if g.Platform != service.PlatformOpenAI {
		return false, service.ErrGroupSmartWSUnsupported
	}
	if previous == nil {
		_, err = client.Setting.Create().SetKey(key).SetValue(next).SetUpdatedAt(time.Now()).Save(ctx)
		if ent.IsConstraintError(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	} else {
		count, err := client.Setting.Update().Where(setting.KeyEQ(key), setting.ValueEQ(*previous)).
			SetValue(next).SetUpdatedAt(time.Now()).Save(ctx)
		if err != nil {
			return false, err
		}
		if count != 1 {
			return false, nil
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
