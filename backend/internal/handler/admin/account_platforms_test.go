package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type platformCountsAdminService struct {
	*stubAdminService
	counts []service.AccountPlatformCount
	err    error
}

func (s *platformCountsAdminService) ListAccountPlatformCounts(context.Context) ([]service.AccountPlatformCount, error) {
	return s.counts, s.err
}

func TestAccountHandlerListPlatforms(t *testing.T) {
	for _, test := range []struct {
		name   string
		counts []service.AccountPlatformCount
		err    error
		status int
	}{
		{"global counts", []service.AccountPlatformCount{{Platform: "anthropic", Count: 2}, {Platform: "openai", Count: 1001}}, nil, http.StatusOK},
		{"empty", nil, nil, http.StatusOK},
		{"failed", nil, errors.New("database offline"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := &platformCountsAdminService{stubAdminService: newStubAdminService(), counts: test.counts, err: test.err}
			handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/api/v1/admin/accounts/platforms", handler.ListPlatforms)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/platforms?page=2&platform=openai&status=active&search=missing", nil))
			require.Equal(t, test.status, rec.Code)
			if test.err == nil {
				var body struct {
					Data []service.AccountPlatformCount `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				require.NotNil(t, body.Data)
				if len(test.counts) > 0 {
					require.Equal(t, test.counts, body.Data)
				} else {
					require.Empty(t, body.Data)
				}
			}
		})
	}
}
