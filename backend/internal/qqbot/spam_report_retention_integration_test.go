//go:build integration

package qqbot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpamReportRetentionCapKeepsEqualTimestampProtectionAheadOfOrdinarySources(t *testing.T) {
	for _, protection := range []string{"administrator", "unknown_role", "robot"} {
		for _, arrival := range []string{"before_cap", "after_cap"} {
			t.Run(protection+"/"+arrival, func(t *testing.T) {
				h := newSpamSourceHarness(t)
				original := spamSourceTestEvent("zz-retained-original")
				stamp, err := json.Marshal(time.Now().UTC().Add(-8 * time.Minute).Truncate(time.Second).Format(time.RFC3339))
				require.NoError(t, err)
				original.Timestamp = stamp
				id, source := h.stage(t, original)
				// All callbacks share QQ's second-precision timestamp. The protective
				// message deliberately sorts below the ordinary original by message ID.
				later := spamSourceTestEvent("aa-protective-observation")
				later.Timestamp = stamp
				later.Content = "今天的安排已收到。"
				protected := later
				switch protection {
				case "administrator":
					protected.Author.MemberRole = "admin"
				case "unknown_role":
					protected.Author.MemberRole = ""
				case "robot":
					protected.Author.Bot = true
				}
				if arrival == "before_cap" {
					h.enqueue(t, protected)
					require.Nil(t, h.source(t, original.ID))
				}
				// Populate only unrelated ordinary-member traffic in one bounded write;
				// a real callback below exercises the production retention path.
				_, err = h.db.Exec(`INSERT INTO qq_bot_spam_report_sources
 (app_id,sandbox,group_id,message_id,ref_idx,member_id,member_role,is_bot,eligible,content_encrypted,policy_hash,sent_at,content_hash)
 SELECT s.app_id,s.sandbox,s.group_id,'mm-filler-'||n::text,'idx-filler-'||n::text,'other-member-'||n::text,
 'member',false,s.eligible,s.content_encrypted,s.policy_hash,s.sent_at,s.content_hash
 FROM qq_bot_spam_report_sources s CROSS JOIN generate_series(1,1999) n WHERE s.message_id=$1`, original.ID)
				require.NoError(t, err)
				if arrival == "before_cap" {
					h.enqueue(t, original)
				} else {
					h.enqueue(t, protected)
				}
				assertRejected := func() {
					t.Helper()
					var count int
					require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_report_sources`).Scan(&count))
					require.Equal(t, 2000, count)
					current, reason, err := h.s.inspectSpamReportSourceForReport(context.Background(), h.db, h.c, "group", original.ID, id)
					require.NoError(t, err)
					require.Nil(t, current, "capacity pruning must not revive the protected sender's original message")
					require.Contains(t, []spamReportSourceReason{spamSourceRoleChanged, spamSourceNotFound}, reason)
					require.False(t, h.confirm(t, id, source))
					require.Empty(t, h.operations(t))
				}
				assertRejected()
				// Late ordinary duplicates of either message cannot erase the observed
				// protection or recreate usable evidence after another capacity prune.
				h.enqueue(t, later)
				h.enqueue(t, original)
				assertRejected()
				h.mu.Lock()
				requests := append([]string(nil), h.requests...)
				h.mu.Unlock()
				require.Empty(t, requests)
			})
		}
	}
}
