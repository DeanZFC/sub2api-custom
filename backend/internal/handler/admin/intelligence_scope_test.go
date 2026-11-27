package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceScopeCountsTextTargetsAcrossGroups(t *testing.T) {
	admin := newStubAdminService()
	account := func(id int64, credentials map[string]any) service.Account {
		return service.Account{ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Credentials: credentials}
	}
	admin.accountSchedulerScoreFilterAccounts = []service.Account{
		account(1, map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}}),
		account(2, map[string]any{"openai_capabilities": []any{"embeddings"}}),
		account(3, map[string]any{"model_mapping": map[string]any{"text-a": "gpt-image-2", "text-b": "text-b"}}),
		account(4, nil),
	}
	router := gin.New()
	router.POST("/scope", NewGroupHandler(admin, nil, nil).IntelligenceScope)
	req := httptest.NewRequest(http.MethodPost, "/scope", strings.NewReader(`{"group_ids":[1,2],"model_ids":["text-a","text-b"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"targets":3,"account_ids":[3,4],"group_counts":{"1":2,"2":2}}`, rec.Body.String())
}
