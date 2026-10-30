package handler

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func wsAllowlistGroup(enabled bool, models ...string) *service.Group {
	return &service.Group{
		ID:       4201,
		Platform: service.PlatformOpenAI,
		ModelAllowlist: service.GroupModelAllowlist{
			Enabled: enabled,
			Models:  models,
		},
	}
}

// 首帧拒绝：模型不在白名单时连接被 1008 关闭（passthrough relay 模式）。
func TestOpenAIResponsesWebSocket_FirstFrameModelNotAllowedCloses_Passthrough(t *testing.T) {
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            `{"type":"response.create","model":"gpt-4.1","stream":false}`,
		group:                   wsAllowlistGroup(true, "gpt-5.4"),
		ingressMode:             service.OpenAIWSIngressModePassthrough,
		firstFrameCloseExpected: true,
	})
}

// 首帧拒绝：原生 ingress 模式。
func TestOpenAIResponsesWebSocket_FirstFrameModelNotAllowedCloses_NativeIngress(t *testing.T) {
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            `{"type":"response.create","model":"gpt-4.1","stream":false}`,
		group:                   wsAllowlistGroup(true, "gpt-5.4"),
		ingressMode:             service.OpenAIWSIngressModeDedicated,
		firstFrameCloseExpected: true,
	})
}

// 首帧命中白名单：连接正常建立并收到 response.completed（passthrough）。
func TestOpenAIResponsesWebSocket_FirstFrameAllowlistedModelProceeds(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		group:        wsAllowlistGroup(true, "gpt-5.4"),
	})
	if len(got.clientEvents) != 1 {
		t.Fatalf("expected one completed event, got %d", len(got.clientEvents))
	}
}

// 后续 turn 切换到白名单外的模型：连接被 1008 关闭（passthrough relay 模式）。
func TestOpenAIResponsesWebSocket_SubsequentTurnModelNotAllowedCloses_Passthrough(t *testing.T) {
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		secondPayload:           `{"type":"response.create","model":"gpt-4.1","stream":false}`,
		group:                   wsAllowlistGroup(true, "gpt-5.4"),
		ingressMode:             service.OpenAIWSIngressModePassthrough,
		secondTurnCloseExpected: true,
	})
}

// 后续 turn 切换到白名单外的模型：连接被 1008 关闭（原生 ingress 模式）。
func TestOpenAIResponsesWebSocket_SubsequentTurnModelNotAllowedCloses_NativeIngress(t *testing.T) {
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		secondPayload:           `{"type":"response.create","model":"gpt-4.1","stream":false}`,
		group:                   wsAllowlistGroup(true, "gpt-5.4"),
		ingressMode:             service.OpenAIWSIngressModeDedicated,
		secondTurnCloseExpected: true,
	})
}

// 后续 turn 省略 model：沿用会话初始模型，白名单校验通过。
func TestOpenAIResponsesWebSocket_SubsequentTurnOmittedModelUsesSessionModel(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:  `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		secondPayload: `{"type":"response.create","stream":false}`,
		group:         wsAllowlistGroup(true, "gpt-5.4"),
	})
	if len(got.clientEvents) != 2 {
		t.Fatalf("expected two completed events, got %d", len(got.clientEvents))
	}
	if len(got.upstreamPayloads) != 2 {
		t.Fatalf("expected two upstream frames, got %d", len(got.upstreamPayloads))
	}
}

// 白名单关闭：不受任何影响。
func TestOpenAIResponsesWebSocket_DisabledAllowlistDoesNotInterfere(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:  `{"type":"response.create","model":"gpt-4.1","stream":false}`,
		secondPayload: `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		group:         wsAllowlistGroup(false, "gpt-5.4"),
	})
	if len(got.clientEvents) != 2 {
		t.Fatalf("expected two completed events, got %d", len(got.clientEvents))
	}
}

