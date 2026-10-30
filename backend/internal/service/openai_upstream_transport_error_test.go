//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestClassifyUpstreamTransportError pins which transport-level upstream failures
// are "persistent" (retrying the same proxy/account is pointless — evict + alert)
// versus "transient" (a blip — fail over to a healthy account but do not evict).
//
// The motivating incident: a SOCKS5 proxy whose credentials expired returned
// `username/password authentication failed`, yet the account kept being scheduled.
func TestClassifyUpstreamTransportError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		persistent bool
	}{
		// Durable — config/credential/routing problems. Retrying same proxy won't help.
		{"socks5 proxy credential rejected", errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": socks connect tcp 85.255.176.68:12324->chatgpt.com:443: username/password authentication failed`), true},
		{"proxy connection refused", errors.New(`proxyconnect tcp: dial tcp 1.2.3.4:1080: connect: connection refused`), true},
		{"no route to host", errors.New(`dial tcp 1.2.3.4:443: connect: no route to host`), true},
		{"dns resolution failure", errors.New(`dial tcp: lookup proxy.example.com: no such host`), true},
		{"network unreachable", errors.New(`dial tcp 1.2.3.4:443: connect: network is unreachable`), true},

		// Transient — a temporary blip. Fail over, but do NOT evict the account.
		{"client timeout", errors.New(`Post "https://chatgpt.com/...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), false},
		{"i/o timeout", errors.New(`dial tcp 1.2.3.4:443: i/o timeout`), false},
		{"connection reset by peer", errors.New(`read tcp 10.0.0.1:5->2.2.2.2:443: read: connection reset by peer`), false},
		{"unexpected eof", errors.New(`unexpected EOF`), false},
		{"broken pipe", errors.New(`write tcp 10.0.0.1:5->2.2.2.2:443: write: broken pipe`), false},

		{"nil error", nil, false},

		// ── Typed-error cases ──────────────────────────────────────────────
		// ECONNREFUSED wrapped in the canonical net.OpError shape Go produces.
		{
			"ECONNREFUSED via net.OpError",
			&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
			},
			true,
		},
		// Bare syscall error (errors.Is traverses the chain).
		{"ECONNREFUSED bare", syscall.ECONNREFUSED, true},
		{"EHOSTUNREACH bare", syscall.EHOSTUNREACH, true},
		{"ENETUNREACH bare", syscall.ENETUNREACH, true},

		// *net.DNSError with IsNotFound — permanent DNS lookup failure.
		{
			"DNS not found (IsNotFound=true)",
			&net.DNSError{Err: "no such host", Name: "proxy.example.com", IsNotFound: true},
			true,
		},
		// *net.DNSError with IsNotFound=false — transient DNS timeout (not persistent).
		{
			"DNS timeout (IsNotFound=false)",
			&net.DNSError{Err: "i/o timeout", Name: "proxy.example.com", IsTimeout: true},
			false,
		},

		// context.Canceled — client gone; NOT classified as persistent.
		{"context.Canceled", context.Canceled, false},
		// context.DeadlineExceeded — slow upstream; NOT persistent.
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyUpstreamTransportError(tc.err).Persistent
			if got != tc.persistent {
				t.Fatalf("classifyUpstreamTransportError(%q).Persistent = %v, want %v", errString(tc.err), got, tc.persistent)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// Exercise the real transport-error constructors rather than manufacturing a
// 502: only an attempted account's failure should permit the next strict tier.
func TestStrictFailoverTransportErrorsRequireWholeTierFailure(t *testing.T) {
	for _, handler := range strictTransportErrorTestHandlers() {
		for _, transportErr := range []error{io.EOF, context.DeadlineExceeded} {
			t.Run(handler.name+"/"+transportErr.Error(), func(t *testing.T) {
				g, repo := strictFixture()
				ctx := strictContext(g)
				excluded := map[int64]struct{}{}
				selected, err := prepareStrictFailover(ctx, g, repo, excluded, "gpt-5.1", strictNoExtraGate)
				require.NoError(t, err)
				require.Equal(t, 1, strictFailoverStateFrom(selected).tier)

				for i, accountID := range []int64{1, 2} {
					account := &repo.accounts[i]
					c := newTransportErrorTestGin(t)
					c.Request = c.Request.WithContext(ctx)
					failure := handler.handle(ctx, c, account, transportErr)
					var failoverErr *UpstreamFailoverError
					require.ErrorAs(t, failure, &failoverErr)
					require.Zero(t, failoverErr.ObservedUpstreamStatus, "transport failure has no HTTP response")
					RecordStrictFailoverFailure(ctx, accountID, failoverErr)
					require.NotEmpty(t, strictFailureReason(ctx, accountID))
					excluded[accountID] = struct{}{}
					selected, err = prepareStrictFailover(ctx, g, repo, excluded, "gpt-5.1", strictNoExtraGate)
					require.NoError(t, err)
					if i == 0 {
						require.Equal(t, 1, strictFailoverStateFrom(selected).tier, "the healthy primary peer must be tried first")
						require.True(t, strictFailoverAllows(selected, 2))
						require.False(t, strictFailoverAllows(selected, 3))
					} else {
						require.Equal(t, 2, strictFailoverStateFrom(selected).tier)
						require.True(t, strictFailoverAllows(selected, 3))
					}
					require.False(t, c.Writer.Written(), "transport helper must leave replay to the handler")
				}
			})
		}
	}
}

func TestStrictFailoverTransportErrorsPreserveRequestCancellation(t *testing.T) {
	for _, handler := range strictTransportErrorTestHandlers() {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deadline=%t", handler.name, deadline), func(t *testing.T) {
				ctx := withStrictFailoverFailures(context.Background())
				var cancel context.CancelFunc
				if deadline {
					ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				} else {
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				defer cancel()
				c := newTransportErrorTestGin(t)
				c.Request = c.Request.WithContext(ctx)
				account := &Account{ID: 1, Platform: PlatformOpenAI}
				failure := handler.handle(ctx, c, account, fmt.Errorf("round trip: %w", ctx.Err()))
				require.ErrorIs(t, failure, ctx.Err())
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(failure, &failoverErr), "an ended request must not trigger replay")
				RecordStrictFailoverFailure(ctx, account.ID, failoverErr)
				require.Empty(t, strictFailureReason(ctx, account.ID))
				require.False(t, c.Writer.Written())
			})
		}
	}
}

func TestStrictFailoverOpenAITransportErrorPreservesPluginRequestSent(t *testing.T) {
	ctx := withStrictFailoverFailures(context.Background())
	c := newTransportErrorTestGin(t)
	c.Request = c.Request.WithContext(ctx)
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	upstreamErr := &PluginTransportError{Code: "UPSTREAM_EOF", Message: "connection lost", RequestSent: true}
	svc := &OpenAIGatewayService{}
	failure := svc.handleOpenAIUpstreamTransportError(ctx, c, account, fmt.Errorf("plugin: %w", upstreamErr), false)
	require.ErrorIs(t, failure, upstreamErr)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(failure, &failoverErr), "a plugin request already sent must not be replayed")
	RecordStrictFailoverFailure(ctx, account.ID, failoverErr)
	require.Empty(t, strictFailureReason(ctx, account.ID))
	require.False(t, c.Writer.Written())
}

func strictTransportErrorTestHandlers() []struct {
	name   string
	handle func(context.Context, *gin.Context, *Account, error) error
} {
	return []struct {
		name   string
		handle func(context.Context, *gin.Context, *Account, error) error
	}{
		{"openai", func(ctx context.Context, c *gin.Context, account *Account, err error) error {
			return (&OpenAIGatewayService{}).handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
		}},
		{"anthropic", func(ctx context.Context, c *gin.Context, account *Account, err error) error {
			return (&GatewayService{}).handleUpstreamTransportError(ctx, c, account, err, OpsUpstreamErrorEvent{})
		}},
		{"gemini", func(ctx context.Context, c *gin.Context, account *Account, err error) error {
			return (&GeminiMessagesCompatService{}).handleUpstreamTransportError(ctx, c, account, err)
		}},
	}
}
