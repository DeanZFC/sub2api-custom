package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passiveHandlerRepo struct {
	service.PassiveQualityRepository
	saved    int
	actions  int
	action   string
	seconds  int
	conflict bool
}

func (r *passiveHandlerRepo) SavePolicy(_ context.Context, p *service.PassiveQualityPolicy) error {
	if r.conflict {
		return service.ErrPassiveQualityConflict
	}
	r.saved++
	p.ID = 5
	return nil
}
func (r *passiveHandlerRepo) Act(_ context.Context, _, _ int64, action string, seconds int) error {
	r.actions++
	r.action = action
	r.seconds = seconds
	return nil
}
func TestPassiveQualityAdminValidation(t *testing.T) {
	repo := &passiveHandlerRepo{}
	svc := service.NewPassiveQualityService(repo, nil)
	h := NewPassiveQualityHandler(svc)
	router := gin.New()
	router.POST("/policies", h.Save)
	router.PUT("/policies/:id", h.Save)
	router.POST("/policies/:id/accounts/:account/action", h.Act)
	request := func(path, body string, method string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 400, request("/policies", `{"enabled":true}`, http.MethodPost).Code)
	require.Zero(t, repo.saved)
	cfg := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Name: "policy", Model: "model", Enabled: true, Mode: "observe", Scope: "model", GroupIDs: []int64{8}, WindowSeconds: 600, MaxSamples: 100, MinSamples: 30, Confirmations: 2, NewSamples: 5, ErrorRate: service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}, CooldownSeconds: 120, MaxCooldownSeconds: 3600, TrialSeconds: 300, TrialRequests: 50, TrialConcurrency: 1, RecoverySamples: 20}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.Equal(t, 200, request("/policies", string(raw), http.MethodPost).Code)
	require.Equal(t, 1, repo.saved)
	cfg.Model = " "
	raw, err = json.Marshal(cfg)
	require.NoError(t, err)
	response := request("/policies", string(raw), http.MethodPost)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"model":""`)
	require.Contains(t, response.Body.String(), `"scope":"account"`)
	require.Equal(t, 2, repo.saved)
	repo.conflict = true
	require.Equal(t, 409, request("/policies/5", string(raw), http.MethodPut).Code)
	for _, body := range []string{`{"action":"resume"}`, `{"action":"exempt","seconds":0}`, `{"action":"exempt","seconds":86401}`} {
		require.Equal(t, 400, request("/policies/5/accounts/62/action", body, http.MethodPost).Code)
	}
	require.Zero(t, repo.actions)
	require.Equal(t, 204, request("/policies/5/accounts/62/action", `{"action":"verify"}`, http.MethodPost).Code)
	require.Equal(t, "verify", repo.action)
	require.Equal(t, 204, request("/policies/5/accounts/62/action", `{"action":"exempt","seconds":900}`, http.MethodPost).Code)
	require.Equal(t, 900, repo.seconds)
}
