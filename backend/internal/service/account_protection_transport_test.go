package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func protectionTransportAccount() *Account {
	a := protectedPoolKeyTestAccount(AntiDegradeModeTLSNode24, "nodejs24")
	a.Concurrency = 3
	a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["max_concurrency"] = 8
	a.Extra[codexFingerprintModeExtraKey] = "device"
	a.Extra[codexFingerprintSeedExtraKey] = testCodexFingerprintSeed
	a.Extra["enable_tls_fingerprint"] = true
	return a
}

func TestProtectionHTTPTransportKeepsPluginPolicyAndTLSKillSwitch(t *testing.T) {
	for _, testPath := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			protected bool
			tls       bool
			plugin    bool
		}{
			{"protected direct", true, true, false},
			{"protected plugin", true, true, true},
			{"TLS disabled", true, false, false},
			{"unprotected direct", false, true, false},
			{"unprotected plugin", false, true, true},
		} {
			path := "gateway/"
			if testPath {
				path = "account-test/"
			}
			t.Run(path+tc.name, func(t *testing.T) {
				a := protectionTransportAccount()
				a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["enabled"] = tc.protected
				cfg := &config.Config{}
				cfg.Gateway.TLSFingerprint.Enabled = tc.tls
				manager := &PluginManager{}
				if tc.plugin {
					manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "test unavailable"})
				}
				upstream := &pluginRoutingHTTPUpstream{}
				req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
				require.NoError(t, err)
				var resp *http.Response
				if testPath {
					svc := &AccountTestService{cfg: cfg, httpUpstream: upstream, pluginManager: manager}
					resp, err = svc.doOpenAIAccountTestUpstream(req, "", a, false)
				} else {
					svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, pluginManager: manager}
					resp, err = svc.doOpenAIUpstream(req, "", a)
				}
				if tc.plugin {
					require.ErrorContains(t, err, "插件不可用")
					require.Nil(t, resp)
					require.Zero(t, upstream.doCalls, "a selected plugin must fail closed")
					require.Zero(t, upstream.doWithTLSCalls)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, resp)
				_ = resp.Body.Close()
				require.Equal(t, 1, upstream.doCalls)
				expectTLS := 0
				if tc.protected && tc.tls {
					expectTLS = 1
				}
				require.Equal(t, expectTLS, upstream.doWithTLSCalls)
			})
		}
	}
}

type protectionTLSCaptureDialer struct {
	profiles []*tlsfingerprint.Profile
}

func (d *protectionTLSCaptureDialer) Dial(ctx context.Context, _ string, _ http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	d.profiles = append(d.profiles, openAIWSTLSProfileFromContext(ctx))
	return &openAIWSFakeConn{}, http.StatusSwitchingProtocols, nil, nil
}

func TestProtectionWSConnectionReuseTracksActualTransport(t *testing.T) {
	for _, change := range []string{"url", "proxy", "tls-kill-switch", "conversation"} {
		t.Run(change, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.TLSFingerprint.Enabled = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			dialer := &protectionTLSCaptureDialer{}
			pool.setClientDialerForTest(dialer)
			req := openAIWSAcquireRequest{
				Account: protectionTransportAccount(),
				WSURL:   "wss://example.com/v1/responses",
				Headers: http.Header{"Conversation_id": []string{"first"}},
			}
			first, err := pool.Acquire(context.Background(), req)
			require.NoError(t, err)
			firstID := first.ConnID()
			first.Release()
			reused, err := pool.Acquire(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, firstID, reused.ConnID())
			reused.Release()
			switch change {
			case "url":
				req.WSURL = "wss://other.example.com/v1/responses"
			case "proxy":
				req.ProxyURL = "http://127.0.0.1:1234"
			case "tls-kill-switch":
				cfg.Gateway.TLSFingerprint.Enabled = false
			case "conversation":
				req.Headers.Set("conversation_id", "second")
			}
			second, err := pool.Acquire(context.Background(), req)
			require.NoError(t, err)
			defer second.Release()
			require.NotEqual(t, firstID, second.ConnID())
			require.Len(t, dialer.profiles, 2)
			require.NotNil(t, dialer.profiles[0])
			if change == "tls-kill-switch" {
				require.Nil(t, dialer.profiles[1])
			}
		})
	}
}

func TestProtectionWSCompatibilityUsesProfileContents(t *testing.T) {
	req := openAIWSAcquireRequest{Account: protectionTransportAccount(), WSURL: "wss://example.com/v1/responses"}
	req.protectionTLSProfile = &tlsfingerprint.Profile{Name: "same-name", CipherSuites: []uint16{0x1301}}
	first := openAIWSAcquireCompatibility(req)
	req.protectionTLSProfile = &tlsfingerprint.Profile{Name: "same-name", CipherSuites: []uint16{0x1302}}
	require.NotEqual(t, first, openAIWSAcquireCompatibility(req))

	req.Account.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["enabled"] = false
	first = openAIWSAcquireCompatibility(req)
	req.WSURL = "wss://other.example.com/v1/responses"
	req.protectionTLSProfile = nil
	req.Headers = http.Header{"Conversation_id": []string{"second"}}
	require.Equal(t, first, openAIWSAcquireCompatibility(req), "protection-only keys are inert when disabled")
}

func TestProtectionWSRejectsMalformedStrategyBeforeReusingConnection(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	pool := newOpenAIWSConnPool(cfg)
	defer pool.Close()
	pool.setClientDialerForTest(&openAIWSFakeDialer{})
	req := openAIWSAcquireRequest{Account: protectionTransportAccount(), WSURL: "wss://example.com/v1/responses"}
	lease, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	lease.Release()
	req.Account.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["max_concurrency"] = 0
	lease, err = pool.Acquire(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, lease)
}

func TestProtectionWSHTTPTransportDoesNotReuseChangedTLSProfile(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer().(*coderOpenAIWSClientDialer)
	firstProfile := &tlsfingerprint.Profile{Name: "same-name", CipherSuites: []uint16{0x1301}}
	first, err := dialer.proxyHTTPClient("", firstProfile)
	require.NoError(t, err)
	same, err := dialer.proxyHTTPClient("", &tlsfingerprint.Profile{Name: "same-name", CipherSuites: []uint16{0x1301}})
	require.NoError(t, err)
	require.Same(t, first, same)
	changed, err := dialer.proxyHTTPClient("", &tlsfingerprint.Profile{Name: "same-name", CipherSuites: []uint16{0x1302}})
	require.NoError(t, err)
	require.NotSame(t, first, changed)
}
