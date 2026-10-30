package middleware

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const duplicateModelFieldsMessage = "duplicate or case-conflicting model fields are not allowed"

func newRequestModelFieldsTestRouter(t *testing.T, limit int64, next gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestModelFields(limit, limit))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodGet, http.MethodDelete} {
		router.Handle(method, "/v1/responses", next)
	}
	return router
}

func TestRequestModelFieldsRejectsBeforeDownstream(t *testing.T) {
	tests := []struct{ name, body string }{
		{"native input", `{"model":"gpt-6-astra","model":"gpt-6.1-sol","input":"hi"}`},
		{"legacy messages", `{"model":"gpt-6-astra","model":"gpt-6.1-sol","messages":[{"role":"user","content":"hi"}]}`},
		{"case variant", `{"model":"one","Model":"two"}`},
		{"identical values", `{"model":"one","model":"one"}`},
		{"escaped key", `{"\u006dodel":"one","model":"two"}`},
		{"null duplicate", `{"model":"one","model":null}`},
		{"non-string duplicate", `{"model":1,"MODEL":false}`},
		{"empty duplicate", `{"model":"","model":""}`},
		{"lenient control byte", "{\"model\":\"one\",\"model\":\"two\",\"input\":\"first\nsecond\"}"},
		{"utf8 BOM", "\xef\xbb\xbf" + `{"model":"one","model":"two"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			router := newRequestModelFieldsTestRouter(t, 1<<20, func(c *gin.Context) { called = true; c.Status(http.StatusOK) })
			w := doJSON(t, router, http.MethodPost, "/v1/responses", tc.body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), duplicateModelFieldsMessage)
			require.Contains(t, w.Body.String(), "invalid_request_error")
			require.NotContains(t, w.Body.String(), "model_not_found")
			require.False(t, called, "ambiguous model reached downstream parsing")
		})
	}
}

func TestRequestModelFieldsRejectsJSONRegardlessOfContentType(t *testing.T) {
	for _, contentType := range []string{"", "application/json", "text/plain", "application/sdp", "multipart/form-data; boundary=test"} {
		t.Run(contentType, func(t *testing.T) {
			router := newRequestModelFieldsTestRouter(t, 1<<20, func(c *gin.Context) { t.Error("ambiguous JSON reached handler") })
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"one","model":"two"}`))
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
}

func TestRequestModelFieldsPreservesBody(t *testing.T) {
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	require.NoError(t, writer.WriteField("model", "one"))
	require.NoError(t, writer.WriteField("model", "two"))
	require.NoError(t, writer.WriteField("session", `{"model":"one","model":"two"}`))
	require.NoError(t, writer.Close())
	tests := []struct{ name, contentType, body string }{
		{"valid", "application/json", `{"model":"one","input":"hi"}`},
		{"nested fields", "application/json", `{"model":"one","metadata":{"model":"two","model":"three"}}`},
		{"malformed JSON", "application/json", `{"model":"one","model":"two"`},
		{"JSON array", "application/json", `[{"model":"one","model":"two"}]`},
		{"multipart", writer.FormDataContentType(), form.String()},
		{"sdp", "application/sdp", "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\n"},
		{"lenient control byte", "application/json", "{\"model\":\"one\",\"input\":\"first\nsecond\"}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := newRequestModelFieldsTestRouter(t, 1<<20, func(c *gin.Context) {
				_, preread := c.Request.Body.(*httputil.PrereadBody)
				require.True(t, preread)
				body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(body))
				require.Equal(t, int64(len(tc.body)), c.Request.ContentLength)
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		})
	}
}

func TestRequestModelFieldsSkipsBodylessMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			body := &readTrackingBody{Reader: strings.NewReader(`{"model":"one","model":"two"}`)}
			router := newRequestModelFieldsTestRouter(t, 1<<20, func(c *gin.Context) { c.Status(http.StatusOK) })
			req := httptest.NewRequest(method, "/v1/responses", body)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			require.False(t, body.read)
		})
	}
}

type requestModelReadError struct{}

func (requestModelReadError) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (requestModelReadError) Close() error             { return nil }

func TestRequestModelFieldsReadErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   io.ReadCloser
		limit  int64
		status int
	}{
		{"read error", requestModelReadError{}, 1024, http.StatusBadRequest},
		{"wire limit", http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(strings.NewReader(`{"model":"one"}`)), 4), 1024, http.StatusRequestEntityTooLarge},
		{"normalized limit", io.NopCloser(strings.NewReader(`{"model":"one"}`)), 4, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := newRequestModelFieldsTestRouter(t, tc.limit, func(c *gin.Context) { t.Error("failed body reached handler") })
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", tc.body)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}

func TestRequestModelFieldsRejectsCompressedDuplicateJSON(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte(`{"model":"one","model":"two"}`))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	router := newRequestModelFieldsTestRouter(t, 1<<20, func(c *gin.Context) { t.Error("compressed ambiguous JSON reached handler") })
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", &compressed)
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), duplicateModelFieldsMessage)
}

func TestRequestModelFieldsLeavesNonJSONUploadsUnread(t *testing.T) {
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	require.NoError(t, writer.WriteField("model", "one"))
	require.NoError(t, writer.WriteField("model", "two"))
	require.NoError(t, writer.WriteField("sdp", "v=0"))
	require.NoError(t, writer.Close())
	tests := []struct{ path, contentType, body string }{
		{"/v1/live", writer.FormDataContentType(), form.String()},
		{"/backend-api/codex/realtime/calls", writer.FormDataContentType(), form.String()},
		{"/v1/images/generations", writer.FormDataContentType(), form.String()},
		{"/v1/images/edits", writer.FormDataContentType(), form.String()},
		{"/images/generations", writer.FormDataContentType(), form.String()},
		{"/images/edits", writer.FormDataContentType(), form.String()},
		{"/v1/stt", writer.FormDataContentType(), form.String()},
		{"/stt", "audio/wav", "RIFF audio upload"},
		{"/v1/custom-voices", writer.FormDataContentType(), form.String()},
		{"/custom-voices", "application/octet-stream", "voice upload"},
	}
	for _, tc := range tests {
		t.Run(tc.path+"/"+tc.contentType, func(t *testing.T) {
			body := &readTrackingBody{Reader: strings.NewReader(tc.body)}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})
				c.Next()
			})
			router.Use(RequestModelFields(1<<20, 1024))
			router.POST(tc.path, func(c *gin.Context) {
				require.False(t, body.read, "guard must leave upload reads to the endpoint")
				require.Same(t, body, c.Request.Body)
				content, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(content))
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, body)
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		})
	}
}

func TestRequestModelFieldsMediaJSONStillValidated(t *testing.T) {
	for _, tc := range []struct{ path, platform, contentType string }{
		{"/v1/images/generations", service.PlatformOpenAI, "application/json"},
		{"/v1/images/edits", service.PlatformGrok, "multipart/form-data; boundary=test"},
		{"/v1/images/edits", service.PlatformComposite, "multipart/form-data; boundary=test"},
		{"/v1/videos/edits", service.PlatformGrok, "multipart/form-data; boundary=test"},
		{"/v1/live", service.PlatformOpenAI, "application/json"},
		{"/v1/stt", service.PlatformGrok, "application/json"},
	} {
		t.Run(tc.path+"/"+tc.platform, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: tc.platform}})
				c.Next()
			})
			router.Use(RequestModelFields(1<<20, 1024))
			router.POST(tc.path, func(c *gin.Context) { t.Error("ambiguous JSON reached media handler") })
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"one","model":"two"}`))
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), duplicateModelFieldsMessage)
		})
	}
}

type requestModelCountingBody struct {
	io.Reader
	bytesRead int
}

func (b *requestModelCountingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.bytesRead += n
	return n, err
}

func (*requestModelCountingBody) Close() error { return nil }

func TestRequestModelFieldsTextLimitBeforePreread(t *testing.T) {
	const textLimit int64 = 64
	for _, path := range []string{"/v1/systemone", "/v1/alpha/search", "/alpha/search", "/backend-api/codex/alpha/search", "/v1/embeddings", "/embeddings"} {
		t.Run(path, func(t *testing.T) {
			body := &requestModelCountingBody{Reader: strings.NewReader(`{"model":"one","input":"` + strings.Repeat("x", 1<<20) + `"}`)}
			router := gin.New()
			router.Use(RequestBodyLimit(256 << 20))
			router.Use(RequestModelFields(256<<20, textLimit))
			router.POST(path, RequestBodyLimit(textLimit), func(c *gin.Context) { t.Error("oversized text reached handler") })
			req := httptest.NewRequest(http.MethodPost, path, body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
			require.Greater(t, body.bytesRead, 0)
			require.LessOrEqual(t, int64(body.bytesRead), textLimit+1, "guard must stop reading at the text limit")
		})
	}
}
