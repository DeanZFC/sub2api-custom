package vm2api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type socksProxy struct {
	host     string
	port     int
	username string
	password string
}

func parseSocksProxy(raw string) (socksProxy, error) {
	var p socksProxy
	if len(raw) > 8192 {
		return p, errors.New("vm2api: invalid proxy URL")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "socks5" && u.Scheme != "socks5h") || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return p, errors.New("vm2api: proxy must be a SOCKS5 or SOCKS5H URL")
	}
	p.host = canonicalHost(u.Hostname())
	p.port = 1080
	if u.Port() != "" {
		p.port, err = strconv.Atoi(u.Port())
		if err != nil || p.port < 1 || p.port > 65535 {
			return socksProxy{}, errors.New("vm2api: invalid proxy port")
		}
	}
	if u.User != nil {
		p.username = u.User.Username()
		p.password, _ = u.User.Password()
	}
	if strings.ContainsAny(p.host+p.username+p.password, "\r\n") {
		return socksProxy{}, errors.New("vm2api: invalid proxy URL")
	}
	return p, nil
}
func canonicalHost(host string) string { return strings.ToLower(strings.Trim(host, "[]")) }

func (c *Client) ListProxies(ctx context.Context) ([]Proxy, error) {
	var result struct {
		Proxies []Proxy `json:"proxies"`
	}
	if err := c.control(ctx, http.MethodGet, "/api/panel/proxies", "list_proxies", nil, &result); err != nil {
		return nil, err
	}
	return result.Proxies, nil
}

func (c *Client) EnsureLocalProxy(ctx context.Context) (*Proxy, error) {
	var result struct {
		Proxy Proxy `json:"proxy"`
		OK    *bool `json:"ok"`
	}
	if err := c.control(ctx, http.MethodPost, "/api/panel/proxies/local", "ensure_local_proxy", struct{}{}, &result); err != nil {
		return nil, err
	}
	if (result.OK != nil && !*result.OK) || validID(result.Proxy.ID) != nil {
		return nil, &APIError{Operation: "ensure_local_proxy", Code: "proxy_missing"}
	}
	if !result.Proxy.Enabled {
		return &result.Proxy, &APIError{Operation: "ensure_local_proxy", Code: "proxy_disabled"}
	}
	return &result.Proxy, nil
}

// findProxy compares the full credential tuple after narrowing by public
// host/port metadata. The reveal result never escapes this method or errors.
func (c *Client) findProxy(ctx context.Context, wanted socksProxy) (*Proxy, error) {
	proxies, err := c.ListProxies(ctx)
	if err != nil {
		return nil, err
	}
	for i := range proxies {
		p := &proxies[i]
		if canonicalHost(p.Host) != wanted.host || p.Port != wanted.port {
			continue
		}
		if err := validID(p.ID); err != nil {
			return nil, err
		}
		var reveal struct {
			ID  string `json:"id"`
			URI string `json:"uri"`
		}
		if err = c.control(ctx, http.MethodPost, "/api/panel/proxies/"+p.ID+"/reveal", "reveal_proxy", struct{}{}, &reveal); err != nil {
			return nil, err
		}
		if reveal.ID != p.ID {
			return nil, &APIError{Operation: "reveal_proxy", Code: "proxy_identity_mismatch"}
		}
		actual, parseErr := parseSocksProxy(reveal.URI)
		if parseErr != nil {
			return nil, &APIError{Operation: "reveal_proxy", Code: "invalid_proxy"}
		}
		if actual != wanted {
			continue
		}
		if !p.Enabled {
			return p, &APIError{Operation: "ensure_proxy", Code: "proxy_disabled"}
		}
		return p, nil
	}
	return nil, nil
}

// ImportProxy reuses matching proxy credentials. vm2api skips duplicate imports
// without returning their IDs, so retries resolve the existing row explicitly.
func (c *Client) ImportProxy(ctx context.Context, input ProxyImportRequest) (*Proxy, error) {
	wanted, err := parseSocksProxy(input.URL)
	if err != nil {
		return nil, err
	}
	if input.BindSlotID != "" {
		if err := validID(input.BindSlotID); err != nil {
			return nil, err
		}
	}
	p, err := c.findProxy(ctx, wanted)
	if err != nil {
		return p, err
	}
	if p == nil {
		body := struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
		}{wanted.host, wanted.port, wanted.username, wanted.password}
		var result struct {
			Added int     `json:"added"`
			Items []Proxy `json:"items"`
		}
		if err = c.control(ctx, http.MethodPost, "/api/panel/proxies/import", "import_proxy", body, &result); err != nil {
			return nil, err
		}
		if len(result.Items) == 1 {
			p = &result.Items[0]
		} else if len(result.Items) == 0 {
			p, err = c.findProxy(ctx, wanted)
			if err != nil {
				return p, err
			}
		}
		if p == nil || validID(p.ID) != nil || canonicalHost(p.Host) != wanted.host || p.Port != wanted.port {
			return p, &APIError{Operation: "import_proxy", Code: "proxy_import_failed"}
		}
		if !p.Enabled {
			return p, &APIError{Operation: "import_proxy", Code: "proxy_disabled"}
		}
	}
	if input.BindSlotID != "" {
		bound, bindErr := c.BindProxy(ctx, p.ID, input.BindSlotID)
		if bindErr != nil {
			return p, bindErr
		}
		p = bound
	}
	return p, nil
}

func (c *Client) BindProxy(ctx context.Context, proxyID, slotID string) (*Proxy, error) {
	if err := validID(proxyID); err != nil {
		return nil, err
	}
	if err := validID(slotID); err != nil {
		return nil, err
	}
	var result struct {
		Proxy  Proxy `json:"proxy"`
		Worker *struct {
			OK *bool `json:"ok"`
		} `json:"worker"`
	}
	body := struct {
		VMID string `json:"vm_id"`
	}{slotID}
	if err := c.control(ctx, http.MethodPost, "/api/panel/proxies/"+proxyID+"/bind", "bind_proxy", body, &result); err != nil {
		return nil, err
	}
	p := &result.Proxy
	if p.ID != proxyID {
		return p, &APIError{Operation: "bind_proxy", Code: "proxy_identity_mismatch"}
	}
	bound := false
	for _, id := range p.BoundVMIDs {
		if id == slotID {
			bound = true
			break
		}
	}
	if !bound || !p.Enabled || (result.Worker != nil && result.Worker.OK != nil && !*result.Worker.OK) {
		return p, &APIError{Operation: "bind_proxy", Code: "proxy_binding_failed"}
	}
	return p, nil
}
