// Package vm2api adapts vm2api's control API for isolated OpenAI OAuth slots.
// It deliberately accepts no refresh tokens. Sub2API remains the refresh owner.
package vm2api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxControlBody = 4 << 20

type Config struct {
	BaseURL           string
	MasterKey         string       `json:"-"`
	HTTPClient        *http.Client `json:"-"`
	ManagementTimeout time.Duration
}

func (c Config) String() string   { return "vm2api.Config{credentials:redacted}" }
func (c Config) GoString() string { return c.String() }

type Client struct {
	base              *url.URL
	masterKey         string
	http              *http.Client
	managementTimeout time.Duration
}

func (*Client) String() string     { return "vm2api.Client{credentials:redacted}" }
func (c *Client) GoString() string { return c.String() }

// APIError intentionally omits upstream bodies, messages and URLs: management
// errors may echo imported access tokens or proxy passwords.
type APIError struct {
	Operation  string
	StatusCode int
	Code       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("vm2api %s failed (status=%d, code=%s)", e.Operation, e.StatusCode, e.Code)
}

func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}
func IsConflict(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == http.StatusConflict
}

// NewClient copies the supplied HTTP client and refuses every redirect so a
// redirect cannot move an administrative bearer credential to another URL.
func NewClient(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("vm2api: invalid base URL")
	}
	key := strings.TrimSpace(cfg.MasterKey)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("vm2api: master key required")
	}
	h := http.Client{}
	if cfg.HTTPClient != nil {
		h = *cfg.HTTPClient
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	timeout := cfg.ManagementTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return &Client{base: u, masterKey: key, http: &h, managementTimeout: timeout}, nil
}

// DeterministicSlotID is stable across retries and separates bridge instances.
// Account IDs alone are not globally unique; use an immutable installation ID
// as namespace, not an administrator-selected account name.
func DeterministicSlotID(namespace string, accountID int64) string {
	sum := sha256.Sum256([]byte(namespace))
	return fmt.Sprintf("s2a-%s-%d", hex.EncodeToString(sum[:6]), accountID)
}

var resourceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func validID(id string) error {
	if !resourceID.MatchString(id) {
		return errors.New("vm2api: invalid resource ID")
	}
	return nil
}

func (c *Client) endpoint(path string) string {
	u := *c.base
	parts := strings.SplitN(path, "?", 2)
	u.Path += parts[0]
	if len(parts) == 2 {
		u.RawQuery = parts[1]
	}
	return u.String()
}

func (c *Client) request(ctx context.Context, method, path, key string, body io.Reader) (*http.Request, error) {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("vm2api: credential required")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body) // #nosec G704 -- Base URL is operator configured; endpoint preserves its host and uses internal API paths.
	if err != nil {
		return nil, errors.New("vm2api: invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) send(req *http.Request, operation string) (*http.Response, error) {
	resp, err := c.http.Do(req) // #nosec G704 -- Requests use the validated operator-configured endpoint; NewClient rejects redirects.
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, fmt.Errorf("vm2api %s: %w", operation, ctxErr)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("vm2api %s: %w", operation, context.DeadlineExceeded)
		}
		return nil, &APIError{Operation: operation, Code: "transport_error"}
	}
	return resp, nil
}

type envelope struct {
	OK    *bool           `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

func decodeControl(data []byte, out any) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	if env.OK == nil || !*env.OK {
		return errors.New("operation rejected")
	}
	if len(env.Error) > 0 && string(env.Error) != "null" {
		return errors.New("operation rejected")
	}
	if out == nil {
		return nil
	}
	if len(env.Data) > 0 && string(env.Data) != "null" {
		data = env.Data
	}
	return json.Unmarshal(data, out)
}

func (c *Client) control(ctx context.Context, method, path, operation string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, c.managementTimeout)
	defer cancel()
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return errors.New("vm2api: invalid management input")
		}
		body = bytes.NewReader(b)
	}
	req, err := c.request(ctx, method, path, c.masterKey, body)
	if err != nil {
		return err
	}
	resp, err := c.send(req, operation)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxControlBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("vm2api %s: %w", operation, ctx.Err())
		}
		return &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "read_failed"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "http_error"}
	}
	if len(data) > maxControlBody {
		return &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "response_too_large"}
	}
	if err := decodeControl(data, output); err != nil {
		return &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "invalid_response"}
	}
	return nil
}
