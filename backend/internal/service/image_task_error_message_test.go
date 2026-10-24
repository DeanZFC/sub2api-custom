package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageTaskPollingOverridesOnlyProviderErrorCopy(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: 7, APIKeyID: 9}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	raw := json.RawMessage(`{"type":"invalid_request_error","message":"private provider detail","debug":"private provider stack"}`)
	require.NoError(t, svc.FailWithSource(context.Background(), created.ID, http.StatusBadRequest, raw, true))
	ctx := WithUpstreamErrorMessage(context.Background(), "统一提示")
	got, err := svc.Get(ctx, owner, created.ID)
	require.NoError(t, err)
	require.Contains(t, string(got.Error), "统一提示")
	require.NotContains(t, string(got.Error), "private")
	require.JSONEq(t, string(raw), string(store.task.Error))
	plain, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(plain.Error))
	public, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(public), "error_source")

	local := json.RawMessage(`{"type":"billing_error","message":"Insufficient balance"}`)
	require.NoError(t, svc.FailWithSource(context.Background(), created.ID, http.StatusPaymentRequired, local, false))
	got, err = svc.Get(ctx, owner, created.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(local), string(got.Error))
}

func TestImageTaskLegacyProviderErrorUsesCurrentOverride(t *testing.T) {
	ctx := WithUpstreamErrorMessage(context.Background(), "统一提示")
	task := &ImageTaskRecord{Status: ImageTaskStatusFailed, Error: json.RawMessage(`{"message":"old private failure"}`)}
	require.Contains(t, string(imageTaskToPublicForContext(ctx, task).Error), "统一提示")
	task.Error = json.RawMessage(`{"type":"subscription_error","message":"Subscription expired"}`)
	require.JSONEq(t, string(task.Error), string(imageTaskToPublicForContext(ctx, task).Error))
}
