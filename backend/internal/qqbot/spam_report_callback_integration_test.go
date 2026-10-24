//go:build integration

package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Use the official callback field layout, including the @ event's stripped
// prefix, rather than round-tripping Event into the same schema under test.
func spamReportCallbackFixture(t *testing.T, eventType, body, role string, bot bool, sent time.Time) Payload {
	t.Helper()
	content, err := json.Marshal(body)
	require.NoError(t, err)
	raw := fmt.Sprintf(`{"op":0,"t":%q,"d":{"id":"callback-original","author":{"id":"target","member_openid":"target","member_role":%q,"username":"群成员","bot":%t},"content":%s,"group_openid":"group","message_type":0,"timestamp":%q,"message_scene":{"source":"default","ext":["msg_idx=REFIDX_callback==","auth_token=must-not-be-retained"]}}}`,
		eventType, role, bot, content, sent.Format(time.RFC3339Nano))
	var p Payload
	require.NoError(t, json.Unmarshal([]byte(raw), &p))
	return p
}

func TestSpamReportCallbacksRequireFullSourceAndRetainCompleteTextInEitherOrder(t *testing.T) {
	for _, atFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("at_first=%t", atFirst), func(t *testing.T) {
			h := newSpamSourceHarness(t)
			sent := time.Now().UTC().Truncate(time.Microsecond)
			fullText := "<@BOT123> 低价代充会员限时优惠，全网最低价格，立即下单请私聊我。"
			atText := " 低价代充会员限时优惠，全网最低价格，立即下单请私聊我。 "
			full := spamReportCallbackFixture(t, groupMessageCreate, fullText, "member", false, sent)
			at := spamReportCallbackFixture(t, groupAtMessageCreate, atText, "member", false, sent)
			first, second := full, at
			if atFirst {
				first, second = at, full
			}
			require.NoError(t, h.s.Enqueue(context.Background(), h.c, first))
			if atFirst {
				require.Nil(t, h.source(t, "REFIDX_callback=="))
				var encrypted string
				require.NoError(t, h.db.QueryRow(`SELECT content_encrypted FROM qq_bot_spam_report_sources`).Scan(&encrypted))
				require.Empty(t, encrypted, "an @ callback alone cannot establish complete original text")
			}
			require.NoError(t, h.s.Enqueue(context.Background(), h.c, second))
			for _, p := range []Payload{full, at, full} {
				require.NoError(t, h.s.Enqueue(context.Background(), h.c, p))
				source := h.source(t, "REFIDX_callback==")
				require.NotNil(t, source)
				require.Equal(t, "target", source.MemberID)
				require.Equal(t, fullText, source.Content)
				require.Equal(t, sent, source.SentAt.UTC())
			}
		})
	}
}

func TestSpamReportCallbackConflictsAndProtectiveRolesStayRejected(t *testing.T) {
	for _, mode := range []string{"full_body", "at_body", "at_whitespace_after_full", "cross_body", "unknown_role", "admin", "bot", "index", "sender", "legacy_hash"} {
		for _, atFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/at_first=%t", mode, atFirst), func(t *testing.T) {
				h := newSpamSourceHarness(t)
				sent := time.Now().UTC().Truncate(time.Microsecond)
				full := spamReportCallbackFixture(t, groupMessageCreate, "<@BOT123> 低价代充请私聊。", "member", false, sent)
				at := spamReportCallbackFixture(t, groupAtMessageCreate, " 低价代充请私聊。 ", "member", false, sent)
				first, second := full, at
				if atFirst {
					first, second = at, full
				}
				require.NoError(t, h.s.Enqueue(context.Background(), h.c, first))
				if mode == "legacy_hash" {
					_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET content_hash='legacy-unmarked-hash'`)
					require.NoError(t, err)
				} else {
					mutated := second
					switch mode {
					case "full_body":
						mutated = spamReportCallbackFixture(t, groupMessageCreate, "<@BOT123> 正常消息", "member", false, sent)
					case "at_body", "cross_body":
						mutated = spamReportCallbackFixture(t, groupAtMessageCreate, "正常消息", "member", false, sent)
					case "at_whitespace_after_full":
						require.NoError(t, h.s.Enqueue(context.Background(), h.c, second))
						mutated = spamReportCallbackFixture(t, groupAtMessageCreate, "低价代充请私聊。", "member", false, sent)
					case "unknown_role", "admin", "bot":
						role := "member"
						if mode == "unknown_role" {
							role = ""
						} else if mode == "admin" {
							role = "admin"
						}
						mutated = spamReportCallbackFixture(t, groupAtMessageCreate, " 低价代充请私聊。 ", role, mode == "bot", sent)
					case "index", "sender":
						var e Event
						require.NoError(t, json.Unmarshal(second.Data, &e))
						if mode == "index" {
							e.MessageScene.Ext = []string{"msg_idx=conflicting-index"}
						} else {
							e.Author.MemberID = "another-member"
						}
						var err error
						mutated.Data, err = json.Marshal(e)
						require.NoError(t, err)
					}
					require.NoError(t, h.s.Enqueue(context.Background(), h.c, mutated))
				}
				for _, p := range []Payload{second, full, at} {
					require.NoError(t, h.s.Enqueue(context.Background(), h.c, p))
					require.Nil(t, h.source(t, "REFIDX_callback=="), "later ordinary callbacks cannot revive a conflict")
				}
				require.Empty(t, h.operations(t))
			})
		}
	}
}
