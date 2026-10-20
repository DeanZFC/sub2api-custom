package qqbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func priceTestSnapshot(value string) PriceSnapshot {
	return PriceSnapshot{States: []PriceState{{Key: "channel:1", Kind: "channel", Name: "示例渠道", Fields: []PriceField{
		{Key: "model/input", Model: "示例模型", Label: "输入", Value: value, Unit: "百万 Token"},
		{Key: "model/time", Model: "示例模型", Label: "当前分时倍率", Value: "1×", Scheduled: true},
	}}}}
}

func TestPriceNotificationDiff(t *testing.T) {
	at := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	before, after := priceTestSnapshot("USD 2"), priceTestSnapshot("USD 1.5")
	chunks := formatPriceChanges(before, after, true, at)
	require.Len(t, chunks, 1)
	require.Contains(t, chunks[0], "渠道：示例渠道")
	require.Contains(t, chunks[0], "模型：示例模型")
	require.Contains(t, chunks[0], "输入：USD 2 → USD 1.5 / 百万 Token")
	require.Contains(t, chunks[0], "2026-10-20 09:00:00（北京时间）")
	require.NotContains(t, chunks[0], "当前分时倍率")
	require.Empty(t, formatPriceChanges(before, before, true, at))
	after = priceTestSnapshot("USD 2")
	after.States[0].Name = "只改名称"
	require.Empty(t, formatPriceChanges(before, after, true, at))
	after.States[0].Fields[1].Value = "0.5×"
	require.Empty(t, formatPriceChanges(before, after, false, at))
	require.Contains(t, formatPriceChanges(before, after, true, at)[0], "定时切换")
	require.Contains(t, formatPriceChanges(before, after, true, at)[0], "1× → 0.5×")
	require.Contains(t, formatPriceChanges(after, before, true, at)[0], "0.5× → 1×")
	require.Empty(t, formatPriceChanges(PriceSnapshot{}, after, true, at))
}

func TestPriceNotificationModelRemovalAndBoundedChunks(t *testing.T) {
	before, after := priceTestSnapshot("USD 2"), priceTestSnapshot("USD 1.5")
	before.States[0].Name = "<qqbot-at-everyone /> **渠道**"
	after.States[0].Name = before.States[0].Name
	for i := 0; i < 50; i++ {
		key := strings.Repeat("模", i+1)
		before.States[0].Fields = append(before.States[0].Fields, PriceField{Key: key, Model: key, Label: "输出", Value: "USD 8"})
		after.States[0].Fields = append(after.States[0].Fields, PriceField{Key: key, Model: key, Label: "输出", Value: "USD 4"})
	}
	chunks := formatPriceChanges(before, after, true, time.Now())
	require.Greater(t, len(chunks), 1)
	for _, chunk := range chunks {
		require.LessOrEqual(t, len([]rune(chunk)), 1800)
		require.NotContains(t, chunk, "<qqbot-at-everyone />")
	}
	require.Equal(t, 50, strings.Count(strings.Join(chunks, ""), "USD 8 → USD 4"))
	before.States[0].Name = strings.Repeat("*", 1000)
	after.States[0].Name = before.States[0].Name
	after.States[0].Fields[0].Model = strings.Repeat("*", 1000)
	after.States[0].Fields[0].Label = strings.Repeat("*", 1000)
	for _, chunk := range formatPriceChanges(before, after, true, time.Now()) {
		require.LessOrEqual(t, len([]rune(chunk)), 1800)
	}
	after.States[0].Fields = after.States[0].Fields[1:]
	require.Contains(t, strings.Join(formatPriceChanges(before, after, true, time.Now()), ""), "USD 2 → 已移除")
}

func TestPriceNotificationConfigAndProactiveDelivery(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.AppID = "app"
	c.AppSecret = "test"
	c.AllowedGroups = []string{"group", "other"}
	c.PriceNotifications = PriceNotificationConfig{Enabled: true, Scheduled: true, Groups: []string{"group", "group"}}
	require.NoError(t, validatePriceNotifications(c))
	require.Equal(t, []string{"group"}, priceNotificationGroups(c))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":"7200"}`))
			return
		}
		requests++
		require.Equal(t, "/v2/groups/group/messages", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.EqualValues(t, 2, body["msg_type"])
		require.Equal(t, "通知正文", body["markdown"].(map[string]any)["content"])
		for _, key := range []string{"msg_id", "event_id", "msg_seq", "content"} {
			require.NotContains(t, body, key)
		}
		_, _ = w.Write([]byte(`{"id":"notice"}`))
	}))
	defer server.Close()
	s := New(nil, nil, false, nil)
	s.qqURL = server.URL
	s.tokenURL = server.URL + "/token"
	j := Job{AppID: "app", GroupID: "group", EventType: priceNotificationEvent}
	require.NoError(t, s.sendPriceNotification(context.Background(), c, j, "通知正文"))
	j.GroupID = "other"
	require.ErrorIs(t, s.sendPriceNotification(context.Background(), c, j, "通知正文"), ErrDisabled)
	j.GroupID = "group"
	j.AppID = "another-app"
	require.ErrorIs(t, s.sendPriceNotification(context.Background(), c, j, "通知正文"), ErrDisabled)
	require.Equal(t, 1, requests)
	c.PriceNotifications.Groups = []string{"forbidden"}
	require.Error(t, validatePriceNotifications(c))
}
