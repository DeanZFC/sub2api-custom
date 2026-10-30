package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestMainlandWebBlockSettingsPersistenceAndImmediateCache(t *testing.T) {
	ctx := context.Background()
	repo := &upstreamRetrySettingsRepo{values: map[string]string{}}
	cfg := &config.Config{}
	cfg.Security.MainlandWebBlock.Enabled = true
	svc := NewSettingService(repo, cfg)
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx), "an absent DB value inherits the deployment default")
	var concurrent sync.WaitGroup
	for range 20 {
		concurrent.Add(1)
		go func() { defer concurrent.Done(); svc.IsMainlandWebBlockEnabled(ctx) }()
	}
	concurrent.Wait()
	require.Equal(t, 1, repo.reads, "hot-path reads share the cache")

	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{MainlandWebBlockEnabled: false}))
	require.False(t, svc.IsMainlandWebBlockEnabled(ctx), "saved false replaces a warm true cache immediately")
	require.Equal(t, "false", repo.values[SettingKeyMainlandWebBlockEnabled])
	stored, err := svc.GetAllSettings(ctx)
	require.NoError(t, err)
	require.False(t, stored.MainlandWebBlockEnabled)
	require.False(t, NewSettingService(repo, cfg).IsMainlandWebBlockEnabled(ctx), "saved false survives restart even with environment true")

	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{MainlandWebBlockEnabled: true}))
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx), "enable applies without restart")
	require.Equal(t, "true", repo.values[SettingKeyMainlandWebBlockEnabled])
	require.NoError(t, svc.UpdateSettingsOmitting(ctx, &SystemSettings{}, OmittedSettingKeys{SettingKeyMainlandWebBlockEnabled: {}}))
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx), "an omitted field cannot clear the switch")
	require.Equal(t, "true", repo.values[SettingKeyMainlandWebBlockEnabled])

	repo.mu.Lock()
	repo.values[SettingKeyMainlandWebBlockEnabled] = "false"
	repo.mu.Unlock()
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx), "other instances reuse the warm cache until expiry")
	svc.mainlandWebBlockCache.Store(&cachedMainlandWebBlock{enabled: true, expiresAt: time.Now().Add(-time.Second)})
	require.False(t, svc.IsMainlandWebBlockEnabled(ctx), "expiry refreshes another instance's save")
}

type mainlandSettingsFailureRepo struct {
	*upstreamRetrySettingsRepo
	readErr   error
	writeErr  error
	getAllErr error
}

func (r *mainlandSettingsFailureRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.readErr != nil {
		return "", r.readErr
	}
	return r.upstreamRetrySettingsRepo.GetValue(ctx, key)
}

func (r *mainlandSettingsFailureRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	return r.upstreamRetrySettingsRepo.SetMultiple(ctx, values)
}

func (r *mainlandSettingsFailureRepo) GetAll(ctx context.Context) (map[string]string, error) {
	if r.getAllErr != nil {
		return nil, r.getAllErr
	}
	return r.upstreamRetrySettingsRepo.GetAll(ctx)
}

func TestMainlandWebBlockReadAndWriteFailuresPreserveLastValue(t *testing.T) {
	ctx := context.Background()
	repo := &mainlandSettingsFailureRepo{upstreamRetrySettingsRepo: &upstreamRetrySettingsRepo{values: map[string]string{SettingKeyMainlandWebBlockEnabled: "true"}}}
	svc := NewSettingService(repo, &config.Config{})
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx))
	repo.readErr = errors.New("database unavailable")
	svc.mainlandWebBlockCache.Store(&cachedMainlandWebBlock{enabled: true, expiresAt: time.Now().Add(-time.Second)})
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx), "read failure retains enabled cache")
	repo.writeErr = errors.New("write unavailable")
	require.Error(t, svc.UpdateSettings(ctx, &SystemSettings{MainlandWebBlockEnabled: false}))
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx), "write failure does not publish false")

	cfg := &config.Config{}
	cfg.Security.MainlandWebBlock.Enabled = true
	require.True(t, NewSettingService(repo, cfg).IsMainlandWebBlockEnabled(ctx), "cold read failure falls back to deployment config")

	repo.writeErr = nil
	repo.getAllErr = errors.New("post-write read failed")
	require.NoError(t, svc.UpdateSettingsOmitting(ctx, &SystemSettings{}, OmittedSettingKeys{SettingKeyRegistrationEnabled: {}}))
	require.False(t, svc.IsMainlandWebBlockEnabled(ctx), "successful save publishes even if the later cache refresh fails")
}

func TestMainlandWebBlockValidationDoesNotPublishOrPersistInvalidData(t *testing.T) {
	ctx := context.Background()
	repo := &upstreamRetrySettingsRepo{values: map[string]string{}}
	cfg := &config.Config{}
	cfg.Security.MainlandWebBlock.CIDRFile = filepath.Join(t.TempDir(), "cn.zone")
	svc := NewSettingService(repo, cfg)
	require.ErrorContains(t, svc.UpdateSettings(ctx, &SystemSettings{MainlandWebBlockEnabled: true}), "Cannot enable mainland web access restriction")
	require.NotContains(t, repo.values, SettingKeyMainlandWebBlockEnabled)
	require.False(t, svc.IsMainlandWebBlockEnabled(ctx))
	require.NoError(t, os.WriteFile(cfg.Security.MainlandWebBlock.CIDRFile, []byte("223.5.5.0/24\n2400:3200::/32\n"), 0600))
	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{MainlandWebBlockEnabled: true}))
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx))
	require.NoError(t, os.Remove(cfg.Security.MainlandWebBlock.CIDRFile))
	require.NoError(t, svc.UpdateSettingsOmitting(ctx, &SystemSettings{MainlandWebBlockEnabled: true}, OmittedSettingKeys{SettingKeyMainlandWebBlockEnabled: {}}), "an omitted switch does not revalidate data for unrelated settings saves")
	require.True(t, svc.IsMainlandWebBlockEnabled(ctx))
	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{MainlandWebBlockEnabled: false}), "disable is possible even after the data file disappears")
	require.False(t, svc.IsMainlandWebBlockEnabled(ctx))
}

func TestMainlandWebBlockDefaultSettingsRespectDeploymentConfig(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := &config.Config{}
		cfg.Security.MainlandWebBlock.Enabled = enabled
		repo := &upstreamRetrySettingsRepo{values: map[string]string{}}
		svc := NewSettingService(repo, cfg)
		require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
		require.Equal(t, enabled, svc.parseSettings(repo.values).MainlandWebBlockEnabled)
	}
}
