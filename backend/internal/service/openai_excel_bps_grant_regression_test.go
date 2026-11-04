package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestExcelBPS403RecoveryUsesRefreshedGrantForRestore(t *testing.T) {
	for _, scenario := range []string{"refreshed grant", "reauthorized during probe"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			reauth, reader, grantRepo, updater, _ := newExcelReauthTestService(t)
			account := reader.account
			account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 10
			account.Extra = cloneExcelBPSRecoveryExtra(recoveryTestAccount(now).Extra)
			primaryBefore := cloneReauthMap(account.Credentials)
			stale := excelTestCredentials("expired-recovery", now.Add(-time.Hour))
			storeExcelTestCredentials(t, reauth, grantRepo, stale)
			staleCiphertext := grantRepo.ciphertext
			fresh := excelTestCredentials("refreshed-recovery", now.Add(time.Hour))
			client := &excelRefreshTestClient{credentials: fresh}
			reauth.oauth.oauthClient = client
			nonceUpstream := &bpsProbeUpstream{}
			var authenticatedGrant string
			upstream := &bpsTestUpstream{send: func(req *http.Request, proxy string) (*http.Response, error) {
				require.Equal(t, "Bearer "+reauthMapString(fresh, "access_token"), req.Header.Get("Authorization"))
				require.Equal(t, "acct-1", req.Header.Get("Chatgpt-Account-Id"))
				authenticatedGrant = grantRepo.ciphertext
				require.NotEqual(t, staleCiphertext, authenticatedGrant)
				if scenario == "reauthorized during probe" {
					storeExcelTestCredentials(t, reauth, grantRepo, excelTestCredentials("concurrent-login", now.Add(2*time.Hour)))
				}
				return nonceUpstream.Do(req, proxy, account.ID, account.Concurrency)
			}}
			repo := &recoveryTestRepo{claim: true}
			gateway := &OpenAIGatewayService{httpUpstream: upstream, accountRepo: repo, excelOAuthReauth: reauth}
			gateway.recoverExcelBPS403Account(t.Context(), account, now)
			require.Equal(t, 1, client.calls)
			require.Equal(t, openai.ExcelClientID, client.clientID)
			require.Equal(t, stale["refresh_token"], client.refreshToken)
			require.Equal(t, "not_expired", grantRepo.diagnosis.Status)
			require.Len(t, nonceUpstream.bodies, 1)
			require.Equal(t, 1, repo.claims)
			require.Equal(t, 1, repo.restores)
			require.Equal(t, []string{authenticatedGrant}, repo.observedGrants,
				"restore must compare the grant used by the successful probe")
			if scenario == "reauthorized during probe" {
				require.NotEqual(t, grantRepo.ciphertext, repo.observedGrants[0],
					"the repository must receive the old observed grant so its CAS rejects a concurrent login")
			}
			require.Equal(t, primaryBefore, account.Credentials)
			require.False(t, updater.applied)
			require.False(t, account.IsExcelBPSEnabled())
			require.NotContains(t, account.Extra, ExcelBPS403LastProbeAtKey)
		})
	}
}

func TestExcelBPSProbeAndDiscovery401RejectOnlyTheirActualGrant(t *testing.T) {
	for _, endpoint := range []string{"403 recovery", "model discovery"} {
		for _, scenario := range []string{"current grant", "reauthorized before 401"} {
			t.Run(endpoint+"/"+scenario, func(t *testing.T) {
				reauth, reader, grantRepo, updater, _ := newExcelReauthTestService(t)
				account := reader.account
				account.Extra = cloneExcelBPSRecoveryExtra(recoveryTestAccount(time.Now()).Extra)
				account.Extra["openai_excel_bps"] = endpoint == "model discovery"
				primaryBefore := cloneReauthMap(account.Credentials)
				credentials := excelTestCredentials("request-grant", time.Now().Add(time.Hour))
				storeExcelTestCredentials(t, reauth, grantRepo, credentials)
				fresh := excelTestCredentials("new-login", time.Now().Add(2*time.Hour))
				gateway := openAIClientToolsTestService(nil)
				authRepo, invalidator := setupExcelBPSAuth(gateway)
				gateway.excelOAuthReauth = reauth
				calls := 0
				var replacementCiphertext string
				gateway.httpUpstream = &bpsTestUpstream{send: func(req *http.Request, _ string) (*http.Response, error) {
					calls++
					require.Equal(t, "Bearer "+reauthMapString(credentials, "access_token"), req.Header.Get("Authorization"))
					if scenario == "reauthorized before 401" {
						storeExcelTestCredentials(t, reauth, grantRepo, fresh)
						replacementCiphertext = grantRepo.ciphertext
					}
					return &http.Response{StatusCode: http.StatusUnauthorized,
						Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"token_revoked"}}`))}, nil
				}}
				var err error
				if endpoint == "403 recovery" {
					err = gateway.probeExcelBPS403Recovery(t.Context(), account)
				} else {
					_, err = gateway.FetchExcelBPSModelsList(t.Context(), account)
				}
				require.Error(t, err)
				require.Equal(t, 1, calls)
				plain, decryptErr := reauth.encryptor.Decrypt(grantRepo.ciphertext)
				require.NoError(t, decryptErr)
				var saved map[string]any
				require.NoError(t, json.Unmarshal([]byte(plain), &saved))
				if scenario == "current grant" {
					require.Equal(t, credentials["access_token"], saved["access_token"])
					require.Equal(t, credentials["refresh_token"], saved["refresh_token"])
					require.Equal(t, true, saved[excelAccessTokenRejectedKey])
					require.Equal(t, "auth_failed", grantRepo.diagnosis.Status)
				} else {
					require.Equal(t, replacementCiphertext, grantRepo.ciphertext)
					require.Equal(t, fresh["access_token"], saved["access_token"])
					require.Equal(t, fresh["refresh_token"], saved["refresh_token"])
					require.NotContains(t, saved, excelAccessTokenRejectedKey)
					require.Empty(t, grantRepo.diagnosis.Status)
				}
				require.Equal(t, primaryBefore, account.Credentials)
				require.False(t, updater.applied)
				require.Zero(t, authRepo.errorCalls+authRepo.tempCalls)
				require.Empty(t, invalidator.ids)
				require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
			})
		}
	}
}
