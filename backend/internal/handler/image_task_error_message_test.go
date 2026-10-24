package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageTaskStoresRawErrorAndWrapsPollingResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, upstream := range []bool{true, false} {
		t.Run(map[bool]string{true: "upstream request error", false: "local billing error"}[upstream], func(t *testing.T) {
			store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
			tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
			owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
			task, err := tasks.Create(context.Background(), owner)
			require.NoError(t, err)
			h := &AsyncImageHandler{tasks: tasks, execute: func(_ string, c *gin.Context) {
				if upstream {
					service.SetOpsUpstreamError(c, http.StatusBadRequest, "private validation failure", "")
					service.MarkOpsRequestScopedError(c, "invalid_request_error")
					c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "private validation failure", "details": "private debug data"}})
				} else {
					c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{"type": "billing_error", "message": "Insufficient balance"}})
				}
			}}
			initial, _ := gin.CreateTestContext(httptest.NewRecorder())
			initial.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", nil).
				WithContext(service.WithUpstreamErrorMessage(context.Background(), "统一提示"))
			taskCtx, recorder, cancel := newAsyncImageContext(initial, nil, time.Minute)
			h.run(task.ID, service.PlatformOpenAI, taskCtx, recorder, cancel)
			stored, err := store.Get(context.Background(), task.ID)
			require.NoError(t, err)
			require.Equal(t, service.ImageTaskStatusFailed, stored.Status)

			pollWriter := httptest.NewRecorder()
			poll, _ := gin.CreateTestContext(pollWriter)
			poll.Request = httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+task.ID, nil).
				WithContext(service.WithUpstreamErrorMessage(context.Background(), "统一提示"))
			poll.Params = gin.Params{{Key: "task_id", Value: task.ID}}
			poll.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: owner.APIKeyID, UserID: owner.UserID})
			h.Get(poll)
			require.Equal(t, http.StatusOK, pollWriter.Code)
			var public service.ImageTask
			require.NoError(t, json.Unmarshal(pollWriter.Body.Bytes(), &public))
			if upstream {
				require.Contains(t, string(stored.Error), "private validation failure")
				require.Equal(t, "upstream", stored.ErrorSource)
				require.Contains(t, string(public.Error), "统一提示")
				require.NotContains(t, pollWriter.Body.String(), "private")
			} else {
				require.Equal(t, "local", stored.ErrorSource)
				require.Contains(t, string(public.Error), "Insufficient balance")
			}
		})
	}
}

func TestAsyncImageTaskRecordsOriginalUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const original = "Excel BPS did not deliver validated output within the allowed interval"
	for _, tc := range []struct {
		name          string
		wireStatus    int
		bodyMessage   string
		contextReason bool
		clientWrapped bool
	}{
		{name: "HTTP error with original context", wireStatus: http.StatusBadGateway, bodyMessage: "Upstream request failed", contextReason: true},
		{name: "HTTP 200 error body", wireStatus: http.StatusOK, bodyMessage: original},
		{name: "HTTP 200 preserves original context", wireStatus: http.StatusOK, bodyMessage: "Upstream request failed", contextReason: true},
		{name: "HTTP error already wrapped for client", wireStatus: http.StatusBadGateway, bodyMessage: original, contextReason: true, clientWrapped: true},
		{name: "HTTP 200 already wrapped for client", wireStatus: http.StatusOK, bodyMessage: original, contextReason: true, clientWrapped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
			tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
			owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
			task, err := tasks.Create(context.Background(), owner)
			require.NoError(t, err)
			repo := &ingressRejectOpsRepo{}
			ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			executions := 0
			h := &AsyncImageHandler{
				tasks: tasks, openAI: &OpenAIGatewayHandler{opsService: ops},
				execute: func(_ string, c *gin.Context) {
					executions++
					if tc.contextReason {
						service.SetOpsUpstreamError(c, http.StatusBadGateway, original, "original provider diagnostics")
					}
					payload, err := json.Marshal(gin.H{"error": gin.H{
						"type": "upstream_error", "code": "generation_failed", "message": tc.bodyMessage,
					}})
					require.NoError(t, err)
					if tc.clientWrapped {
						payload = service.WrapUpstreamErrorPayload(c.Request.Context(), payload, "")
					}
					c.Data(tc.wireStatus, "application/json", payload)
				},
			}
			ctx := service.WithUpstreamErrorMessage(context.Background(), "统一提示")
			initial, _ := gin.CreateTestContext(httptest.NewRecorder())
			initial.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", nil).WithContext(ctx)
			initial.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: owner.APIKeyID, UserID: owner.UserID})
			taskCtx, recorder, cancel := newAsyncImageContext(initial, nil, time.Minute)
			h.run(task.ID, service.PlatformOpenAI, taskCtx, recorder, cancel)
			require.Equal(t, 1, executions)

			stored, err := store.Get(context.Background(), task.ID)
			require.NoError(t, err)
			require.Equal(t, service.ImageTaskStatusFailed, stored.Status)
			require.Equal(t, http.StatusBadGateway, stored.HTTPStatus)
			require.Equal(t, "upstream", stored.ErrorSource)
			require.Empty(t, stored.Result, "an HTTP 200 error envelope must not become a completed image result")
			if tc.clientWrapped {
				require.Contains(t, string(stored.Error), "统一提示")
			} else {
				require.Contains(t, string(stored.Error), tc.bodyMessage)
				require.NotContains(t, string(stored.Error), "统一提示")
			}
			public, err := tasks.Get(ctx, owner, task.ID)
			require.NoError(t, err)
			require.Contains(t, string(public.Error), "统一提示")
			require.NotContains(t, string(public.Error), tc.bodyMessage)

			require.Equal(t, int64(1), OpsErrorLogQueueLength(), "detached execution must record its failure once")
			job := <-opsErrorLogQueue
			require.Equal(t, http.StatusBadGateway, job.entry.StatusCode)
			require.False(t, job.entry.Stream, "HTTP 200 error bodies are non-stream failures")
			require.NotNil(t, job.entry.UpstreamErrorMessage)
			require.Equal(t, original, *job.entry.UpstreamErrorMessage)
			if !tc.clientWrapped {
				require.NotContains(t, job.entry.ErrorBody, "统一提示")
			}
			if tc.contextReason {
				require.NotNil(t, job.entry.UpstreamErrorDetail)
				require.Equal(t, "original provider diagnostics", *job.entry.UpstreamErrorDetail)
			}
			flushOpsErrorLogBatch([]opsErrorLogJob{job})
			require.Len(t, repo.entries, 1)
			require.Equal(t, original, *repo.entries[0].UpstreamErrorMessage)
		})
	}
}
