// Package ovhipv6 is the private control protocol for the host IPv6 egress agent.
package ovhipv6

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Lease struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	State    string `json:"state"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
}

type Status struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	Enabled    bool   `json:"enabled"`
	Prefix     string `json:"prefix"`
	Issued     string `json:"issued"`
	Bound      int64  `json:"bound"`
	Pending    int64  `json:"pending_cleanup"`
	Error      string `json:"error,omitempty"`
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// FromEnv retains the optional-provider convention. Use LoadFromEnv when a
// caller needs to distinguish missing configuration from invalid configuration.
func FromEnv() *Client {
	c, _ := LoadFromEnv()
	return c
}

func LoadFromEnv() (*Client, error) {
	base := strings.TrimSpace(os.Getenv("OVH_IPV6_CONTROL_URL"))
	token, file := os.Getenv("OVH_IPV6_CONTROL_TOKEN"), strings.TrimSpace(os.Getenv("OVH_IPV6_CONTROL_TOKEN_FILE"))
	if base == "" && token == "" && file == "" {
		return nil, nil
	}
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return nil, errors.New("OVH IPv6 control token file is unavailable")
		}
		raw, err := io.ReadAll(io.LimitReader(f, 4097))
		_ = f.Close()
		if err != nil || len(raw) > 4096 {
			return nil, errors.New("OVH IPv6 control token file is invalid")
		}
		token = strings.TrimSpace(string(raw))
	}
	origin, err := controlOrigin(base)
	if err != nil {
		return nil, err
	}
	if !validControlToken(token) {
		return nil, errors.New("OVH IPv6 control token must contain 32-4096 characters without whitespace")
	}
	return &Client{BaseURL: origin, Token: token, HTTP: &http.Client{
		Timeout:       25 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second},
		CheckRedirect: noRedirect,
	}}, nil
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
func validControlToken(token string) bool {
	if len(token) < 32 || len(token) > 4096 {
		return false
	}
	for _, r := range token {
		if r <= ' ' || r >= 127 {
			return false
		}
	}
	return true
}

func controlOrigin(raw string) (string, error) {
	invalid := errors.New("OVH IPv6 control URL must be an HTTP(S) origin without credentials, path, query, or fragment")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(u.Host, "%\\\r\n\t ") {
		return "", invalid
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", invalid
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", invalid
		}
	}
	if net.ParseIP(u.Hostname()) == nil && !regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`).MatchString(u.Hostname()) {
		return "", invalid
	}
	u.Path = ""
	return u.String(), nil
}

var leaseActionPath = regexp.MustCompile(`^/v1/leases/[1-9][0-9]*(/activate)?$`)

func validControlAction(method, path string) bool {
	if path == "/v1/status" {
		return method == http.MethodGet
	}
	if path == "/v1/leases" {
		return method == http.MethodPost
	}
	if !leaseActionPath.MatchString(path) {
		return false
	}
	if strings.HasSuffix(path, "/activate") {
		return method == http.MethodPost
	}
	return method == http.MethodDelete
}

func (c *Client) Call(ctx context.Context, method, path string, input, output any) error {
	if c == nil {
		return errors.New("OVH IPv6 出口服务尚未配置")
	}
	origin, err := controlOrigin(c.BaseURL)
	if err != nil {
		return err
	}
	if !validControlToken(c.Token) || !validControlAction(method, path) {
		return errors.New("OVH IPv6 control request configuration is invalid")
	}
	var reader io.Reader
	if input != nil {
		body, e := json.Marshal(input)
		if e != nil {
			return e
		}
		if len(body) > 4096 {
			return errors.New("OVH IPv6 control request is too large")
		}
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, reader)
	if err != nil {
		return errors.New("OVH IPv6 control request is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 25 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
		if client.Timeout <= 0 {
			client.Timeout = 25 * time.Second
		}
	}
	// Always prohibit redirects, even for caller-supplied test transports.
	client.CheckRedirect = noRedirect
	res, err := client.Do(req)
	if err != nil {
		return errors.New("OVH IPv6 出口服务连接失败")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("OVH IPv6 出口服务返回 HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("OVH IPv6 control response is invalid or too large")
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return errors.New("OVH IPv6 control response is invalid")
	}
	return nil
}

// Address calculates a host without enumerating the prefix. Zero is reserved.
// Decimal IDs support all 72 host bits of an IPv6 /56, beyond uint64.
func Address(prefix, sequence string) (netip.Addr, error) {
	p, err := netip.ParsePrefix(prefix)
	if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() || p.Bits() > 120 || p != p.Masked() {
		return netip.Addr{}, fmt.Errorf("invalid IPv6 allocation prefix")
	}
	n, ok := new(big.Int).SetString(sequence, 10)
	if !ok || n.Sign() <= 0 || n.BitLen() > 128-p.Bits() {
		return netip.Addr{}, fmt.Errorf("IPv6 allocation range exhausted or invalid")
	}
	b := p.Addr().As16()
	base := new(big.Int).SetBytes(b[:])
	base.Add(base, n).FillBytes(b[:])
	return netip.AddrFrom16(b), nil
}

func Next(sequence string) (string, error) {
	n, ok := new(big.Int).SetString(sequence, 10)
	if !ok || n.Sign() < 0 {
		return "", fmt.Errorf("invalid allocation counter")
	}
	return n.Add(n, big.NewInt(1)).String(), nil
}
