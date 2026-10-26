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
	saved         int
	actions       int
	action        string
	seconds       int
	model         string
	modelRequired bool
	conflict      bool
}

type passiveQueryHandlerRepo struct {
	passiveHandlerRepo
	query service.PassiveQualityQuery
	err   error
}

func (r *passiveQueryHandlerRepo) QueryStates(_ context.Context, q service.PassiveQualityQuery) (*service.PassiveQualityStatePage, error) {
	r.query = q
	return &service.PassiveQualityStatePage{Items: []service.PassiveQualityState{}, Total: 12345}, r.err
}
func (r *passiveQueryHandlerRepo) QueryEvents(_ context.Context, q service.PassiveQualityQuery) (*service.PassiveQualityEventPage, error) {
	r.query = q
	return &service.PassiveQualityEventPage{Items: []service.PassiveQualityEvent{}, HasMore: true, NextCursor: "22"}, r.err
}
func (r *passiveQueryHandlerRepo) StateSummary(context.Context) (*service.PassiveQualityStateSummary, error) {
	return &service.PassiveQualityStateSummary{Total: 12345}, nil
}

func TestPassiveQualityReportValidation(t *testing.T) {
	repo := &passiveQueryHandlerRepo{}
	h := NewPassiveQualityHandler(service.NewPassiveQualityService(repo, nil))
	router := gin.New()
	router.GET("/states", h.States)
	router.GET("/events", h.Events)
	router.GET("/summary", h.StateSummary)
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	require.Equal(t, 200, request("/events?paginated=true&policy_id=7&account_id=62&model=model-a&kind=paused&cursor=30&limit=20&since=2026-10-01T00:00:00Z&until=2026-10-02T00:00:00Z").Code)
	require.EqualValues(t, 7, repo.query.PolicyID)
	require.EqualValues(t, 62, repo.query.AccountID)
	require.Equal(t, "model-a", repo.query.Model)
	require.Equal(t, "paused", repo.query.Kind)
	require.Equal(t, "30", repo.query.Cursor)
	require.Equal(t, 20, repo.query.Limit)
	require.NotNil(t, repo.query.Since)
	require.NotNil(t, repo.query.Until)
	for _, filter := range []string{"policy_id=-1", "account_id=nope", "limit=201", "since=bad", "since=2026-10-02T00:00:00Z&until=2026-10-01T00:00:00Z"} {
		require.Equal(t, 400, request("/events?paginated=true&"+filter).Code, filter)
	}
	require.Contains(t, request("/states?paginated=true&phase=paused").Body.String(), `"total":12345`)
	require.Equal(t, "paused", repo.query.Phase)
	require.Contains(t, request("/summary").Body.String(), `"total":12345`)
	repo.err = service.ErrPassiveQualityCursor
	require.Equal(t, 400, request("/events?paginated=true&cursor=bad").Code)
}

func (r *passiveHandlerRepo) SavePolicy(_ context.Context, p *service.PassiveQualityPolicy) error {
	if r.conflict {
		return service.ErrPassiveQualityConflict
	}
	r.saved++
	p.ID = 5
	return nil
}
func (r *passiveHandlerRepo) Act(_ context.Context, _, _ int64, action string, seconds int, models ...string) error {
	if r.modelRequired && (len(models) == 0 || models[0] == "") {
		return service.ErrPassiveQualityModelRequired
	}
	r.actions++
	r.action = action
	r.seconds = seconds
	if len(models) > 0 {
		r.model = models[0]
	}
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
	cfg.SchemaVersion, cfg.Scope = 2, ""
	raw, err = json.Marshal(cfg)
	require.NoError(t, err)
	response = request("/policies", string(raw), http.MethodPost)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"scope":"model"`)
	require.Contains(t, response.Body.String(), `"model":""`)
	require.Equal(t, 3, repo.saved)
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
	repo.modelRequired = true
	require.Equal(t, 400, request("/policies/5/accounts/62/action", `{"action":"verify"}`, http.MethodPost).Code)
	require.Equal(t, 204, request("/policies/5/accounts/62/action", `{"action":"exempt","seconds":900,"model":" model-b "}`, http.MethodPost).Code)
	require.Equal(t, "model-b", repo.model)
}
