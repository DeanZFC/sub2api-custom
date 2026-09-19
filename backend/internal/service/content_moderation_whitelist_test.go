package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newModerationWhitelistTestService(t *testing.T, cfg *ContentModerationConfig) (*ContentModerationService, *contentModerationRuntimeSettingRepo) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(raw),
	}}
	// Leave workers stopped so even enqueueing an audit is observable deterministically.
	return &ContentModerationService{
		settingRepo:     settings,
		repo:            &contentModerationTestRepo{},
		hashCache:       &contentModerationTestHashCache{},
		httpClient:      http.DefaultClient,
		asyncQueue:      make(chan contentModerationTask, 4),
		runtimeCacheTTL: time.Hour,
	}, settings
}

func TestContentModerationUserWhitelistAllProtocols(t *testing.T) {
	cases := []struct {
		protocol string
		body     string
	}{
		{ContentModerationProtocolOpenAIChat, `{"messages":[{"role":"user","content":"blocked"}]}`},
		{ContentModerationProtocolOpenAIResponses, `{"input":"blocked"}`},
		{ContentModerationProtocolAnthropicMessages, `{"messages":[{"role":"user","content":"blocked"}]}`},
		{ContentModerationProtocolGemini, `{"contents":[{"role":"user","parts":[{"text":"blocked"}]}]}`},
		{ContentModerationProtocolOpenAIImages, `{"prompt":"blocked"}`},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			cfg := defaultContentModerationConfig()
			cfg.Enabled = true
			cfg.BlockedKeywords = []string{"blocked"}
			cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
			cfg.UserWhitelistIDs = []int64{7}
			svc, _ := newModerationWhitelistTestService(t, cfg)
			input := ContentModerationCheckInput{UserID: 7, Protocol: tc.protocol, Body: []byte(tc.body)}

			decision, err := svc.Check(context.Background(), input)
			require.NoError(t, err)
			require.True(t, decision.Allowed)
			require.False(t, decision.Blocked)
			require.Equal(t, ContentModerationActionAllow, decision.Action)
			require.Zero(t, svc.preBlockChecked.Load())
			require.Zero(t, svc.asyncEnqueued.Load())
			require.Empty(t, svc.asyncQueue)
			require.Empty(t, svc.repo.(*contentModerationTestRepo).snapshotLogs())

			input.UserID = 8
			decision, err = svc.Check(context.Background(), input)
			require.NoError(t, err)
			require.True(t, decision.Blocked)
			require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
			require.EqualValues(t, 1, svc.asyncEnqueued.Load())
		})
	}
}

func TestContentModerationUserWhitelistSkipsHashAPIAndObserveQueue(t *testing.T) {
	for _, mode := range []string{"hash", "api", "observe"} {
		t.Run(mode, func(t *testing.T) {
			var apiCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiCalls.Add(1)
				_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{
					CategoryScores: map[string]float64{"sexual": 1},
				}}})
			}))
			defer server.Close()
			cfg := defaultContentModerationConfig()
			cfg.Enabled = true
			cfg.UserWhitelistIDs = []int64{7}
			cfg.BaseURL = server.URL
			cfg.APIKeys = []string{"test-moderation-key"}
			cfg.PreHashCheckEnabled = mode == "hash"
			cfg.RecordNonHits = true
			if mode == "observe" {
				cfg.Mode = ContentModerationModeObserve
			}
			svc, _ := newModerationWhitelistTestService(t, cfg)
			cache := &contentModerationTestHashCache{hasResultUsed: true, hasResult: true}
			svc.hashCache = cache
			input := runtimeCacheTestInput("audit this prompt")
			input.UserID = 7

			decision, err := svc.Check(context.Background(), input)
			require.NoError(t, err)
			require.True(t, decision.Allowed)
			require.False(t, decision.Flagged)
			require.Zero(t, apiCalls.Load())
			require.Empty(t, cache.snapshotChecked())
			require.Empty(t, cache.snapshotRecorded())
			require.Empty(t, svc.repo.(*contentModerationTestRepo).snapshotLogs())
			require.Empty(t, svc.asyncQueue)
			require.Zero(t, svc.asyncEnqueued.Load())
			require.Zero(t, svc.preBlockChecked.Load())

			input.UserID = 8
			decision, err = svc.Check(context.Background(), input)
			require.NoError(t, err)
			switch mode {
			case "hash":
				require.True(t, decision.Blocked)
				require.Equal(t, ContentModerationActionHashBlock, decision.Action)
				require.Len(t, cache.snapshotChecked(), 1)
				require.Zero(t, apiCalls.Load())
			case "api":
				require.True(t, decision.Blocked)
				require.Equal(t, ContentModerationActionBlock, decision.Action)
				require.EqualValues(t, 1, apiCalls.Load())
			case "observe":
				require.True(t, decision.Allowed)
				require.Zero(t, apiCalls.Load(), "observe mode should enqueue instead of synchronously calling the API")
			}
			require.EqualValues(t, 1, svc.asyncEnqueued.Load())
			require.Len(t, svc.asyncQueue, 1)
		})
	}
}

