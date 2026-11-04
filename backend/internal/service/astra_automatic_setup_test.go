package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestAstraAutomaticSetupShutdownCancelsAndJoins(t *testing.T) {
	gateway, repo, _ := unifiedAstraFixture(t)
	started := make(chan struct{})
	gateway.httpUpstream = &harvestProxyUpstream{do: func(req *http.Request, _ string) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}}
	service := &AccountTestService{cfg: gateway.cfg, accountRepo: repo, openaiGatewayService: gateway}
	settings := gateway.cfg.AstraRouting(context.Background())
	service.StartAstraAutomaticSetup(settings)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("setup did not start")
	}
	stopped := make(chan struct{})
	go func() { service.StopAstraAutomaticSetup(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join canceled setup")
	}
	require.True(t, service.astraSetupStopped)
	require.Equal(t, "failed", service.astraSetupStatus.State)
	service.StartAstraAutomaticSetup(settings)
	require.Equal(t, "failed", service.astraSetupStatus.State, "shutdown prevents new background requests")
	service.StopAstraAutomaticSetup()
}
