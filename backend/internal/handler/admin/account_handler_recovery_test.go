//go:build unit

package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qualityRecoveryAccountRepoStub struct {
	service.AccountRepository
	account *service.Account
	calls   int
	err     error
}

func (r *qualityRecoveryAccountRepoStub) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

func (r *qualityRecoveryAccountRepoStub) ClearAccountQualityProtection(context.Context, int64) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.account.Status = service.StatusActive
	r.account.Extra = map[string]any{}
	return nil
}

type qualityRecoveryAdminStub struct {
	service.AdminService
	repo *qualityRecoveryAccountRepoStub
}

func (s *qualityRecoveryAdminStub) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.repo.account, nil
}

func (s *qualityRecoveryAdminStub) ClearAccountError(context.Context, int64) (*service.Account, error) {
	s.repo.account.Status = service.StatusActive
	return s.repo.account, nil
}

func TestAccountHandler_ManualRecoveryReleasesQualityProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/accounts/42/recover-state", "/accounts/batch-clear-error"} {
		for _, fails := range []bool{false, true} {
			t.Run(path+map[bool]string{false: " success", true: " failure"}[fails], func(t *testing.T) {
				repo := &qualityRecoveryAccountRepoStub{account: &service.Account{
					ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusQualityPaused, Schedulable: true,
					Extra: map[string]any{"active_test_model_holds": map[string]any{"model-a": "failed"}},
				}}
				if fails {
					repo.err = errors.New("quality recovery failed")
				}
				h := &AccountHandler{
					adminService:     &qualityRecoveryAdminStub{repo: repo},
					rateLimitService: service.NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
				}
				router := gin.New()
				router.POST("/accounts/:id/recover-state", h.RecoverState)
				router.POST("/accounts/batch-clear-error", h.BatchClearError)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"account_ids":[42]}`))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(response, request)
				require.Equal(t, 1, repo.calls)
				if fails {
					if strings.HasSuffix(path, "batch-clear-error") {
						require.Contains(t, response.Body.String(), "quality recovery failed")
						require.Contains(t, response.Body.String(), `"failed":1`)
						require.Contains(t, response.Body.String(), `"success":0`)
					} else {
						require.Equal(t, http.StatusInternalServerError, response.Code)
					}
					return
				}
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, service.StatusActive, repo.account.Status)
				require.Empty(t, repo.account.Extra)
			})
		}
	}
}
