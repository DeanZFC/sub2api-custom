package qqbot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

func signingKey(secret string) (ed25519.PrivateKey, error) {
	if secret == "" {
		return nil, errors.New("AppSecret 未配置")
	}
	for len(secret) < ed25519.SeedSize {
		secret += secret
	}
	return ed25519.NewKeyFromSeed([]byte(secret[:ed25519.SeedSize])), nil
}
func VerifySignature(secret, timestamp, signature string, body []byte, now time.Time) bool {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(ts, 0)) > 5*time.Minute || time.Unix(ts, 0).Sub(now) > time.Minute {
		return false
	}
	key, err := signingKey(secret)
	if err != nil {
		return false
	}
	sig, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	publicKey, ok := key.Public().(ed25519.PublicKey)
	return ok && ed25519.Verify(publicKey, append([]byte(timestamp), body...), sig)
}

var challengeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]{1,128}$`)
var challengeTimePattern = regexp.MustCompile(`^[0-9]{1,20}$`)

func Challenge(secret string, data json.RawMessage) (map[string]string, error) {
	var v struct {
		PlainToken string `json:"plain_token"`
		EventTS    string `json:"event_ts"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	// The unsigned URL-validation endpoint must never sign arbitrary JSON. QQ
	// event signatures use the same timestamp+body format: accepting a JSON
	// document as plain_token would turn this into an event-forgery oracle.
	if !challengeTokenPattern.MatchString(v.PlainToken) || !challengeTimePattern.MatchString(v.EventTS) {
		return nil, errors.New("invalid challenge")
	}
	key, err := signingKey(secret)
	if err != nil {
		return nil, err
	}
	return map[string]string{"plain_token": v.PlainToken, "signature": hex.EncodeToString(ed25519.Sign(key, []byte(v.EventTS+v.PlainToken)))}, nil
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
func (s *Service) request(ctx context.Context, method, endpoint, key string, body any, limit int64) ([]byte, string, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, "", errors.New("请求地址无效")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", errors.New("服务连接失败或超时；未自动重试，请查看用量后再操作")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("服务返回 HTTP %d，请管理员检查接口、模型和凭据", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", errors.New("响应读取失败")
	}
	if int64(len(raw)) > limit {
		return nil, "", errors.New("响应超过大小限制")
	}
	return raw, resp.Header.Get("Content-Type"), nil
}
func endpoint(base, path string) string { return strings.TrimRight(base, "/") + path }

func (s *Service) chat(ctx context.Context, c Config, messages []Message) (string, int64, int64, error) {
	path := "/chat/completions"
	body := map[string]any{"model": c.ChatModel, "messages": messages, "stream": false, "max_completion_tokens": c.MaxOutputTokens}
	if c.ChatProtocol == "responses" {
		path = "/responses"
		body = map[string]any{"model": c.ChatModel, "input": responsesMessages(messages), "stream": false, "store": false, "max_output_tokens": c.MaxOutputTokens}
	}
	raw, ct, err := s.request(ctx, "POST", endpoint(c.BaseURL, path), c.APIKey, body, 4<<20)
	if err != nil {
		return "", 0, 0, err
	}
	return parseChat(raw, ct)
}

func responsesMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{"role": message.Role}
		if len(message.Images) == 0 {
			item["content"] = message.Content
		} else {
			parts := make([]map[string]any, 0, len(message.Images)+1)
			if message.Content != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": message.Content})
			}
			for _, imageURL := range message.Images {
				if imageURL != "" {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": imageURL})
				}
			}
			item["content"] = parts
		}
		out = append(out, item)
	}
	return out
}
func parseChat(raw []byte, ct string) (string, int64, int64, error) {
	if strings.Contains(ct, "text/event-stream") {
		var out strings.Builder
		var in, output int64
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 4096), 4<<20)
		done := false
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				done = true
				continue
			}
			var v struct {
				Type    string `json:"type"`
				Delta   string `json:"delta"`
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
				Response json.RawMessage `json:"response"`
				Usage    struct {
					Prompt     int64 `json:"prompt_tokens"`
					Completion int64 `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &v) != nil {
				continue
			}
			if v.Type == "error" || v.Type == "response.failed" || v.Type == "response.incomplete" {
				return "", 0, 0, errors.New("模型未完成回复，请查看调用记录")
			}
			if v.Type == "response.output_text.delta" {
				_, _ = out.WriteString(v.Delta)
			}
			for _, ch := range v.Choices {
				_, _ = out.WriteString(ch.Delta.Content)
			}
			if v.Usage.Prompt > 0 {
				in = v.Usage.Prompt
				output = v.Usage.Completion
			}
			if v.Type == "response.completed" {
				done = true
				text, i, o, e := parseChat(v.Response, "application/json")
				if e == nil {
					if out.Len() == 0 {
						_, _ = out.WriteString(text)
					}
					in = i
					output = o
				}
			}
		}
		if scanner.Err() != nil || !done || out.Len() == 0 {
			return "", 0, 0, errors.New("模型流式响应不完整")
		}
		return out.String(), in, output, nil
	}
	var v struct {
		Status     string `json:"status"`
		OutputText string `json:"output_text"`
		Choices    []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			Prompt     int64 `json:"prompt_tokens"`
			Completion int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", 0, 0, errors.New("模型响应格式不兼容")
	}
	if v.Status == "failed" || v.Status == "incomplete" {
		return "", 0, 0, errors.New("模型未完成回复")
	}
	text := v.OutputText
	if len(v.Choices) > 0 {
		text = v.Choices[0].Message.Content
	}
	if text == "" {
		for _, o := range v.Output {
			for _, p := range o.Content {
				if p.Type == "output_text" {
					text += p.Text
				}
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", 0, 0, errors.New("模型没有返回文本")
	}
	if v.Usage.Input == 0 {
		v.Usage.Input = v.Usage.Prompt
	}
	if v.Usage.Output == 0 {
		v.Usage.Output = v.Usage.Completion
	}
	return text, v.Usage.Input, v.Usage.Output, nil
}

func (s *Service) generateImage(ctx context.Context, c Config, prompt string) ([]byte, error) {
	base, key := c.BaseURL, c.APIKey
	if c.ImageBaseURL != "" {
		base = c.ImageBaseURL
		key = c.ImageAPIKey
	} else if c.ImageAPIKey != "" {
		key = c.ImageAPIKey
	}
	raw, _, err := s.request(ctx, "POST", endpoint(base, "/images/generations"), key, map[string]any{"model": c.ImageModel, "prompt": prompt, "n": 1, "size": c.ImageSize}, 32<<20)
	if err != nil {
		return nil, err
	}
	var v struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Data) == 0 {
		return nil, errors.New("生图接口没有返回图片")
	}
	var data []byte
	if v.Data[0].B64 != "" {
		data, err = base64.StdEncoding.DecodeString(v.Data[0].B64)
	} else if v.Data[0].URL != "" {
		data, err = downloadImage(ctx, v.Data[0].URL)
	} else {
		return nil, errors.New("图片响应缺少 b64_json 或 url")
	}
	if err != nil {
		return nil, errors.New("获取生成图片失败")
	}
	return normalizeImage(data)
}
func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	v4 := ip.To4()
	return v4 == nil || v4[0] != 100 || v4[1] < 64 || v4[1] > 127
}

func downloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return nil, errors.New("图片地址必须为公网 HTTPS")
	}
	tr := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("图片域名无法解析")
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, errors.New("图片地址禁止访问内网")
			}
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: noRedirect}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, errors.New("图片下载失败")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (20<<20)+1))
	if len(data) > 20<<20 {
		return nil, errors.New("图片过大")
	}
	return data, err
}
func normalizeImage(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return nil, errors.New("图片尺寸或格式不受支持")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("图片解码失败")
	}
	if cfg.Width > 2048 || cfg.Height > 2048 {
		w, h := cfg.Width, cfg.Height
		if w >= h {
			h = h * 2048 / w
			w = 2048
		} else {
			w = w * 2048 / h
			h = 2048
		}
		dst := image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		img = dst
	}
	var out bytes.Buffer
	if err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	if out.Len() > 3<<20 {
		return nil, errors.New("图片压缩后仍超过 QQ 上传限制")
	}
	return out.Bytes(), nil
}

func (s *Service) token(ctx context.Context, c Config) (string, error) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	identity := codeHash(c.AppID + ":" + c.AppSecret)
	if s.tokenKey == identity && time.Now().Before(s.tokenExpires) {
		return s.accessToken, nil
	}
	raw, _, err := s.request(ctx, "POST", s.tokenURL, "", map[string]string{"appId": c.AppID, "clientSecret": c.AppSecret}, 1<<20)
	if err != nil {
		return "", err
	}
	var v struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
	}
	if json.Unmarshal(raw, &v) != nil || v.AccessToken == "" {
		return "", errors.New("QQ 凭据验证失败")
	}
	seconds, _ := v.ExpiresIn.Int64()
	if seconds < 60 {
		seconds = 60
	}
	s.tokenKey = identity
	s.accessToken = v.AccessToken
	s.tokenExpires = time.Now().Add(time.Duration(seconds-30) * time.Second)
	return v.AccessToken, nil
}
func (s *Service) qqRequest(ctx context.Context, c Config, path string, body any) ([]byte, error) {
	return s.qqRequestMethod(ctx, c, http.MethodPost, path, body)
}
func (s *Service) qqRequestMethod(ctx context.Context, c Config, method, path string, body any) ([]byte, error) {
	token, err := s.token(ctx, c)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	base := s.qqURL
	if c.Sandbox && base == "https://api.sgroup.qq.com" {
		base = "https://sandbox.api.sgroup.qq.com"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	req.Header.Set("X-Union-Appid", c.AppID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, errors.New("QQ 消息发送失败")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var v struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(data, &v)
		return nil, fmt.Errorf("QQ HTTP %d / code %d", resp.StatusCode, v.Code)
	}
	return data, nil
}
func (s *Service) send(ctx context.Context, c Config, j Job, text string, img []byte, seq int) error {
	// Only passive replies: no accidental proactive notification or expired retry.
	if j.MessageID == "" || time.Since(j.CreatedAt) > 280*time.Second {
		return errors.New("回复窗口已过期")
	}
	base := "/v2/groups/" + url.PathEscape(j.GroupID)
	body := map[string]any{"msg_id": j.MessageID, "msg_seq": seq}
	if j.ReplyReference != "" {
		body["message_reference"] = map[string]string{"message_id": j.ReplyReference}
	}
	deliveredText := limitText(text, 1800)
	if len(img) > 0 {
		data, err := s.qqRequest(ctx, c, base+"/files", map[string]any{"file_type": 1, "file_name": "ctav.jpg", "file_data": base64.StdEncoding.EncodeToString(img), "srv_send_msg": false})
		if err != nil {
			return err
		}
		var v struct {
			FileInfo string `json:"file_info"`
		}
		if json.Unmarshal(data, &v) != nil || v.FileInfo == "" {
			return errors.New("QQ 图片上传未返回文件信息")
		}
		body["msg_type"] = 7
		body["media"] = map[string]string{"file_info": v.FileInfo}
	} else if j.EventType == groupMessageCreate && j.MemberID != "" {
		// Send explicit mentions through Markdown: group clients can display the
		// same tag literally in plain-text messages (msg_type=0).
		// https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html
		text = `<qqbot-at-user id="` + html.EscapeString(j.MemberID) + `" />` + "\n" + strings.TrimLeft(text, "\n")
		body["msg_type"] = 2
		// QQ requires content/ark to be absent when markdown is supplied.
		markdown := limitText(text, 1800)
		body["markdown"] = map[string]string{"content": markdown}
		deliveredText = strings.TrimPrefix(markdown, `<qqbot-at-user id="`+html.EscapeString(j.MemberID)+`" />`+"\n")
	} else {
		// QQ adds the sender mention for GROUP_AT_MESSAGE_CREATE replies itself.
		body["msg_type"] = 0
		body["content"] = limitText(text, 1800)
	}
	raw, err := s.qqRequest(ctx, c, base+"/messages", body)
	if err != nil {
		return err
	}
	return s.rememberSentMessage(ctx, c, j.GroupID, raw, deliveredText, len(img) > 0)
}

func limitText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (s *Service) Probe(ctx context.Context, kind string) (string, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return "", err
	}
	if kind == "qq" {
		_, err = s.token(ctx, c)
		return "QQ 凭据有效；请在测试群验证事件和消息权限。", err
	}
	if c.BaseURL == "" || c.APIKey == "" {
		return "", errors.New("请先保存中转地址和 API Key")
	}
	if kind == "image" {
		_, err = s.generateImage(ctx, c, "A small blue circle on a white background, minimal illustration.")
		return "生图测试成功（使用站长 API Key，可能产生费用）。", err
	}
	if kind != "chat" {
		return "", errors.New("未知测试类型")
	}
	result, _, _, err := s.chat(ctx, c, []Message{{Role: "user", Content: "Reply with OK."}})
	return limitText(result, 300), err
}
