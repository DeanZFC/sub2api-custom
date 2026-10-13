package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type upstreamModelReplacementRepo struct {
	AccountRepository
	ids     []int64
	updates AccountBulkUpdate
	rows    int64
	err     error
}

func (r *upstreamModelReplacementRepo) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.ids, r.updates = ids, updates
	return r.rows, r.err
}

func modelReplacementAccount() *Account {
	a := &Account{
		ID: 91, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "original-key", "base_url": "https://provider.example/v1",
			"model_mapping": map[string]any{"removed-model": "removed-model", "alias": "retained-model", "*": "removed-model"},
		},
		Extra: map[string]any{"unrelated_setting": true},
	}
	a.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Models: map[string]UpstreamModelMetadata{
			"removed-model":  {ID: "removed-model", ContextWindow: 100000},
			"retained-model": {ID: "retained-model", ContextWindow: 200000},
		},
	})
	return a
}

func modelReplacementResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestReplaceModelMappingWithUpstream(t *testing.T) {
	for _, complete := range []bool{true, false} {
		name := "with capabilities"
		if !complete {
			name = "without capabilities"
		}
		t.Run(name, func(t *testing.T) {
			body := `{"data":[{"id":"new-model","reasoning":false,"input_modalities":["text"],"context_window":128000},{"id":"retained-model","reasoning":false,"input_modalities":["text"],"context_window":256000}]}`
			if !complete {
				body = `{"data":[{"id":"new-model"},{"id":"retained-model"},{"id":"new-model"}]}`
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				modelReplacementResponse(http.StatusOK, body),
				modelReplacementResponse(http.StatusBadGateway, `{}`),
			}}
			repo := &upstreamModelReplacementRepo{rows: 1}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			account := modelReplacementAccount()
			// Prime the mapping cache to check reads after synchronization, too.
			require.True(t, account.IsModelSupported("removed-model"))
			catalog, err := svc.ReplaceModelMappingWithUpstream(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, []string{"new-model", "retained-model"}, catalog.Models)
			require.Equal(t, []int64{91}, repo.ids)
			wantMapping := map[string]any{"new-model": "new-model", "retained-model": "retained-model"}
			// Only model configuration is patched; credentials refreshed during the
			// network request must not be overwritten by this account snapshot.
			require.Equal(t, map[string]any{"model_mapping": wantMapping}, repo.updates.Credentials)
			require.Equal(t, wantMapping, account.Credentials["model_mapping"])
			require.Equal(t, "original-key", account.GetCredential("api_key"))
			require.Equal(t, true, account.Extra["unrelated_setting"])
			require.False(t, account.IsModelSupported("removed-model"))
			require.False(t, account.IsModelSupported("alias"))
			require.False(t, account.IsModelSupported("anything"))
			require.True(t, account.IsModelSupported("new-model"))
			snapshot := repo.updates.Extra[UpstreamModelMetadataExtraKey].(UpstreamModelMetadataSnapshot)
			require.NotContains(t, snapshot.Models, "removed-model")
			require.Contains(t, snapshot.Models, "retained-model")
			if complete {
				require.Empty(t, catalog.Warnings)
				require.Contains(t, snapshot.Models, "new-model")
			} else {
				require.Equal(t, int64(200000), snapshot.Models["retained-model"].ContextWindow)
				require.Equal(t, UpstreamModelMetadataIncompleteCode, catalog.Warnings[0].Code)
			}
		})
	}
}

func TestReplaceModelMappingWithUpstreamPreservesConfigurationOnFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"upstream failure", http.StatusBadGateway, `{}`},
		{"unsupported endpoint", http.StatusNotFound, `{}`},
		{"unsupported method", http.StatusMethodNotAllowed, `{}`},
		{"empty list", http.StatusOK, `{"data":[]}`},
		{"invalid JSON", http.StatusOK, `invalid`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &upstreamModelReplacementRepo{rows: 1}
			upstream := &httpUpstreamRecorder{resp: modelReplacementResponse(tt.status, tt.body)}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			account := modelReplacementAccount()
			originalCredentials, originalExtra := shallowCopyMap(account.Credentials), shallowCopyMap(account.Extra)
			_, err := svc.ReplaceModelMappingWithUpstream(context.Background(), account)
			require.Error(t, err)
			require.Nil(t, repo.ids)
			require.Equal(t, originalCredentials, account.Credentials)
			require.Equal(t, originalExtra, account.Extra)
			require.Len(t, upstream.requests, 1, "failed model-list fetch must not fall back to configured models")
		})
	}
}

func TestReplaceModelMappingWithUpstreamPersistenceFailure(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "write failure", true: "account deleted"}[missing], func(t *testing.T) {
			repo := &upstreamModelReplacementRepo{err: errors.New("database unavailable")}
			if missing {
				repo.err = nil
			}
			upstream := &httpUpstreamRecorder{resp: modelReplacementResponse(http.StatusOK,
				`{"data":[{"id":"new-model","reasoning":false,"input_modalities":["text"],"context_window":128000}]}`)}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
			account := modelReplacementAccount()
			originalCredentials, originalExtra := shallowCopyMap(account.Credentials), shallowCopyMap(account.Extra)
			_, err := svc.ReplaceModelMappingWithUpstream(context.Background(), account)
			var syncErr *UpstreamModelSyncError
			require.ErrorAs(t, err, &syncErr)
			require.Equal(t, UpstreamModelSyncErrorInternal, syncErr.Kind)
			require.Equal(t, originalCredentials, account.Credentials)
			require.Equal(t, originalExtra, account.Extra)
		})
	}
}
