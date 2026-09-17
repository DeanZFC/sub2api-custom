package prism

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAccountTokenBootstrapsIsolatedSession(t *testing.T) {
	token := "synthetic-access-one"
	providerCalls := 0
	c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		access, err := r.Cookie("prism_oai_access_token")
		if err != nil || access.Value != token || r.Header.Get("Authorization") != "" {
			t.Fatal("account credential was not sent exclusively as the Prism access cookie")
		}
		if r.URL.Path == "/auth/session" {
			if len(r.Cookies()) != 1 {
				t.Fatal("saved or previously generated session leaked into account bootstrap")
			}
			http.SetCookie(w, &http.Cookie{Name: "prism_session_token", Value: "issued-session", Path: "/", Secure: true})
			// The existing synthetic generation fixture uses this extra cookie.
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "rotated", Path: "/", Secure: true})
			fmt.Fprint(w, `{"user":{"app_metadata":{"user_id":"synthetic-openai-user"}}}`)
			return true
		}
		if session, err := r.Cookie("prism_session_token"); err != nil || session.Value != "issued-session" {
			t.Error("issued session was not used for subsequent Prism calls")
		}
		return false
	})
	c.opts.AccessTokenProvider = func(ctx context.Context) (string, error) {
		providerCalls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("token acquisition is outside generation timeout")
		}
		return token, nil
	}
	c.opts.ExpectedOpenAIUserID = "synthetic-openai-user"
	for _, nextToken := range []string{"synthetic-access-one", "synthetic-access-two"} {
		token = nextToken
		result, err := generate(c, context.Background())
		if err != nil || result.Text != "Synthetic answer" {
			t.Fatalf("account generation failed: %v", err)
		}
	}
	if providerCalls != 2 || f.startCount != 2 || len(f.projects) != 2 || f.projects[0] == f.projects[1] {
		t.Fatal("token refresh or isolated generation was skipped")
	}
}

func TestAccountAuthenticationFailureStopsBeforeProject(t *testing.T) {
	for _, tc := range []struct {
		name, token, body, expectedID, wantCode string
		providerError, sessionCookie            bool
	}{
		{name: "refresh failed", providerError: true, wantCode: "account_token_unavailable"},
		{name: "empty token", wantCode: "invalid_account_token"},
		{name: "cookie injection", token: "private; prism_session_token=other", wantCode: "invalid_account_token"},
		{name: "header injection", token: "private\r\nAuthorization: other", wantCode: "invalid_account_token"},
		{name: "anonymous", token: "private", body: `{"user":null}`, wantCode: "account_auth_rejected"},
		{name: "missing issued session", token: "private", body: `{"user":{"app_metadata":{"user_id":"user-one"}}}`, wantCode: "session_cookie_missing"},
		{name: "identity mismatch", token: "private", body: `{"user":{"app_metadata":{"user_id":"user-one"}}}`, expectedID: "user-two", sessionCookie: true, wantCode: "account_identity_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/auth/session" {
					t.Error("authentication failure reached project creation")
				}
				if tc.sessionCookie {
					http.SetCookie(w, &http.Cookie{Name: "prism_session_token", Value: "issued", Path: "/", Secure: true})
				}
				fmt.Fprint(w, tc.body)
				return true
			})
			c.opts.AccessTokenProvider = func(context.Context) (string, error) {
				if tc.providerError {
					return "", errors.New("private refresh response must not escape")
				}
				return tc.token, nil
			}
			c.opts.ExpectedOpenAIUserID = tc.expectedID
			_, err := generate(c, context.Background())
			var upstream *Error
			if !errors.As(err, &upstream) || upstream.Stage != "auth" || upstream.Code != tc.wantCode || upstream.Submitted {
				t.Fatalf("unexpected safe authentication error: %v", err)
			}
			if strings.Contains(err.Error(), "private") || errors.Unwrap(err) != nil || f.startCount != 0 || len(f.paths) > 1 {
				t.Fatal("authentication error leaked secrets or attempted generation")
			}
		})
	}
}

func TestAccountTokenAcquisitionHonorsTimeout(t *testing.T) {
	c, f := newFixture(t, nil)
	c.opts.Timeout = 10 * time.Millisecond
	c.opts.AccessTokenProvider = func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	_, err := generate(c, context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || len(f.paths) != 0 {
		t.Fatalf("timeout was not respected before authentication: %v", err)
	}
}
