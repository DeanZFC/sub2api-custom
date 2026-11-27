//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type probeRuleRepoStub struct {
	service.GroupProbeRepository
	saved []service.GroupProbeRuleUpdate
}

func (r *probeRuleRepoStub) Configure(_ context.Context, _ int64, _ string, _ bool, rule service.GroupProbeRuleUpdate) error {
	r.saved = append(r.saved, rule)
	return nil
}

func (r *probeRuleRepoStub) Claim(context.Context) (*service.GroupProbeJob, time.Duration, error) {
	return nil, service.GroupProbePollInterval, nil
}

type probeRuleGroupStub struct{ service.GroupRepository }

func (probeRuleGroupStub) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	return &service.Group{ID: id, Status: service.StatusActive, Platform: service.PlatformOpenAI}, nil
}

func TestGroupProbeConfigureTrafficRule(t *testing.T) {
	repo := &probeRuleRepoStub{}
	svc := service.NewGroupProbeService(repo, probeRuleGroupStub{}, nil, nil)
	t.Cleanup(svc.Stop)
	router := gin.New()
	router.PUT("/group-probes/:id", NewGroupProbeHandler(svc).Configure)
	save := func(body string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/group-probes/7", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
	}
	save(`{"model":"m","enabled":true,"idle_window_minutes":15,"min_requests":3,"duration_minutes":12,"interval_seconds":17}`, http.StatusOK)
	save(`{"model":"m","enabled":false}`, http.StatusOK)
	save(`{"model":"m","enabled":true,"idle_window_minutes":0}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"idle_window_minutes":1441}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"min_requests":0}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"min_requests":2.5}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"duration_minutes":0}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"duration_minutes":1441}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"interval_seconds":0}`, http.StatusBadRequest)
	save(`{"model":"m","enabled":true,"interval_seconds":86401}`, http.StatusBadRequest)
	require.Equal(t, []service.GroupProbeRuleUpdate{
		{IdleWindowMinutes: new(int32(15)), MinRequests: new(int32(3)), DurationMinutes: new(int32(12)), IntervalSeconds: new(int32(17))},
		{},
	}, repo.saved)
}
