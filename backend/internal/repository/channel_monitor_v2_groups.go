package repository

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Minute and rollup facts use positive IDs for ordinary groups, zero for
// ungrouped traffic, and negative IDs for an immutable set of quality groups.
const channelMonitorV2UsageGroup = "COALESCE(-ul.quality_group_set_id, ul.group_id, 0)"
const channelMonitorV2ErrorGroup = "COALESCE(-current_error.quality_group_set_id, current_error.group_id, 0)"

func channelMonitorV2GroupMatch(group, set, param string) string {
	return "(" + group + " = ANY(" + param + ") OR " + set + " IN (SELECT id FROM channel_quality_group_sets WHERE group_ids && " + param + "))"
}

func channelMonitorV2GroupsInDimension(by service.ChannelMonitorV2GroupBy) bool {
	return by == service.ChannelMonitorV2GroupByPlatformGroup || by == service.ChannelMonitorV2GroupByPlatformGroupModel
}

// Expand a set only for per-group views. With an allowlist, number only the
// visible groups so dimensions can count the first one toward platform totals.
func channelMonitorV2ProjectGroups(key string, expand bool, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, args *[]any) (group, join string) {
	if !expand {
		return key, ""
	}
	ids, empty := channelMonitorV2ScopedGroupIDs(filter, cfg)
	where := ""
	if empty {
		where = " WHERE FALSE"
	} else if len(ids) > 0 {
		*args = append(*args, pq.Array(ids))
		where = fmt.Sprintf(" WHERE member = ANY($%d)", len(*args))
	}
	return "visible.group_id", " LEFT JOIN channel_quality_group_sets qs ON qs.id = -(" + key + ") CROSS JOIN LATERAL (SELECT member AS group_id, row_number() OVER (ORDER BY member) AS ordinal FROM unnest(COALESCE(qs.group_ids, ARRAY[" + key + "])) AS member" + where + ") visible "
}
