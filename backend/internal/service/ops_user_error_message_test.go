package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserErrorMessageOverridePreservesAdminDiagnostics(t *testing.T) {
	ctx := WithUpstreamErrorMessage(context.Background(), "服务暂时不可用，请稍后重试")
	for _, tc := range []struct {
		name string
		log  OpsErrorLog
		mask bool
	}{
		{"provider", OpsErrorLog{Phase: "upstream"}, true},
		{"request scoped provider", OpsErrorLog{Phase: "request", Type: "invalid_request_error", HasUpstreamError: true}, true},
		{"provider cyber", OpsErrorLog{Phase: "request", Type: "cyber_policy", Source: "upstream_http"}, true},
		{"provider owner", OpsErrorLog{Phase: "request", Owner: "provider"}, true},
		{"local authentication", OpsErrorLog{Phase: "auth", Type: "authentication_error"}, false},
		{"local balance", OpsErrorLog{Phase: "request", Type: "billing_error"}, false},
		{"local subscription", OpsErrorLog{Phase: "request", Type: "subscription_error"}, false},
		{"local invalid request", OpsErrorLog{Phase: "request", Type: "invalid_request_error"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.log.Message = "original diagnostic"
			out := toUserErrorRequestForContext(ctx, &tc.log)
			if tc.mask {
				require.Equal(t, ConfiguredUpstreamErrorMessage(ctx), out.Message)
			} else {
				require.Equal(t, "original diagnostic", out.Message)
			}
			require.Equal(t, "original diagnostic", tc.log.Message)
			require.Equal(t, "original diagnostic", ToUserErrorRequest(&tc.log).Message)
		})
	}
}

func TestUserErrorDetailOverrideUsesUpstreamEvidenceForRequestErrors(t *testing.T) {
	owner, status := int64(7), 400
	stored := &OpsErrorLogDetail{
		OpsErrorLog:        OpsErrorLog{ID: 42, UserID: &owner, Phase: "request", Type: "invalid_request_error", Message: "provider validation detail"},
		UpstreamStatusCode: &status,
		ErrorBody:          `{"error":{"message":"provider validation detail"},"debug":"internal provider diagnostic"}`,
	}
	svc := &OpsService{opsRepo: &stubOpsRepoForUserErr{detailToReturn: stored}}
	ctx := WithUpstreamErrorMessage(context.Background(), "统一提示")
	out, err := svc.GetUserErrorRequestDetail(ctx, owner, stored.ID)
	require.NoError(t, err)
	require.Equal(t, "统一提示", out.Message)
	require.Empty(t, out.ErrorBody)
	require.Equal(t, 400, *out.UpstreamStatusCode)
	require.Contains(t, stored.ErrorBody, "internal provider diagnostic")
	require.Equal(t, "provider validation detail", stored.Message)
	plain, err := svc.GetUserErrorRequestDetail(context.Background(), owner, stored.ID)
	require.NoError(t, err)
	require.Equal(t, stored.ErrorBody, plain.ErrorBody)
	require.Equal(t, stored.Message, plain.Message)
}

func TestUserErrorOverridePreservesFinalLocalBillingAfterUpstreamRetry(t *testing.T) {
	ctx := WithUpstreamErrorMessage(context.Background(), "统一提示")
	for _, typ := range []string{"billing_error", "subscription_error"} {
		e := &OpsErrorLogDetail{
			OpsErrorLog:          OpsErrorLog{Phase: "upstream", Type: typ, HasUpstreamError: true, Message: "Insufficient balance", StatusCode: 402},
			UpstreamErrorMessage: "previous upstream failure",
			ErrorBody:            `{"error":{"message":"Insufficient balance"}}`,
		}
		require.Equal(t, "Insufficient balance", toUserErrorRequestForContext(ctx, &e.OpsErrorLog).Message)
		detail := toUserErrorRequestDetailForContext(ctx, e)
		require.Equal(t, "Insufficient balance", detail.Message)
		require.Equal(t, 402, detail.StatusCode)
		require.Equal(t, e.ErrorBody, detail.ErrorBody)
	}
}
