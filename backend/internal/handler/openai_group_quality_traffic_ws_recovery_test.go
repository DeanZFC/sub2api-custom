//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type handlerFallbackAdmission struct {
	ctx      context.Context
	request  service.GroupQualityTrafficRequest
	marker   service.GroupQualityFallbackRequest
	fallback bool
}
type handlerFallbackTrafficGate struct {
	handlerGroupTrafficGate
	stateMu     sync.Mutex
	normalReady bool
	normalErr   error
	admissions  []handlerFallbackAdmission
}

func (g *handlerFallbackTrafficGate) RefreshGroupQualityFallback(context.Context, string) (bool, error) {
	return true, nil
}
func (g *handlerFallbackTrafficGate) AcquireGroupQualityTraffic(ctx context.Context, aid int64, model, lease string) (bool, bool, error) {
	r, ok := service.GroupQualityTrafficRequestFromContext(ctx)
	if !ok {
		return false, false, errors.New("missing turn traffic context")
	}
	r.AccountID, r.Model, r.LeaseID = aid, model, lease
	marker, fallback := service.GroupQualityFallbackFromContext(ctx)
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	g.admissions = append(g.admissions, handlerFallbackAdmission{ctx: ctx, request: r, marker: marker, fallback: fallback})
	if fallback {
		allowed := !g.normalReady && marker.GroupID == r.GroupID && marker.AccountID == aid && marker.Model == model
		return allowed, allowed, nil
	}
	return g.normalReady, g.normalReady, g.normalErr
}
func (g *handlerFallbackTrafficGate) snapshot() []handlerFallbackAdmission {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	return append([]handlerFallbackAdmission(nil), g.admissions...)
}

type wsRecoveryProfitAccountRepo struct {
	groupTrafficAccountRepo
	mu          sync.Mutex
	failRead    bool
	reads       int
	failedReads int
}

func (r *wsRecoveryProfitAccountRepo) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.failRead {
		r.failedReads++
		return nil, errors.New("account storage unavailable after quality recovery")
	}
	return r.groupTrafficAccountRepo.GetByID(ctx, id)
}

