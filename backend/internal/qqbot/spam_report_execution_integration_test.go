//go:build integration

package qqbot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpamReportUnexpectedSuccessfulHTTPBodyDoesNotAuthorizeMute(t *testing.T) {
	for _, body := range []string{`{"code":40062003}`, `{"code":0}`, `{"members":[{"success":false}]}`, "null", "not JSON"} {
		t.Run(body, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = fmt.Fprint(w, `{"access_token":"test-only","expires_in":7200}`)
					return
				}
				if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/messages/") {
					_, _ = fmt.Fprint(w, body)
					return
				}
				t.Errorf("unconfirmed recall must not authorize %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(server.Close)
			h.s.qqURL, h.s.tokenURL = server.URL, server.URL+"/token"
			id, source := h.stage(t, spamSourceTestEvent("reported"))
			require.True(t, h.confirm(t, id, source))
			h.s.runModeration(context.Background())
			h.s.runModeration(context.Background())
			require.Equal(t, map[string]string{"recall": "unconfirmed", "mute": "cancelled"}, h.operations(t))
		})
	}
}
