package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestUpstreamErrorMessageSettingsPersistenceAndCache(t *testing.T) {
	ctx := context.Background()
	repo := &upstreamRetrySettingsRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	require.Empty(t, svc.GetUpstreamErrorMessage(ctx))
	var concurrent sync.WaitGroup
	for range 20 {
		concurrent.Add(1)
		go func() { defer concurrent.Done(); svc.GetUpstreamErrorMessage(ctx) }()
	}
	concurrent.Wait()
	require.Equal(t, 1, repo.reads, "cached reads must not query DB for stream events")

	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{UpstreamErrorMessage: "  服务繁忙，请稍后重试。 \n"}))
	require.Equal(t, "服务繁忙，请稍后重试。", svc.GetUpstreamErrorMessage(ctx), "save replaces a warm disabled cache immediately")
	require.Equal(t, "服务繁忙，请稍后重试。", repo.values[SettingKeyUpstreamErrorMessage])
	stored, err := svc.GetAllSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "服务繁忙，请稍后重试。", stored.UpstreamErrorMessage)
	require.Equal(t, stored.UpstreamErrorMessage, NewSettingService(repo, &config.Config{}).GetUpstreamErrorMessage(ctx))

	require.NoError(t, svc.UpdateSettingsOmitting(ctx, &SystemSettings{}, OmittedSettingKeys{SettingKeyUpstreamErrorMessage: {}}))
	require.Equal(t, stored.UpstreamErrorMessage, svc.GetUpstreamErrorMessage(ctx), "an omitted field cannot clear the override cache")
	require.NoError(t, svc.UpdateSettings(ctx, &SystemSettings{UpstreamErrorMessage: "\n\t "}))
	require.Empty(t, svc.GetUpstreamErrorMessage(ctx))
	require.Empty(t, repo.values[SettingKeyUpstreamErrorMessage])
}

func TestUpstreamErrorMessageValidationCountsCharacters(t *testing.T) {
	repo := &upstreamRetrySettingsRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	accepted := strings.Repeat("错", 500)
	require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{UpstreamErrorMessage: " " + accepted + " "}))
	require.Equal(t, accepted, svc.GetUpstreamErrorMessage(context.Background()))
	require.Error(t, svc.UpdateSettings(context.Background(), &SystemSettings{UpstreamErrorMessage: accepted + "误"}))
	require.Equal(t, accepted, repo.values[SettingKeyUpstreamErrorMessage])
	require.Equal(t, accepted, svc.GetUpstreamErrorMessage(context.Background()), "failed validation must not replace cache")
}

type upstreamMessageFailureRepo struct {
	*upstreamRetrySettingsRepo
	readErr  error
	writeErr error
}

func (r *upstreamMessageFailureRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.readErr != nil {
		return "", r.readErr
	}
	return r.upstreamRetrySettingsRepo.GetValue(ctx, key)
}

func (r *upstreamMessageFailureRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	return r.upstreamRetrySettingsRepo.SetMultiple(ctx, values)
}

func TestUpstreamErrorMessageTransientFailureKeepsExistingOverride(t *testing.T) {
	repo := &upstreamMessageFailureRepo{upstreamRetrySettingsRepo: &upstreamRetrySettingsRepo{values: map[string]string{SettingKeyUpstreamErrorMessage: "请稍后重试"}}}
	svc := NewSettingService(repo, &config.Config{})
	require.Equal(t, "请稍后重试", svc.GetUpstreamErrorMessage(context.Background()))
	repo.readErr = errors.New("database unavailable")
	svc.upstreamErrorMessageCache.Store(&cachedUpstreamErrorMessage{message: "请稍后重试", expiresAt: time.Now().Add(-time.Second)})
	require.Equal(t, "请稍后重试", svc.GetUpstreamErrorMessage(context.Background()))
	repo.writeErr = errors.New("write unavailable")
	require.Error(t, svc.UpdateSettings(context.Background(), &SystemSettings{UpstreamErrorMessage: "new message"}))
	require.Equal(t, "请稍后重试", svc.GetUpstreamErrorMessage(context.Background()))
}
