package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetAccessToken_SparkShadowResolvesToParent 验证对影子账号调用 GetAccessToken
// 时能透明地解析到母账号的凭据，防止 refresh_token 脱钩。
// 影子账号不持凭据；断言必须返回母账号的 access_token。
func TestGetAccessToken_SparkShadowResolvesToParent(t *testing.T) {
	ctx := context.Background()

	parentID := int64(100)
	parent := Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token": "parent-access-token",
		},
	}
	shadow := Account{
		ID:              200,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		// 影子账号不持凭据，与生产语义一致
	}

	repo := &stubOpenAIAccountRepo{accounts: []Account{parent}}

	svc := &OpenAIGatewayService{
		accountRepo: repo,
		// openAITokenProvider=nil → 走降级路径，直接读 account.GetOpenAIAccessToken()
	}

	// Before fix (RED): shadow 无凭据 → GetOpenAIAccessToken()="" → error
	// After fix (GREEN): shadow 解析到 parent → 返回 parent 的 "parent-access-token"
	token, tokenType, err := svc.GetAccessToken(ctx, &shadow)
	require.NoError(t, err)
	require.Equal(t, "parent-access-token", token)
	require.Equal(t, "oauth", tokenType)
}

func TestGetAccessToken_SparkShadowVMBindingPreservesCredentialBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		clientID string
		wantErr  bool
	}{
		{name: "native Codex VM shadow"},
		{name: "Excel primary VM shadow", clientID: "app_fnr0pYvVwwFDocDumLG3H2Bp", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parentID := int64(100)
			parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "parent-access-token", "client_id": tc.clientID}}
			shadow := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID}
			repo := newStubCredRepo(parent)
			vmRepo := &vmTestRepo{b: &OpenAIVMBinding{AccountID: parentID, Status: "ready"}}
			svc := &OpenAIGatewayService{accountRepo: repo,
				openaiVM: NewOpenAIVMService(vmRepo, repo, nil, vmTestEncryptor{})}
			token, kind, err := svc.GetAccessToken(t.Context(), shadow)
			if tc.wantErr {
				require.ErrorContains(t, err, "native Codex credentials are required")
				require.Empty(t, token)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "parent-access-token", token)
			require.Equal(t, "oauth", kind)
		})
	}
}
