package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type excelBPSAccountTestRepo struct {
	AccountRepository
	account *Account
}

func (r *excelBPSAccountTestRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func excelBPSAccountTestResponse(model string) *http.Response {
	wire := `data: {"type":"response.output_text.delta","delta":"OK"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"resp_account_test","status":"completed","model":` + strconv.Quote(model) +
		`,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":20,"output_tokens":7,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}}` + "\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
}

func TestBuildExcelBPSAccountTestBodyUsesResponsesContract(t *testing.T) {
	raw, err := buildExcelBPSAccountTestBody("gpt-6-astra", "糖果题")
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(raw, "model").String())
	require.True(t, gjson.GetBytes(raw, "stream").Bool())
	require.False(t, gjson.GetBytes(raw, "store").Bool())
	require.Equal(t, "message", gjson.GetBytes(raw, "input.0.type").String())
	require.Equal(t, "糖果题", gjson.GetBytes(raw, "input.0.content.0.text").String())
	require.Equal(t, "medium", gjson.GetBytes(raw, "reasoning.effort").String())
	require.Equal(t, int64(scheduledTestCustomPromptMaxOutputTokens), gjson.GetBytes(raw, "max_output_tokens").Int())
}

func TestExcelBPSAccountTestUsesMappedModelScope(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: excelBPSAccountTestResponse("gpt-6-astra")}
	svc := &AccountTestService{openaiGatewayService: openAIClientToolsTestService(upstream)}
	account := excelAccount()
	account.Credentials["model_mapping"] = map[string]any{"public-model": "gpt-6-astra", "gpt-6-astra": "gpt-6-sol"}
	account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
	c.Request = c.Request.WithContext(withAccountTestReasoningEffort(withAccountTestMaxOutputTokens(c.Request.Context(), 512), "high"))

	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "public-model", "Reply OK", AccountTestModeDefault))
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "bps.openai.com", upstream.lastReq.URL.Host)
	require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String(), "mapping must be applied once")
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.Empty(t, c.Request.Header.Get("Session-Id"), "the inbound request must remain unchanged")
}

func TestExcelBPSAccountTestLeavesOutOfScopeModelNative(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: excelBPSAccountTestResponse("gpt-6-sol")}
	svc := &AccountTestService{httpUpstream: upstream}
	account := excelAccount()
	account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)

	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "gpt-6-sol", "Reply OK", AccountTestModeDefault))
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "chatgpt.com", upstream.lastReq.URL.Host)
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
}

func TestExcelBPSAccountTestMissingGatewayFailsWithoutNativeFallback(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	svc := &AccountTestService{httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)

	require.ErrorContains(t, svc.testOpenAIAccountConnection(c, excelAccount(), "gpt-6-astra", "Reply OK", AccountTestModeDefault), "gateway is unavailable")
	require.Nil(t, upstream.lastReq)
}

func TestExcelBPSAccountTestSuppliesProxySessionIdentity(t *testing.T) {
	for _, nilHeader := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "background"}[nilHeader], func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			svc := &AccountTestService{openaiGatewayService: openAIClientToolsTestService(upstream)}
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = true
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if nilHeader {
				c.Request = &http.Request{}
			} else {
				c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
			}
			err := svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "Reply OK", AccountTestModeDefault)
			require.ErrorContains(t, err, "basispoints_proxy_unavailable")
			require.NotContains(t, err.Error(), "basispoints_session_required")
			require.Empty(t, c.Request.Header.Get("Session-Id"))
			if nilHeader {
				require.Nil(t, c.Request.Header)
			}
			require.Nil(t, upstream.lastReq)
		})
	}
}

