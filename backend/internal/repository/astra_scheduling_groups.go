package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Before is the membership before this mode took ownership; After is the last
// membership written/observed by this mode. Nil means absent. Keep both across
// route expiry and recovery so leaving the mode can undo only owned changes.
type astraGroupMembership struct {
	GroupID int64                 `json:"group_id"`
	Before  *astraSchedulingGroup `json:"before"`
	After   *astraSchedulingGroup `json:"after"`
}

func astraSchedulingGroups(ctx context.Context, tx *sql.Tx, id int64) ([]astraSchedulingGroup, error) {
	rows, err := tx.QueryContext(ctx, `SELECT account_id,group_id,priority,created_at
 FROM account_groups WHERE account_id=$1 ORDER BY group_id FOR UPDATE`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var groups []astraSchedulingGroup
	for rows.Next() {
		var g astraSchedulingGroup
		if err = rows.Scan(&g.AccountID, &g.GroupID, &g.Priority, &g.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func astraMembershipEqual(a, b *astraSchedulingGroup) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AccountID == b.AccountID && a.GroupID == b.GroupID && a.Priority == b.Priority &&
		a.CreatedAt.Equal(b.CreatedAt)
}

// Runs inside the settings/account transaction. A conflict rolls back the whole
// action, including earlier group writes, ownership and cache invalidation.
func reconcileAstraGroups(ctx context.Context, tx *sql.Tx, id int64, state *astraActionState, groups []astraSchedulingGroup, ready, restore bool) (changed, dirty bool, err error) {
	current := make(map[int64]*astraSchedulingGroup, len(groups))
	for i := range groups {
		current[groups[i].GroupID] = &groups[i]
	}
	if len(state.Memberships) == 0 {
		// Upgrade legacy removal-only ownership without losing original priority,
		// timestamps or per-membership model restrictions.
		removed := make(map[int64]*astraSchedulingGroup, len(state.Removed))
		for i := range state.Removed {
			removed[state.Removed[i].GroupID] = &state.Removed[i]
		}
		for _, gid := range state.GroupIDs {
			m := astraGroupMembership{GroupID: gid, Before: current[gid], After: current[gid]}
			if original, ok := removed[gid]; ok {
				m.Before, m.After = original, nil
			}
			state.Memberships = append(state.Memberships, m)
		}
		state.Removed = nil
		dirty = true
	}
	for _, m := range state.Memberships {
		if !astraMembershipEqual(current[m.GroupID], m.After) {
			return false, false, errors.New("astra_restore_conflict")
		}
	}
	for i := range state.Memberships {
		m := &state.Memberships[i]
		var desired *astraSchedulingGroup
		if restore || ready {
			desired = m.Before
		}
		if !restore && ready && desired == nil {
			if m.After != nil {
				desired = m.After
			} else {
				// New memberships follow the normal group defaults. Existing
				// memberships retain their priority and model restrictions.
				desired = &astraSchedulingGroup{AccountID: id, GroupID: m.GroupID, Priority: 50}
			}
		}
		if astraMembershipEqual(m.After, desired) {
			continue
		}
		if desired == nil {
			if _, err = tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, id, m.GroupID); err != nil {
				return false, false, err
			}
		} else {
			if err = lockLiveGroups(ctx, tx, []int64{m.GroupID}); err != nil {
				return false, false, errors.New("astra_restore_conflict")
			}
			var created any
			if !desired.CreatedAt.IsZero() {
				created = desired.CreatedAt
			}
			inserted := *desired
			err = tx.QueryRowContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,created_at)
 VALUES($1,$2,$3,COALESCE($4::timestamptz,clock_timestamp()))
 ON CONFLICT (account_id,group_id) DO NOTHING RETURNING created_at`, id, m.GroupID, desired.Priority, created).Scan(&inserted.CreatedAt)
			if errors.Is(err, sql.ErrNoRows) {
				return false, false, errors.New("astra_restore_conflict")
			}
			if err != nil {
				return false, false, err
			}
			desired = &inserted
		}
		m.After = desired
		changed, dirty = true, true
	}
	return changed, dirty, nil
}

// Snapshot only the membership columns supported by this installation.
type astraSchedulingGroup struct {
	AccountID int64     `json:"account_id"`
	GroupID   int64     `json:"group_id"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}