func TestContentModerationUserWhitelistUpdateRefreshesRuntimeAndPreservesOmitted(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
	cfg.BlockedKeywords = []string{"blocked"}
	svc, settings := newModerationWhitelistTestService(t, cfg)
	input := runtimeCacheTestInput("blocked")
	input.UserID = 7
	decision, err := svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked)

	ids := []int64{9, 7, -3, 0, 7}
	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{UserWhitelistIDs: &ids})
	require.NoError(t, err)
	require.Equal(t, []int64{7, 9}, view.UserWhitelistIDs)
	stored, err := settings.GetValue(context.Background(), SettingKeyContentModerationConfig)
	require.NoError(t, err)
	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(stored), &saved))
	require.Equal(t, []int64{7, 9}, saved.UserWhitelistIDs)
	decision, err = svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Allowed, "saving the whitelist must replace the cached runtime configuration immediately")

	// Neither callers changing the input nor UI view mutations may change live policy.
	ids[1] = 8
	view.UserWhitelistIDs[0] = 8
	decision, err = svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	message := "custom moderation message"
	view, err = svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{BlockMessage: &message})
	require.NoError(t, err)
	require.Equal(t, []int64{7, 9}, view.UserWhitelistIDs, "an unrelated settings update must retain the whitelist")

	ids = []int64{}
	view, err = svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{UserWhitelistIDs: &ids})
	require.NoError(t, err)
	require.Equal(t, []int64{}, view.UserWhitelistIDs)
	decision, err = svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked, "removing the whitelist must take effect before runtime cache expiry")
	require.Equal(t, message, decision.Message)
	_, loads := settings.calls()
	require.Equal(t, 1, loads, "updates should replace the live snapshot without waiting for or forcing a settings reload")
}

func TestContentModerationUserWhitelistLegacyConfig(t *testing.T) {
	settings := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: `{"enabled":true,"mode":"pre_block","blocked_keywords":["blocked"],"keyword_blocking_mode":"keyword_only"}`,
	}}
	svc := &ContentModerationService{settingRepo: settings, repo: &contentModerationTestRepo{}}
	view, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{}, view.UserWhitelistIDs)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &response))
	require.JSONEq(t, `[]`, string(response["user_whitelist_ids"]))
	input := runtimeCacheTestInput("blocked")
	input.UserID = 7
	decision, err := svc.Check(context.Background(), input)
	require.NoError(t, err)
	require.True(t, decision.Blocked, "older configurations must retain auditing for existing users")
}

func TestContentModerationUserWhitelistCyberPolicySkipsSideEffects(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.UserWhitelistIDs = []int64{7}
	cfg.BanThreshold = 1
	svc, _ := newModerationWhitelistTestService(t, cfg)
	repo := &banCountArgsTestRepo{}
	users := &contentModerationTestUserRepo{user: &User{ID: 7, Role: RoleUser, Status: StatusActive}}
	emailSettings := &contentModerationRuntimeSettingRepo{}
	svc.repo = repo
	svc.userRepo = users
	svc.emailService = &EmailService{settingRepo: emailSettings}
	input := CyberPolicyRecordInput{UserID: 7, UserEmail: "user@example.test", Model: "gpt-5", UpstreamMessage: "cyber_policy"}

	svc.RecordCyberPolicyEvent(context.Background(), input)
	require.Empty(t, repo.snapshotLogs())
	require.Empty(t, repo.snapshotCountCalls())
	require.Empty(t, users.updated)
	require.Equal(t, StatusActive, users.user.Status)
	_, emailCalls := emailSettings.calls()
	require.Zero(t, emailCalls, "whitelisted users must not trigger a notification attempt")

	input.UserID = 8
	users.user.ID = 8
	svc.RecordCyberPolicyEvent(context.Background(), input)
	require.Len(t, repo.snapshotLogs(), 1)
	require.Len(t, repo.snapshotCountCalls(), 1)
	require.Len(t, users.updated, 1)
	require.Equal(t, StatusDisabled, users.user.Status)
	_, emailCalls = emailSettings.calls()
	require.Positive(t, emailCalls, "non-whitelisted cyber events must retain existing notification behavior")
}

func TestContentModerationUserWhitelistSkipsAlreadyQueuedAudits(t *testing.T) {
	var apiCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{
			CategoryScores: map[string]float64{"sexual": 1},
		}}})
	}))
	defer server.Close()
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"test-moderation-key"}
	svc, _ := newModerationWhitelistTestService(t, cfg)
	input := runtimeCacheTestInput("queued content")
	input.UserID = 7
	content := ContentModerationInput{Text: "queued content"}
	svc.enqueueAsync(input, cfg, content, content.Hash())
	log := svc.buildLog(input, cfg, ContentModerationActionBlock, true, "sexual", 1, nil, content.Text, nil, nil, "")
	svc.enqueueRecord(input, cfg, log, content.Hash(), true, true)
	require.Len(t, svc.asyncQueue, 2)

	ids := []int64{7}
	_, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{UserWhitelistIDs: &ids})
	require.NoError(t, err)
	// A following non-whitelisted audit acts as a completion marker for both older tasks.
	input.UserID = 8
	svc.enqueueAsync(input, cfg, content, content.Hash())
	go svc.worker(0)
	repo := svc.repo.(*contentModerationTestRepo)
	require.Eventually(t, func() bool {
		return svc.asyncProcessed.Load() >= 1 && len(svc.asyncQueue) == 0
	}, time.Second, time.Millisecond)
	logs := repo.snapshotLogs()
	require.Len(t, logs, 1)
	require.NotNil(t, logs[0].UserID)
	require.Equal(t, int64(8), *logs[0].UserID)
	require.EqualValues(t, 1, apiCalls.Load())
	require.Len(t, svc.hashCache.(*contentModerationTestHashCache).snapshotRecorded(), 1)
}