func TestOpenAIGroupTrafficWSFallbackRecoversNormallyOrRefreshesModel(t *testing.T) {
	for _, tc := range []struct {
		name, nextModel string
		healthy         bool
	}{
		{"healthy owner exits fallback", "gpt-5.1", true},
		{"new public model gets fresh fallback authorization", "gpt-5.2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			gate := &handlerFallbackTrafficGate{}
			forwarded := make(chan string, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for turn := 1; turn <= 2; turn++ {
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					_, payload, err := conn.Read(ctx)
					if err != nil {
						cancel()
						return
					}
					forwarded <- string(payload)
					response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","model":%q,"output":[],"usage":{"input_tokens":100,"output_tokens":1}}}`, turn, gjson.GetBytes(payload, "model").String())
					_ = conn.Write(ctx, coderws.MessageText, []byte(response))
					cancel()
				}
			}))
			defer upstream.Close()
			var account service.Account
			h := newGroupTrafficHandler(t, gate, upstream.URL, nil, nil, func(a *service.Account) {
				a.GroupIDs = []int64{3131}
				a.Status = "quality_paused"
				a.Extra["quality_protection_reason"] = "automatic quality hold"
				rate := 0.08
				a.RateMultiplier = &rate
				account = *a
			})
			var profitRepo *wsRecoveryProfitAccountRepo
			if tc.healthy {
				profitRepo = &wsRecoveryProfitAccountRepo{groupTrafficAccountRepo: groupTrafficAccountRepo{openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}}
				h.gatewayService = service.NewOpenAIGatewayService(profitRepo, &openAIWSUsageHandlerUsageLogRepoStub{}, nil, nil, nil, nil, nil, h.cfg, nil, h.concurrencyHelper.concurrencyService, service.NewBillingService(h.cfg, nil), nil, h.billingCacheService, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			}
			conn, _ := startGroupTrafficWS(t, h, func(group *service.Group) {
				if tc.healthy {
					group.RateMultiplier, group.ProfitControlEnabled, group.ProfitMinMargin = 0.11, true, 0.2
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			for turn, model := range []string{"gpt-5.1", tc.nextModel} {
				if turn == 1 {
					gate.stateMu.Lock()
					gate.normalReady = tc.healthy
					gate.stateMu.Unlock()
					if profitRepo != nil {
						profitRepo.mu.Lock()
						require.Positive(t, profitRepo.reads, "first fallback must use the authoritative profit check")
						profitRepo.failRead = true
						profitRepo.mu.Unlock()
					}
				}
				previous := ""
				if turn == 1 {
					previous = `,"previous_response_id":"resp_1"`
				}
				body := fmt.Sprintf(`{"type":"response.create","model":%q,"input":"hello"%s}`, model, previous)
				require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(body)))
				_, payload, err := conn.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, "response.completed", gjson.GetBytes(payload, "type").String(), string(payload))
			}
			if profitRepo != nil {
				profitRepo.mu.Lock()
				require.Zero(t, profitRepo.failedReads, "ordinary recovery must not fail because of the connection's old fallback marker")
				profitRepo.mu.Unlock()
			}
			calls := gate.snapshot()
			wantCalls := 3
			if tc.healthy {
				wantCalls = 2
			}
			require.Len(t, calls, wantCalls)
			require.True(t, calls[0].fallback, "first turn has a real emergency reservation")
			require.False(t, calls[1].fallback, "later turn must try normal admission before recovery")
			require.NotEqual(t, calls[0].request.Key, calls[1].request.Key)
			require.Equal(t, tc.nextModel, calls[1].request.Model)
			last := calls[len(calls)-1]
			require.Equal(t, !tc.healthy, service.GroupQualityFallbackActive(last.ctx))
			if !tc.healthy {
				require.True(t, last.fallback)
				require.Equal(t, tc.nextModel, last.marker.Model)
				require.Equal(t, calls[1].request.Key, last.request.Key)
			}
			for _, call := range calls {
				require.EqualValues(t, 1, call.request.AccountID, "continuation remains on its established credential")
				require.EqualValues(t, 3131, call.request.GroupID)
			}
			<-forwarded
			second := <-forwarded
			require.Equal(t, "resp_1", gjson.Get(second, "previous_response_id").String())
			require.Equal(t, tc.nextModel, gjson.Get(second, "model").String())
		})
	}
}

func TestOpenAIGroupTrafficWSFallbackDoesNotBypassBusyOrInfrastructureErrors(t *testing.T) {
	storageErr := errors.New("budget storage unavailable")
	for _, tc := range []struct {
		name          string
		busy          bool
		gateErr       error
		wantGateCalls int
	}{
		{"ordinary concurrency busy", true, nil, 0},
		{"budget infrastructure error", false, storageErr, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := &handlerFallbackTrafficGate{normalErr: tc.gateErr}
			acquires := 0
			cache := &concurrencyCacheMock{acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { acquires++; return !tc.busy, nil }}
			concurrency := service.ProvideConcurrencyService(cache, nil, nil, gate)
			h := &OpenAIGatewayHandler{concurrencyHelper: NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)}
			ctx := service.GroupQualityTrafficContext(context.Background(), service.GroupQualityTrafficRequest{Key: "turn-2", GroupID: 3131, Model: "gpt-5.2"})
			ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{GroupID: 3131, AccountID: 1, Model: "gpt-5.1"})
			account := &service.Account{ID: 1, Concurrency: 1}
			release, acquired, err := h.tryAcquireOpenAIWSTurnAccountSlot(ctx, account.ID, 1, &account)
			require.ErrorIs(t, err, tc.gateErr)
			require.False(t, acquired)
			require.Nil(t, release)
			require.Equal(t, 1, acquires, "ordinary busy/error is never retried with an emergency marker")
			calls := gate.snapshot()
			require.Len(t, calls, tc.wantGateCalls)
			for _, call := range calls {
				require.False(t, call.fallback)
			}
		})
	}
}
