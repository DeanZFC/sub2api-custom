package middleware

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/mainlandip"
	"github.com/gin-gonic/gin"
)

//go:embed mainland_access_denied.html
var mainlandAccessDeniedHTML []byte

// NewMainlandWebBlock restricts only the three explicitly selected document paths.
// Country data is loaded once when enabled; requests never make external lookups.
func NewMainlandWebBlock(cfg config.MainlandWebBlockConfig, runtimeEnabled ...func(context.Context) bool) (gin.HandlerFunc, error) {
	var enabled func(context.Context) bool
	if len(runtimeEnabled) > 0 {
		enabled = runtimeEnabled[0]
	}
	if !cfg.Enabled && enabled == nil {
		return func(c *gin.Context) { c.Next() }, nil
	}
	data := &mainlandCountryData{path: strings.TrimSpace(cfg.CIDRFile)}
	initialEnabled := cfg.Enabled
	if enabled != nil {
		initialEnabled = enabled(context.Background())
	}
	if initialEnabled {
		if _, err := data.get(); err != nil {
			return nil, err
		}
	}
	return func(c *gin.Context) {
		switch c.Request.URL.Path {
		case "/", "/login", "/home":
		default:
			c.Next()
			return
		}
		if enabled != nil && !enabled(c.Request.Context()) {
			c.Next()
			return
		}

		// Both allowed and denied documents depend on the visitor's IP. Prevent
		// downstream SPA handlers from replacing no-store with their HTML cache policy.
		writer := &mainlandDocumentWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		writer.preventCache()
		// Do not reuse a document cached before the restriction was enabled.
		c.Request.Header.Del("If-None-Match")
		c.Request.Header.Del("If-Modified-Since")
		set, err := data.get()
		if err != nil {
			// Data may disappear between the administrator's validation and the
			// first enabled request. Never silently bypass an enabled restriction.
			c.Header("Retry-After", "1")
			if c.Request.Method == http.MethodHead {
				c.AbortWithStatus(http.StatusServiceUnavailable)
				return
			}
			c.String(http.StatusServiceUnavailable, "Access temporarily unavailable. Please try again later.")
			c.Abort()
			return
		}
		// Always use the configured trusted proxy chain, independently of the
		// legacy raw-forwarded-header compatibility switch used by other features.
		addr, err := netip.ParseAddr(ip.GetTrustedClientIP(c))
		if err != nil || !set.Contains(addr) {
			c.Next()
			writer.preventCache()
			return
		}

		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Robots-Tag", "noindex, nofollow")
		c.Header("Content-Language", "zh-CN")
		if c.Request.Method == http.MethodHead {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Data(http.StatusForbidden, "text/html; charset=utf-8", mainlandAccessDeniedHTML)
		c.Abort()
	}, nil
}

// A disabled installation must remain operable even if its optional data file
// is missing. Load lazily on activation, retain a valid snapshot, and bound disk
// retries after failures without creating any per-visitor state.
type mainlandCountryData struct {
	path    string
	set     atomic.Pointer[mainlandip.Set]
	mu      sync.Mutex
	retryAt time.Time
	err     error
}

func (d *mainlandCountryData) get() (*mainlandip.Set, error) {
	if set := d.set.Load(); set != nil {
		return set, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if set := d.set.Load(); set != nil {
		return set, nil
	}
	if time.Now().Before(d.retryAt) {
		return nil, d.err
	}
	var (
		set *mainlandip.Set
		err error
	)
	if d.path != "" {
		set, err = mainlandip.Load(d.path)
	} else {
		set, err = mainlandip.Default()
	}
	if err != nil {
		d.err = fmt.Errorf("load mainland web block CIDRs: %w", err)
		d.retryAt = time.Now().Add(time.Second)
		return nil, d.err
	}
	d.set.Store(set)
	return set, nil
}

type mainlandDocumentWriter struct {
	gin.ResponseWriter
}

func (w *mainlandDocumentWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *mainlandDocumentWriter) preventCache() {
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	w.Header().Del("ETag")
	w.Header().Del("Last-Modified")
}

func (w *mainlandDocumentWriter) WriteHeader(code int) {
	w.preventCache()
	w.ResponseWriter.WriteHeader(code)
}

func (w *mainlandDocumentWriter) WriteHeaderNow() {
	w.preventCache()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *mainlandDocumentWriter) Write(data []byte) (int, error) {
	w.preventCache()
	return w.ResponseWriter.Write(data)
}

func (w *mainlandDocumentWriter) WriteString(data string) (int, error) {
	w.preventCache()
	return w.ResponseWriter.WriteString(data)
}

func (w *mainlandDocumentWriter) Flush() {
	w.preventCache()
	w.ResponseWriter.Flush()
}
