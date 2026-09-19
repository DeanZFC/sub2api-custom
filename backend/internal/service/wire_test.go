package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/collection"
)

type pluginProviderAccountRepository struct {
	AccountRepository
}

func (*pluginProviderAccountRepository) ListByPlatform(context.Context, string) ([]Account, error) {
	return []Account{
		{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},
		{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive},
		{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusDisabled},
	}, nil
}

func TestProvidePluginManagerOffersHostAccountDirectory(t *testing.T) {
	gateway := &OpenAIGatewayService{accountRepo: &pluginProviderAccountRepository{}}
	manager := ProvidePluginManager(nil, nil, &config.Config{}, PluginHostInfo{}, newFakePluginKVStore(), gateway)
	host := manager.buildHostServices(&PluginInstallation{
		PluginKey: "test.provider",
		Manifest: PluginManifest{Capabilities: []PluginCapability{{
			ID: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth,
		}}},
	})
	require.NotNil(t, host)
	result, err := host.ListAccounts(context.Background(), &pluginv1.ListAccountsRequest{
		Platform: PlatformOpenAI, AccountType: AccountTypeOAuth,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{7}, result.AccountIds)
	require.False(t, manager.ShouldRouteOpenAIOAuth(&Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}),
		"providing host services must not activate a plugin transport binding")
}

func TestProvideTimingWheelService_ReturnsError(t *testing.T) {
	original := newTimingWheel
	t.Cleanup(func() { newTimingWheel = original })

	newTimingWheel = func(_ time.Duration, _ int, _ collection.Execute) (*collection.TimingWheel, error) {
		return nil, errors.New("boom")
	}

	svc, err := ProvideTimingWheelService()
	if err == nil {
		t.Fatalf("期望返回 error，但得到 nil")
	}
	if svc != nil {
		t.Fatalf("期望返回 nil svc，但得到非空")
	}
}

func TestProvideTimingWheelService_Success(t *testing.T) {
	svc, err := ProvideTimingWheelService()
	if err != nil {
		t.Fatalf("期望 err 为 nil，但得到: %v", err)
	}
	if svc == nil {
		t.Fatalf("期望 svc 非空，但得到 nil")
	}
	svc.Stop()
}