func TestExcelBPSAccountTestBackgroundUsesGatewayAndCollectsUsage(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: excelBPSAccountTestResponse("gpt-6-sol")}
	account := excelAccount()
	account.Credentials["model_mapping"] = map[string]any{"public-model": "gpt-6-astra"}
	account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
	admissions := 0
	quality := &qualityOperationsStub{reserve: func(_ context.Context, id, tokens int64, _ bool, session string) (*QualityProbeReservation, error) {
		admissions++
		require.Equal(t, account.ID, id)
		require.Positive(t, tokens)
		require.NotEmpty(t, session)
		return &QualityProbeReservation{ID: 1, AccountID: id}, nil
	}}
	svc := &AccountTestService{accountRepo: &excelBPSAccountTestRepo{account: account}, openaiGatewayService: openAIClientToolsTestService(upstream), qualityOperations: quality}
	result, err := svc.RunTestBackgroundWithPromptAndReasoning(withAccountTestMaxOutputTokens(context.Background(), 512), account.ID, "public-model", "Reply OK", "high")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "OK", result.ResponseText)
	require.Equal(t, "gpt-6-astra", result.UpstreamModel)
	require.Equal(t, []string{"gpt-6-sol"}, result.ReturnedModels, "upstream model evidence must not be replaced by the requested alias")
	require.False(t, result.ModelEvidenceInvalid)
	require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
	require.Len(t, result.ExecutionInfo.Attempts, 1)
	require.EqualValues(t, 20, *result.ExecutionInfo.InputTokens)
	require.EqualValues(t, 7, *result.ExecutionInfo.OutputTokens)
	require.EqualValues(t, 3, *result.ExecutionInfo.ReasoningTokens)
	require.EqualValues(t, 4, *result.ExecutionInfo.CacheReadTokens)
	require.Equal(t, 1, admissions)
	require.Len(t, quality.outcomes, 1)
	require.EqualValues(t, 27, *quality.outcomes[0].Tokens)
}

func TestExcelBPSAccountTestUsesCanonicalIdentityForCodexCLIOnly(t *testing.T) {
	for _, nilHeader := range []bool{false, true} {
		t.Run(map[bool]string{false: "browser", true: "background"}[nilHeader], func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: excelBPSAccountTestResponse("gpt-6-astra")}
			account := excelAccount()
			account.Extra["codex_cli_only"] = true
			svc := &AccountTestService{openaiGatewayService: openAIClientToolsTestService(upstream)}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if nilHeader {
				c.Request = &http.Request{}
			} else {
				c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
				c.Request.Header.Set("User-Agent", "Mozilla/5.0")
			}

			require.NoError(t, svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "Reply OK", AccountTestModeDefault))
			require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
			if nilHeader {
				require.Nil(t, c.Request.Header)
			} else {
				require.Equal(t, "Mozilla/5.0", c.Request.UserAgent())
				require.Empty(t, c.Request.Header.Get("Originator"))
			}
		})
	}
}

func TestExcelBPSAccountTestRetainsVMRoutePriority(t *testing.T) {
	vmRequests := 0
	vm, repo, _ := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
		vmRequests++
		require.Equal(t, "/v1/accounts/300/responses", r.URL.Path)
		resp := excelBPSAccountTestResponse("gpt-6-astra")
		defer func() { _ = resp.Body.Close() }()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.Copy(w, resp.Body)
	})
	repo.b = &OpenAIVMBinding{AccountID: 300, Status: "ready"}
	account := excelAccount()
	account.Extra["openai_excel_bps_mihomo"] = true
	upstream := &httpUpstreamRecorder{}
	gateway := openAIClientToolsTestService(upstream)
	gateway.openaiVM = vm
	gateway.accountRepo = &excelBPSAccountTestRepo{account: account}
	svc := &AccountTestService{openaiGatewayService: gateway}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)

	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "Reply OK", AccountTestModeDefault))
	require.Equal(t, 1, vmRequests)
	require.Nil(t, upstream.lastReq)
}

func TestExcelBPSAccountTestGlobalSwitchAndPrimaryGrant(t *testing.T) {
	for _, primary := range []bool{false, true} {
		t.Run(strconv.FormatBool(primary), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: excelBPSAccountTestResponse("gpt-6-astra")}
			gateway := openAIClientToolsTestService(upstream)
			gateway.settingService = &SettingService{settingRepo: &excelBPSDisabledSettingsRepo{}}
			svc := &AccountTestService{openaiGatewayService: gateway, httpUpstream: upstream}
			account := excelAccount()
			if primary {
				account.Credentials["client_id"] = "app_fnr0pYvVwwFDocDumLG3H2Bp"
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/admin/test", nil)
			err := svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "OK", AccountTestModeDefault)
			if primary {
				require.ErrorContains(t, err, "native Codex is unavailable")
				require.Nil(t, upstream.lastReq)
			} else {
				require.NoError(t, err)
				require.Equal(t, "chatgpt.com", upstream.lastReq.URL.Host)
			}
		})
	}
}

type excelBPSDisabledSettingsRepo struct{ SettingRepository }

func (*excelBPSDisabledSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{SettingKeyExcelBPSEnabled: "false"}, nil
}

func (*excelBPSDisabledSettingsRepo) GetValue(context.Context, string) (string, error) {
	return "false", nil
}