// Model ambiguity is invalid independently of group allowlist configuration.
// Test both ingress implementations before the first turn and after a valid turn.
func TestOpenAIResponsesWebSocket_DuplicateModelFieldsRejected(t *testing.T) {
	fields := []struct {
		name string
		json string
	}{
		{"different values", `"model":"gpt-5.4","model":"gpt-4.1"`},
		{"identical values", `"model":"gpt-5.4","model":"gpt-5.4"`},
		{"case variant", `"model":"gpt-5.4","Model":"gpt-4.1"`},
		{"escaped key", `"model":"gpt-5.4","m\u006fdel":"gpt-4.1"`},
		{"null duplicate", `"model":"gpt-5.4","model":null`},
		{"non-string duplicate", `"model":"gpt-5.4","model":123`},
		{"legacy messages", `"model":"gpt-5.4","model":"gpt-4.1","messages":[{"role":"user","content":"hello"}]`},
	}
	groups := []struct {
		name  string
		group *service.Group
	}{
		{"allowlist disabled", wsAllowlistGroup(false)},
		{"both models allowed", wsAllowlistGroup(true, "gpt-5.4", "gpt-4.1")},
	}
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeDedicated} {
		for _, group := range groups {
			for _, field := range fields {
				for _, subsequent := range []bool{false, true} {
					stage := "first frame"
					if subsequent {
						stage = "subsequent turn"
					}
					t.Run(mode+"/"+group.name+"/"+field.name+"/"+stage, func(t *testing.T) {
						tc := openAIResponsesWSUsageLogCase{
							firstPayload:            `{"type":"response.create",` + field.json + `,"stream":false}`,
							group:                   group.group,
							ingressMode:             mode,
							firstFrameCloseExpected: true,
							closeReason:             "duplicate or case-conflicting model fields are not allowed",
						}
						if subsequent {
							tc.secondPayload = tc.firstPayload
							tc.firstPayload = `{"type":"response.create","model":"gpt-5.4","stream":false}`
							tc.firstFrameCloseExpected = false
							tc.secondTurnCloseExpected = true
							tc.checkFirstTurnUsage = true
						}
						got := runOpenAIResponsesWebSocketUsageLogCase(t, tc)
						if subsequent && got.log.Model != "gpt-5.4" {
							t.Fatalf("accepted first turn model changed: got %q", got.log.Model)
						}
					})
				}
			}
		}
	}
}

// session.update 轮换绕过：首帧用白名单内模型建立会话，session.update 把会话
// 模型改为白名单外的模型，随后 response.create 携带嵌套 session.model（白名单
// 内）让帧内候选非空。实际生效模型（轮换后的会话模型）必须始终参与校验。
func TestOpenAIResponsesWebSocket_SessionUpdateRotationBypassRejected(t *testing.T) {
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		midPayload:              `{"type":"session.update","session":{"model":"gpt-4.1"}}`,
		secondPayload:           `{"type":"response.create","session":{"model":"gpt-5.4"},"stream":false}`,
		group:                   wsAllowlistGroup(true, "gpt-5.4"),
		ingressMode:             service.OpenAIWSIngressModePassthrough,
		secondTurnCloseExpected: true,
	})
}

// 轮换后的会话模型本身在白名单内时，省略 model 的后续 turn 正常放行。
func TestOpenAIResponsesWebSocket_SessionUpdateToAllowedModelStillWorks(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:  `{"type":"response.create","model":"gpt-5.4","stream":false}`,
		midPayload:    `{"type":"session.update","session":{"model":"gpt-5.4"}}`,
		secondPayload: `{"type":"response.create","stream":false}`,
		group:         wsAllowlistGroup(true, "gpt-5.4"),
	})
	if len(got.clientEvents) < 2 {
		t.Fatalf("expected at least two completed events, got %d", len(got.clientEvents))
	}
}

func TestOpenAIResponsesWebSocket_AmbiguousSessionUpdateRejected(t *testing.T) {
	payloads := []struct {
		name string
		body string
	}{
		{"duplicate model", `{"type":"session.update","session":{"model":"gpt-5.4","model":"gpt-4.1"}}`},
		{"identical model", `{"type":"session.update","session":{"model":"gpt-5.4","model":"gpt-5.4"}}`},
		{"case model", `{"type":"session.update","session":{"model":"gpt-5.4","Model":"gpt-4.1"}}`},
		{"null model", `{"type":"session.update","session":{"model":"gpt-5.4","model":null}}`},
		{"duplicate session", `{"type":"session.update","session":{"model":"gpt-5.4"},"session":{"model":"gpt-4.1"}}`},
		{"case session", `{"type":"session.update","session":{"model":"gpt-5.4"},"Session":{"model":"gpt-4.1"}}`},
		{"null session", `{"type":"session.update","session":{"model":"gpt-5.4"},"session":null}`},
	}
	for _, tt := range payloads {
		t.Run(tt.name, func(t *testing.T) {
			field := "model"
			if strings.HasSuffix(tt.name, "session") {
				field = "session"
			}
			// The session.update is the second incoming frame and must be closed
			// immediately; no following response.create is needed to reject it.
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:            `{"type":"response.create","model":"gpt-5.4","stream":false}`,
				secondPayload:           tt.body,
				group:                   wsAllowlistGroup(false),
				ingressMode:             service.OpenAIWSIngressModePassthrough,
				secondTurnCloseExpected: true,
				checkFirstTurnUsage:     true,
				closeReason:             "duplicate or case-conflicting " + field + " fields are not allowed",
			})
			if got.log.Model != "gpt-5.4" {
				t.Fatalf("accepted first turn model changed: got %q", got.log.Model)
			}
		})
	}
}
