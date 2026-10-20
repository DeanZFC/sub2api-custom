package qqbot

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestAutonomousConfigDefaultsAndValidation(t *testing.T) {
	var c Config
	require.NoError(t, json.Unmarshal([]byte(`{"autonomous":{"sample_percent":0,"daily_calls":0}}`), &c))
	require.Equal(t, 120, c.Autonomous.CooldownSeconds)
	require.Zero(t, c.Autonomous.SamplePercent)
	require.Zero(t, c.Autonomous.DailyCalls)
	c = DefaultConfig()
	require.False(t, c.Autonomous.Enabled)
	require.NoError(t, validateAutonomous(c))
	c.Autonomous.Enabled = true
	require.Error(t, validateAutonomous(c))
	c.Autonomous.Groups = []string{"group"}
	require.Error(t, validateAutonomous(c))
	c.AllowedGroups = []string{"group"}
	require.NoError(t, validateAutonomous(c))
	c.Autonomous.SamplePercent = 101
	require.Error(t, validateAutonomous(c))
	c = DefaultConfig()
	c.Autonomous.Names = []string{"@all"}
	require.Error(t, validateAutonomous(c))
}
func TestAutonomousNamesAndLocalFilters(t *testing.T) {
	a := DefaultAutonomousConfig()
	for _, text := range []string{"CTAV，帮我看看这个问题", "ctav 请问怎么安装", "CTAV你能帮忙吗"} {
		require.True(t, directBotName(a, text), text)
	}
	for _, text := range []string{"今天CTAV不错", "CTAVX 帮我", "CTAV的功能怎么样", "CTAV"} {
		require.False(t, directBotName(a, text), text)
	}
	for _, text := range []string{"哈哈哈哈", "嗯", "/签到", "有人知道 https://example.com 吗", "@张三 部署好了吗", "sk-secret-key", "这是验证码 12345"} {
		_, reason := autonomousText(Event{Content: text})
		require.NotEmpty(t, reason, text)
	}
	text, reason := autonomousText(Event{Content: "今天这个错误怎么处理？"})
	require.Empty(t, reason)
	require.NotEmpty(t, text)
	e := Event{Content: "这个问题怎么处理？"}
	e.MessageReference.MessageID = "another-user"
	_, reason = autonomousText(e)
	require.NotEmpty(t, reason)
	c := DefaultConfig()
	c.Autonomous.SamplePercent = 0
	require.False(t, sampledMessage(c, Event{ID: "a"}))
	c.Autonomous.SamplePercent = 100
	require.True(t, sampledMessage(c, Event{ID: "a"}))
}
func TestAutonomousActiveBeijingWindow(t *testing.T) {
	a := DefaultAutonomousConfig()
	at := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	require.True(t, autonomousActive(a, at))
	require.False(t, autonomousActive(a, at.Add(-time.Second)))
	require.False(t, autonomousActive(a, time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)))
	a.StartHour, a.EndHour = 23, 9
	require.True(t, autonomousActive(a, at.Add(-time.Second)))
	require.False(t, autonomousActive(a, at))
	a.StartHour = a.EndHour
	require.True(t, autonomousActive(a, at))
}
func TestAutonomousStrictOutput(t *testing.T) {
	text, err := parseAutonomousReply(`{"action":"reply","text":"可以先看看错误日志。"}`, 100)
	require.NoError(t, err)
	require.Equal(t, "可以先看看错误日志。", text)
	text, err = parseAutonomousReply(`{"action":"skip"}`, 100)
	require.NoError(t, err)
	require.Empty(t, text)
	for _, raw := range []string{`{"action":"reply","text":"@大家"}`, `{"action":"reply","text":"<qqbot-at-everyone />"}`, `{"action":"reply","text":""}`, `{"action":"reply","text":"answer","other":true}`, `{"action":"skip"} {}`, "普通回复", `{"action":"reply","text":"` + strings.Repeat("字", 101) + `"}`} {
		_, err = parseAutonomousReply(raw, 100)
		require.Error(t, err, raw)
	}
}
