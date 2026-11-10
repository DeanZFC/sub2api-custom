package service

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (result *http.Response, resultErr error) {
	if request == nil || account == nil {
		return nil, errors.New("OpenAI request or account is missing")
	}
	if attempt, _ := request.Context().Value(groupSmartWSAttemptKey{}).(*groupSmartWSAttempt); attempt != nil && attempt.sent.Load() {
		return nil, finishGroupSmartWSAttempt(attempt, errSmartWSAlreadyDispatched)
	}
	// Resolve ticket policy at the final native send boundary, after request identity
	// normalization and before opening a network connection. All HTTP aliases share it.
	if request != nil && account != nil && account.IsOpenAIOAuthLike() && s.openAICodexTicketEnabledContext(request.Context()) {
		raw, err := snapshotHTTPRequestBody(request)
		if err != nil {
			return nil, err
		}
		replaceHTTPRequestJSONBody(request, raw)
		model := extractOpenAICodexTicketModel(raw)
		account, err = s.refreshCodexTicketAccountForSend(request.Context(), account, model, request.Header.Get("Authorization"))
		if err != nil {
			return nil, err
		}
		if err := s.applyOpenAICodexTicket(request.Context(), account, model, request.Header); err != nil {
			return nil, err
		}
	}
	request = s.stickBoundCodexTicketRequest(request, account)
	bound := s.codexTicketRequestBound(request, account)
	observationTicket := s.boundCodexTicket(request, account)
	observationHeaders := request.Header.Clone()
	releaseChat := func() {}
	if bound {
		releaseChat = s.holdCodexTicketChat(account)
	}
	pinnedProxy, release, err := s.pinCodexTicketEgress(request, account, proxyURL)
	if err != nil {
		releaseChat()
		return nil, err
	}
	proxyURL = pinnedProxy
	if s.openaiIPv6 != nil {
		if err := s.openaiIPv6.CheckRoute(request.Context(), account, proxyURL); err != nil {
			release()
			releaseChat()
			return nil, err
		}
	}
	if err := validateOpenAIIPv6Route(account, proxyURL); err != nil {
		release()
		releaseChat()
		return nil, err
	}
	defer func() {
		observe := resultErr == nil && result != nil && result.StatusCode == http.StatusOK
		result, resultErr = attachCodexTicketEgressRelease(result, resultErr, func() {
			release()
			releaseChat()
			if observe {
				s.observeScheduledAstraCandy(request.Context(), observationHeaders, account, observationTicket)
			}
		})
	}()
	defer func() {
		if resultErr == nil {
			observedRequest := request
			if result != nil && result.Request != nil && s.codexTicketRequestBound(result.Request, account) {
				observedRequest = result.Request
			}
			if ticket := s.boundCodexTicket(observedRequest, account); ticket != nil {
				observationTicket = ticket
				observationHeaders = observedRequest.Header.Clone()
			}
			responseBound := bound || s.codexTicketRequestBound(observedRequest, account)
			s.observeCodexTicketResponse(observedRequest, result, account)
			if responseBound && s.settingService != nil && s.settingService.CodexTicketStrictResponse(request.Context()) && codexResponseMismatches(observedRequest, result, account) {
				if result != nil && result.Body != nil {
					_ = result.Body.Close()
				}
				result = nil
				resultErr = ErrCodexTicketResponseRejected
			}
		}
	}()

	if s.openaiVM != nil {
		if response, handled, err := s.openaiVM.RoundTrip(request, account); handled {
			return response, err
		}
	}
	if err := validateOpenAIVMProxy(account, account.Proxy); err != nil {
		return nil, err
	}
	if s.pluginManager != nil && !bound && !account.IsOpenAIIPv6Bound() {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}

	if response, handled, err := s.tryGroupSmartWS(request, proxyURL, account); handled {
		return response, err
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if gateway := s.openaiGatewayService; gateway != nil && request != nil && request.URL != nil && request.URL.String() == chatgptCodexURL && gateway.openAICodexTicketEnabledContext(request.Context()) {
		raw, err := snapshotHTTPRequestBody(request)
		if err != nil {
			return nil, err
		}
		replaceHTTPRequestJSONBody(request, raw)
		model := extractOpenAICodexTicketModel(raw)
		if gateway.isOpenAICodexTicketAccount(request.Context(), account, model) && gateway.openAICodexTicketGatedModel(model) {
			if err := gateway.applyOpenAICodexTicket(request.Context(), account, model, request.Header, "sse"); err != nil {
				return nil, err
			}
			request = request.WithContext(context.WithValue(request.Context(), astraCandySkipObservationKey{}, true))
			started := time.Now().UTC()
			response, sendErr := gateway.doOpenAIUpstream(request, proxyURL, account)
			return observeAccountTestResponse(request.Context(), started, response, sendErr)
		}
	}
	if s.openaiGatewayService != nil && s.openaiGatewayService.openaiIPv6 != nil {
		if err := s.openaiGatewayService.openaiIPv6.CheckRoute(request.Context(), account, proxyURL); err != nil {
			return nil, err
		}
	}
	if s.openaiVM != nil {
		started := time.Now().UTC()
		if response, handled, err := s.openaiVM.RoundTrip(request, account); handled {
			return observeAccountTestResponse(request.Context(), started, response, err)
		}
	}
	if err := validateOpenAIVMProxy(account, account.Proxy); err != nil {
		return nil, err
	}
	if err := validateOpenAIIPv6Route(account, proxyURL); err != nil {
		return nil, err
	}
	if s.pluginManager != nil && !account.IsOpenAIIPv6Bound() {
		started := time.Now().UTC()
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return observeAccountTestResponse(request.Context(), started, response, err)
		}
	}
	if useTLSFallback {
		return s.doAccountTestHTTPWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}

	return s.doAccountTestHTTP(request, proxyURL, account.ID, account.Concurrency)
}
