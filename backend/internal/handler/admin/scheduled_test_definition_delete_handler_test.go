package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type deleteDefinitionHandlerRepo struct {
	service.ScheduledTestDefinitionRepository
	err error
}

func (r deleteDefinitionHandlerRepo) Delete(context.Context, int64) error {
	return r.err
}

func TestScheduledTestDefinitionDelete(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"unused or historical only", nil, http.StatusOK},
		{"current plan or active hold", service.ErrScheduledTestDefinitionInUse, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.NewScheduledTestService(nil, nil)
			svc.SetDefinitionRepository(deleteDefinitionHandlerRepo{err: tc.err})
			router := gin.New()
			router.DELETE("/types/:id", NewScheduledTestHandler(svc).DeleteDefinition)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/types/1", nil))
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.err != nil {
				require.Contains(t, recorder.Body.String(), tc.err.Error())
			}
		})
	}
}
