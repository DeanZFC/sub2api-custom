//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMainlandWebBlockRuntimeSwitchAppliesWithoutRestart(t *testing.T) {
	cfg := mainlandWebBlockTestConfig(t)
	cfg.Enabled = false // A stored website choice can enable the deployment default.
	var enabled atomic.Bool
	var calls atomic.Int32
	handler, err := NewMainlandWebBlock(cfg, func(ctx context.Context) bool {
		require.NotNil(t, ctx)
		calls.Add(1)
		return enabled.Load()
	})
	require.NoError(t, err)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(handler)
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "normal page") })
	for _, state := range []bool{false, true, false, true} {
		enabled.Store(state)
		want := http.StatusOK
		if state {
			want = http.StatusForbidden
		}
		for _, path := range []string{"/", "/login", "/home?redirect=/dashboard"} {
			w := mainlandWebBlockTestRequest(r, http.MethodGet, path, "203.0.113.42:1234", nil)
			require.Equal(t, want, w.Code)
		}
		previous := calls.Load()
		w := mainlandWebBlockTestRequest(r, http.MethodGet, "/v1/messages", "203.0.113.42:1234", nil)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, previous, calls.Load(), "API requests must skip runtime setting lookup")
	}
}

func TestMainlandWebBlockRuntimeSettingOverridesEnabledDeployment(t *testing.T) {
	handler, err := NewMainlandWebBlock(mainlandWebBlockTestConfig(t), func(context.Context) bool { return false })
	require.NoError(t, err)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(handler)
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "normal page") })
	w := mainlandWebBlockTestRequest(r, http.MethodGet, "/login", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMainlandWebBlockDisabledRuntimeDoesNotRequireCustomData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "initially-missing.zone")
	var enabled atomic.Bool
	handler, err := NewMainlandWebBlock(config.MainlandWebBlockConfig{Enabled: true, CIDRFile: path}, func(context.Context) bool { return enabled.Load() })
	require.NoError(t, err, "stored false overrides the enabled environment even with unavailable data")
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(handler)
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "normal page") })
	w := mainlandWebBlockTestRequest(r, http.MethodGet, "/login", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, os.WriteFile(path, []byte(mainlandWebBlockTestCIDRs), 0600))
	enabled.Store(true)
	w = mainlandWebBlockTestRequest(r, http.MethodGet, "/login", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusForbidden, w.Code, "first activation loads the now-available data without restart")
	require.NoError(t, os.Remove(path))
	w = mainlandWebBlockTestRequest(r, http.MethodGet, "/home", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusForbidden, w.Code, "a valid in-memory snapshot survives file removal")
}

func TestMainlandWebBlockRuntimeActivationWithoutDataFailsClosed(t *testing.T) {
	var enabled atomic.Bool
	handler, err := NewMainlandWebBlock(config.MainlandWebBlockConfig{CIDRFile: filepath.Join(t.TempDir(), "missing.zone")}, func(context.Context) bool { return enabled.Load() })
	require.NoError(t, err)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(handler)
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "normal page") })
	enabled.Store(true)
	w := mainlandWebBlockTestRequest(r, http.MethodGet, "/login", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "1", w.Result().Header.Get("Retry-After"))
	require.Contains(t, w.Result().Header.Get("Cache-Control"), "no-store")
	w = mainlandWebBlockTestRequest(r, http.MethodGet, "/v1/messages", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusOK, w.Code)
	enabled.Store(false)
	w = mainlandWebBlockTestRequest(r, http.MethodGet, "/login", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusOK, w.Code, "disabling restores normal documents even when data is invalid")
}

// Documentation prefixes keep these tests independent of the bundled country data.
const mainlandWebBlockTestCIDRs = "# Synthetic dual-stack mainland ranges\n203.0.113.0/24\n2001:db8:100::/48\n"

func mainlandWebBlockTestConfig(t *testing.T) config.MainlandWebBlockConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-cn.zone")
	require.NoError(t, os.WriteFile(path, []byte(mainlandWebBlockTestCIDRs), 0600))
	return config.MainlandWebBlockConfig{Enabled: true, CIDRFile: path}
}

func mainlandWebBlockTestRouter(t *testing.T, cfg config.MainlandWebBlockConfig, downstream gin.HandlerFunc) *gin.Engine {
	t.Helper()
	handler, err := NewMainlandWebBlock(cfg)
	require.NoError(t, err)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(handler)
	r.NoRoute(downstream)
	return r
}

