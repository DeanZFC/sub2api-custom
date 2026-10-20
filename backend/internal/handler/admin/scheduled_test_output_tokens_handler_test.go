package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type outputTokensDefinitionRepo struct {
	service.ScheduledTestDefinitionRepository
	definition service.ScheduledTestDefinition
	writes     int
}

func (r *outputTokensDefinitionRepo) GetByID(context.Context, int64) (*service.ScheduledTestDefinition, error) {
	d := r.definition
	return &d, nil
}

func (r *outputTokensDefinitionRepo) Create(_ context.Context, d *service.ScheduledTestDefinition) (*service.ScheduledTestDefinition, error) {
	r.definition = *d
	r.writes++
	return d, nil
}

func (r *outputTokensDefinitionRepo) Update(ctx context.Context, d *service.ScheduledTestDefinition) (*service.ScheduledTestDefinition, error) {
	return r.Create(ctx, d)
}

func TestScheduledTestDefinitionOutputTokenLimit(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		want, status       int
	}{
		{"create", "POST", `{"key":"pelican","name":"Pelican","prompt":"Draw a pelican","output_kind":"html","max_output_tokens":32768}`, 32768, 200},
		{"update", "PUT", `{"max_output_tokens":49152}`, 49152, 200},
		{"partial preserves", "PUT", `{"sort_order":4}`, 32768, 200},
		{"reset default", "PUT", `{"max_output_tokens":0}`, 0, 200},
		{"minimum", "PUT", `{"max_output_tokens":1}`, 1, 200},
		{"maximum", "PUT", `{"max_output_tokens":131072}`, 131072, 200},
		{"negative", "PUT", `{"max_output_tokens":-1}`, 0, 400},
		{"too large", "PUT", `{"max_output_tokens":131073}`, 0, 400},
		{"fraction", "PUT", `{"max_output_tokens":1.5}`, 0, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &outputTokensDefinitionRepo{definition: service.ScheduledTestDefinition{ID: 1, Key: "pelican", Name: "Pelican", Prompt: "Draw a pelican", OutputKind: "html", MaxOutputTokens: 32768}}
			svc := service.NewScheduledTestService(nil, nil)
			svc.SetDefinitionRepository(repo)
			handler := NewScheduledTestHandler(svc)
			router := gin.New()
			router.POST("/types/:id", handler.CreateDefinition)
			router.PUT("/types/:id", handler.UpdateDefinition)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, "/types/1", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.status == http.StatusOK {
				var d service.ScheduledTestDefinition
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &d))
				require.Equal(t, tc.want, d.MaxOutputTokens)
				require.Equal(t, tc.want, repo.definition.MaxOutputTokens)
				require.Equal(t, 1, repo.writes)
			} else {
				require.Zero(t, repo.writes)
			}
		})
	}
}
