package service

import (
	"context"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http"
	"testing"
	"time"
)

func TestOpenAIVMDuplicateInterruptAckDoesNotSettleNextTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, repo, _ := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP call") })
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 5
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.openaiVM, svc.accountRepo = vm, vm.accounts
	done := make(chan *OpenAIForwardResult, 3)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, repo.account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
			if err == nil {
				done <- result
			}
		}}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = client.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	second := `{"type":"response.create","model":"gpt-5.1","input":"queued"}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(second)))
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.interrupt"}`)))
	require.Equal(t, "response.interrupt", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String(), "queued create must not block interrupt")
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.interrupt"}`)))
	require.Equal(t, "response.interrupt", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	upstream.Send(`{"type":"response.interrupt.accepted"}`)
	ack, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"response.interrupt.accepted"}`, string(ack))
	select {
	case result := <-done:
		require.Empty(t, result.ResponseID)
	case <-time.After(time.Second):
		t.Fatal("interrupt did not release turn accounting")
	}
	require.JSONEq(t, second, string(requirePassthroughUpstreamWrite(t, upstream, time.Second)))
	upstream.Send(`{"type":"response.interrupt.accepted"}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	select {
	case result := <-done:
		t.Fatalf("old duplicate interrupt ACK settled new turn: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	upstream.Send(`{"type":"response.completed","response":{"id":"resp_queued","model":"gpt-5.1","usage":{"input_tokens":8,"output_tokens":6}}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	select {
	case result := <-done:
		require.EqualValues(t, 8, result.Usage.InputTokens)
		require.EqualValues(t, 6, result.Usage.OutputTokens)
	case <-time.After(time.Second):
		t.Fatal("queued turn usage missing")
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("session did not close")
	}
}

func TestOpenAIVMIdleInterruptAckDoesNotSettleNextTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, repo, _ := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP call") })
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 5
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.openaiVM, svc.accountRepo = vm, vm.accounts
	done := make(chan *OpenAIForwardResult, 4)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, repo.account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
			if err == nil {
				done <- result
			}
		}}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1"}`)
	defer func() { _ = client.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_idle_first","usage":{"input_tokens":2,"output_tokens":1}}}`)
	_, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first turn accounting missing")
	}
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.interrupt"}`)))
	require.Equal(t, "response.interrupt", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":"next"}`)))
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	upstream.Send(`{"type":"response.interrupt.accepted"}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	select {
	case result := <-done:
		t.Fatalf("idle interrupt ACK settled active turn: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_idle_second","usage":{"input_tokens":8,"output_tokens":6}}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	select {
	case result := <-done:
		require.EqualValues(t, 8, result.Usage.InputTokens)
		require.EqualValues(t, 6, result.Usage.OutputTokens)
	case <-time.After(time.Second):
		t.Fatal("second turn usage missing")
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("session did not close")
	}
}