func mainlandWebBlockTestRequest(r *gin.Engine, method, path, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remoteAddr
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMainlandWebBlockDisabledDoesNotLoadOrChangeResponses(t *testing.T) {
	for _, cfg := range []config.MainlandWebBlockConfig{
		{},
		{CIDRFile: filepath.Join(t.TempDir(), "missing.zone")},
	} {
		r := mainlandWebBlockTestRouter(t, cfg, func(c *gin.Context) {
			require.Equal(t, `"cached"`, c.GetHeader("If-None-Match"))
			c.Header("Cache-Control", "public, max-age=60")
			c.Header("ETag", `"cached"`)
			c.String(http.StatusOK, "original page")
		})
		w := mainlandWebBlockTestRequest(r, http.MethodGet, "/login", "203.0.113.42:1234", map[string]string{"If-None-Match": `"cached"`})
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "original page", w.Body.String())
		require.Equal(t, "public, max-age=60", w.Result().Header.Get("Cache-Control"))
		require.Equal(t, `"cached"`, w.Result().Header.Get("ETag"))
	}
}

func TestMainlandWebBlockDeniesOnlySelectedDocumentPaths(t *testing.T) {
	cfg := mainlandWebBlockTestConfig(t)
	r := mainlandWebBlockTestRouter(t, cfg, func(c *gin.Context) {
		t.Error("blocked document reached the downstream handler")
		c.String(http.StatusOK, "original page")
	})
	for _, remote := range []string{"203.0.113.42:1234", "[2001:db8:100::42]:1234", "[::ffff:203.0.113.42]:1234"} {
		for _, path := range []string{"/", "/login", "/home", "/login?redirect=/dashboard", "/home?lang=en"} {
			t.Run(remote+path, func(t *testing.T) {
				w := mainlandWebBlockTestRequest(r, http.MethodGet, path, remote, nil)
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Contains(t, w.Result().Header.Get("Content-Type"), "text/html")
				require.Contains(t, w.Body.String(), "当前地区暂不支持访问")
				require.Contains(t, w.Body.String(), `name="viewport"`)
				require.Contains(t, w.Body.String(), `aria-labelledby="notice-title"`)
				require.NotContains(t, strings.ToLower(w.Body.String()), "<script")
				require.Contains(t, w.Result().Header.Get("Cache-Control"), "no-store")
				require.Contains(t, w.Result().Header.Get("Content-Security-Policy"), "default-src 'none'")
			})
		}
	}
}

func TestMainlandWebBlockLeavesOtherPathsAndAPIsUntouched(t *testing.T) {
	r := mainlandWebBlockTestRouter(t, mainlandWebBlockTestConfig(t), func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=60")
		c.Header("ETag", `"original"`)
		c.String(http.StatusOK, "original response")
	})
	paths := []string{"/login/", "/home/", "/register", "/index.html", "/api/v1/auth/login", "/v1/messages", "/v1beta/models"}
	for _, remote := range []string{"203.0.113.42:1234", "[2001:db8:100::42]:1234", "198.51.100.7:1234"} {
		for _, path := range paths {
			t.Run(remote+path, func(t *testing.T) {
				w := mainlandWebBlockTestRequest(r, http.MethodGet, path, remote, nil)
				require.Equal(t, http.StatusOK, w.Code)
				require.Equal(t, "original response", w.Body.String())
				require.Equal(t, "public, max-age=60", w.Result().Header.Get("Cache-Control"))
				require.Equal(t, `"original"`, w.Result().Header.Get("ETag"))
			})
		}
	}
}

