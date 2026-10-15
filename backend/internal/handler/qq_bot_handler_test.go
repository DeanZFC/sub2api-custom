package handler

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qqTestEncryptor struct{}

func (qqTestEncryptor) Encrypt(s string) (string, error) { return s, nil }
func (qqTestEncryptor) Decrypt(s string) (string, error) { return s, nil }

func TestQQBotBindingManagementRequiresAdmin(t *testing.T) {
	for _, role := range []string{"", "user"} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
		r := gin.New()
		r.Use(func(c *gin.Context) {
			if role != "" {
				c.Set(string(middleware.ContextKeyUserRole), role)
			}
			c.Next()
		})
		r.GET("/bindings", h.ListBindings)
		r.DELETE("/bindings/:user_id", h.Unbind)
		for _, method := range []string{"GET", "DELETE"} {
			path := "/bindings"
			if method == "DELETE" {
				path += "/7"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			require.Equal(t, 403, w.Code)
		}
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
}

func TestQQBotAdminUnbindStaleIdentityReturnsConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("app", 273).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("app", 272).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("DELETE FROM qq_bot_bindings").WillReturnRows(sqlmock.NewRows([]string{"member_id"}))
	mock.ExpectRollback()
	h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), "admin"); c.Next() })
	r.DELETE("/bindings/:user_id", h.Unbind)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/bindings/7", strings.NewReader(`{"app_id":"app","member_id":"member","created_at":"2026-10-15T00:00:00Z"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 409, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQQBotWebhookRejectsUnsignedAndAlteredEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, app string
		valid     bool
		alter     bool
		want      int
	}{
		{"unsigned", "app", false, false, 401}, {"wrong app", "wrong", true, false, 401}, {"altered", "app", true, true, 401}, {"valid ignored event", "app", true, false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			cfg := qqbot.DefaultConfig()
			cfg.AppID = "app"
			cfg.AppSecret = "test-secret"
			rawConfig, err := json.Marshal(cfg)
			require.NoError(t, err)
			mock.ExpectQuery("SELECT value FROM settings").WithArgs("qq_bot_config").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(rawConfig)))
			h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
			r := gin.New()
			r.POST("/hook", h.Webhook)
			raw := []byte(`{"op":0,"t":"UNUSED","d":{}}`)
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			seed := "test-secret"
			for len(seed) < 32 {
				seed += seed
			}
			key := ed25519.NewKeyFromSeed([]byte(seed[:32]))
			sig := hex.EncodeToString(ed25519.Sign(key, append([]byte(ts), raw...)))
			if tc.alter {
				raw = append(raw, ' ')
			}
			req := httptest.NewRequest("POST", "/hook", bytes.NewReader(raw))
			req.Header.Set("X-Bot-Appid", tc.app)
			if tc.valid {
				req.Header.Set("X-Signature-Timestamp", ts)
				req.Header.Set("X-Signature-Ed25519", sig)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestQQBotWebhookChallengeAndBodyLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cfg := qqbot.DefaultConfig()
	cfg.AppID = "app"
	cfg.AppSecret = "test-secret"
	raw, _ := json.Marshal(cfg)
	mock.ExpectQuery("SELECT value FROM settings").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
	h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
	r := gin.New()
	r.POST("/hook", h.Webhook)
	req := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"op":13,"d":{"plain_token":"challenge","event_ts":"123"}}`))
	req.Header.Set("X-Bot-Appid", "app")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "signature")
	require.NotContains(t, w.Body.String(), "test-secret")
	req = httptest.NewRequest("POST", "/hook", strings.NewReader(strings.Repeat("x", (2<<20)+1)))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQQBotConfigDoesNotReturnSecrets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cfg := qqbot.DefaultConfig()
	cfg.APIKey = "secret-key"
	cfg.AppSecret = "qq-secret"
	raw, _ := json.Marshal(cfg)
	mock.ExpectQuery("SELECT value FROM settings").WithArgs("qq_bot_config").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
	bot := qqbot.New(db, qqTestEncryptor{}, true, nil)
	h := NewQQBotHandler(bot)
	r := gin.New()
	r.GET("/config", h.GetConfig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/config", nil))
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "secret-key")
	require.NotContains(t, w.Body.String(), "qq-secret")
	require.Contains(t, w.Body.String(), `"api_key_set":true`)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQQBotEmailBindingPreviewWithoutLoginAndNoTokenEcho(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cfg := qqbot.DefaultConfig()
	cfg.Enabled = true
	cfg.AppID = "app"
	cfg.AllowedGroups = []string{"group"}
	raw, _ := json.Marshal(cfg)
	mock.ExpectQuery("SELECT value FROM settings").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
	mock.ExpectQuery("SELECT r.member_id,r.group_id,r.nickname,r.expires_at").WillReturnRows(sqlmock.NewRows([]string{"member_id", "group_id", "nickname", "expires_at"}).AddRow("member", "group", "Alice", time.Now().Add(10*time.Minute)))
	h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
	r := gin.New()
	r.POST("/preview", h.PreviewEmailBinding)
	r.POST("/confirm", h.ConfirmEmailBinding)
	token := strings.Repeat("a", 48)
	body, err := json.Marshal(map[string]string{"token": token})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Body.String(), "Alice")
	require.NotContains(t, w.Body.String(), token)
	// A missing identity cannot be confirmed, even with a syntactically valid token.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/confirm", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
