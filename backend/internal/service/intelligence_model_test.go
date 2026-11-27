package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceModelEligibilityOnWire(t *testing.T) {
	const image = "gpt-image-2"
	const text = "text-alias"
	for _, tc := range []struct {
		name, requested, mapped, wantWire string
		mappingKey                        string
		passthrough, filter, chatOnly     bool
		noText                            bool
	}{
		{name: "explicit text whitelist", requested: text, mappingKey: text, mapped: text, wantWire: text},
		{name: "image-only whitelist", requested: text, mappingKey: image, mapped: image},
		{name: "direct image model", requested: image},
		{name: "public image name mapped to text", requested: image, mappingKey: image, mapped: text},
		{name: "qualified image target", requested: text, mappingKey: text, mapped: "openai/gpt-image-2"},
		{name: "text alias to native image", requested: text, mappingKey: text, mapped: image},
		{name: "text alias to compatible image", requested: text, mappingKey: text, mapped: "gemini-2.5-flash-image"},
		{name: "text disabled", requested: text, noText: true},
		{name: "native passthrough ignores stale mapping target", requested: text, mappingKey: text, mapped: image, passthrough: true, wantWire: text},
		{name: "passthrough source filter rejects text", requested: text, mappingKey: image, mapped: image, passthrough: true, filter: true},
		{name: "raw chat fallback applies mapping before passthrough", requested: text, mappingKey: text, mapped: image, passthrough: true, chatOnly: true},
		{name: "raw chat can run text checks", requested: text, mappingKey: text, mapped: "mapped-text", chatOnly: true, wantWire: "mapped-text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credentials := map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}
			if tc.mappingKey != "" {
				credentials["model_mapping"] = map[string]any{tc.mappingKey: tc.mapped}
			}
			if tc.noText {
				credentials["openai_capabilities"] = []string{"embeddings"}
			}
			account := &Account{
				ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Credentials: credentials, Extra: map[string]any{
					"openai_passthrough": tc.passthrough, "openai_passthrough_model_filter": tc.filter,
					openai_compat.ExtraKeyResponsesSupported: !tc.chatOnly,
				},
			}
			require.Equal(t, tc.wantWire != "", account.IntelligenceMonitoringEligible(time.Now()) && account.SupportsIntelligenceModel(tc.requested))
			plan := &ScheduledTestPlan{Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{}}}
			ctx := context.WithValue(context.Background(), scheduledTestPlanContextKey{}, plan)
			up := &modelIdentityHTTPUpstream{body: `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`}
			if tc.chatOnly {
				up.body = `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`
			}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: up, cfg: &config.Config{}}
			result, err := svc.RunTestBackgroundWithPrompt(ctx, account.ID, tc.requested, "Reply OK")
			require.NoError(t, err)
			if tc.wantWire == "" {
				require.Equal(t, "failed", result.Status)
				require.Zero(t, up.requests)
				return
			}
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, 1, up.requests)
			require.Equal(t, tc.wantWire, up.requestModel)
		})
	}
}