func TestMainlandWebBlockOverseasDocumentsCannotBeCachedOrRevalidated(t *testing.T) {
	for _, commit := range []string{"write", "write-header-now", "write-string", "flush"} {
		t.Run(commit, func(t *testing.T) {
			r := mainlandWebBlockTestRouter(t, mainlandWebBlockTestConfig(t), func(c *gin.Context) {
				require.Empty(t, c.GetHeader("If-None-Match"))
				require.Empty(t, c.GetHeader("If-Modified-Since"))
				c.Header("Cache-Control", "public, max-age=31536000")
				c.Header("CDN-Cache-Control", "public, max-age=31536000")
				c.Header("Cloudflare-CDN-Cache-Control", "public, max-age=31536000")
				c.Header("ETag", `"cached-index"`)
				c.Header("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
				c.Status(http.StatusOK)
				switch commit {
				case "write-header-now":
					c.Writer.WriteHeaderNow()
				case "write-string":
					_, err := c.Writer.WriteString("original page")
					require.NoError(t, err)
					return
				case "flush":
					c.Writer.Flush()
				}
				_, err := c.Writer.Write([]byte("original page"))
				require.NoError(t, err)
			})
			for _, remote := range []string{"198.51.100.7:1234", "[2001:db8:200::7]:1234"} {
				for _, path := range []string{"/", "/login", "/home"} {
					w := mainlandWebBlockTestRequest(r, http.MethodGet, path, remote, map[string]string{
						"If-None-Match": `"cached-index"`, "If-Modified-Since": "Wed, 21 Oct 2015 07:28:00 GMT",
					})
					require.Equal(t, http.StatusOK, w.Code)
					require.Equal(t, "original page", w.Body.String())
					headers := w.Result().Header // Inspect headers committed on the wire, not the mutable recorder map.
					require.Contains(t, headers.Get("Cache-Control"), "private")
					require.Contains(t, headers.Get("Cache-Control"), "no-store")
					require.Equal(t, "no-store", headers.Get("CDN-Cache-Control"))
					require.Equal(t, "no-store", headers.Get("Cloudflare-CDN-Cache-Control"))
					require.Empty(t, headers.Get("ETag"))
					require.Empty(t, headers.Get("Last-Modified"))
				}
			}
		})
	}
}

func TestMainlandWebBlockHeadReturnsForbiddenWithoutBody(t *testing.T) {
	r := mainlandWebBlockTestRouter(t, mainlandWebBlockTestConfig(t), func(c *gin.Context) {
		t.Error("blocked HEAD reached downstream")
	})
	w := mainlandWebBlockTestRequest(r, http.MethodHead, "/home", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, w.Body.String())
	require.Contains(t, w.Result().Header.Get("Cache-Control"), "no-store")
}

func TestMainlandWebBlockUsesTrustedProxyChainIndependentlyOfLegacyMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		remote  string
		trusted []string
		headers map[string]string
		want    int
	}{
		{name: "untrusted mainland cannot forge overseas headers", remote: "203.0.113.42:1234", headers: map[string]string{"CF-IPCountry": "US", "CF-Connecting-IP": "198.51.100.7", "X-Real-IP": "198.51.100.7", "X-Forwarded-For": "198.51.100.7"}, want: http.StatusForbidden},
		{name: "untrusted overseas cannot be classified from forged headers", remote: "198.51.100.7:1234", headers: map[string]string{"CF-IPCountry": "CN", "CF-Connecting-IP": "203.0.113.42", "X-Real-IP": "203.0.113.42", "X-Forwarded-For": "203.0.113.42"}, want: http.StatusOK},
		{name: "configured proxy forwards mainland IPv4", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32"}, headers: map[string]string{"X-Forwarded-For": "203.0.113.42"}, want: http.StatusForbidden},
		{name: "configured proxy forwards mainland IPv6", remote: "[::1]:1234", trusted: []string{"::1/128"}, headers: map[string]string{"X-Forwarded-For": "2001:db8:100::42"}, want: http.StatusForbidden},
		{name: "rightmost untrusted hop beats forged overseas prefix", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32", "10.0.0.0/24"}, headers: map[string]string{"X-Forwarded-For": "198.51.100.7, 203.0.113.42, 10.0.0.8"}, want: http.StatusForbidden},
		{name: "overseas forwarded client stays allowed", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32", "10.0.0.0/24"}, headers: map[string]string{"X-Forwarded-For": "203.0.113.42, 198.51.100.7, 10.0.0.8"}, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.SetTrustForwardedIPForAPIKeyACL(true)
			handler, err := NewMainlandWebBlock(mainlandWebBlockTestConfig(t))
			require.NoError(t, err)
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(tc.trusted))
			r.Use(SessionBindingContext(cfg), handler)
			r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "original page") })
			w := mainlandWebBlockTestRequest(r, http.MethodGet, "/login", tc.remote, tc.headers)
			require.Equal(t, tc.want, w.Code)
		})
	}
}

func TestMainlandWebBlockRejectsBadEnabledDataAndLoadsOnce(t *testing.T) {
	missing := config.MainlandWebBlockConfig{Enabled: true, CIDRFile: filepath.Join(t.TempDir(), "missing.zone")}
	handler, err := NewMainlandWebBlock(missing)
	require.ErrorContains(t, err, "load mainland web block CIDRs")
	require.Nil(t, handler)
	for _, content := range []string{"invalid CIDR\n", "203.0.113.0/24\n"} {
		path := filepath.Join(t.TempDir(), "invalid.zone")
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
		handler, err := NewMainlandWebBlock(config.MainlandWebBlockConfig{Enabled: true, CIDRFile: path})
		require.Error(t, err)
		require.Nil(t, handler)
	}
	cfg := mainlandWebBlockTestConfig(t)
	r := mainlandWebBlockTestRouter(t, cfg, func(c *gin.Context) { c.Status(http.StatusOK) })
	require.NoError(t, os.Remove(cfg.CIDRFile))
	w := mainlandWebBlockTestRequest(r, http.MethodGet, "/", "203.0.113.42:1234", nil)
	require.Equal(t, http.StatusForbidden, w.Code, "request handling must use the already loaded data")
}
