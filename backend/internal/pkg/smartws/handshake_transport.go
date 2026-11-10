package smartws

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxHandshakeDiagnostics = 16
	maxHandshakeBodySample  = 8 << 10
)

type dialState struct {
	inFlight       int
	tokens         float64
	refilled, used time.Time
	failures       uint
	epoch          uint64
	blockedUntil   time.Time
	httpErrorUntil time.Time
	probing        bool
}
type dialPermit struct {
	state *dialState
	epoch uint64
	probe bool
}
type handshakeDiagnostic struct {
	At            string         `json:"at"`
	Status        int            `json:"status"`
	BodyBytes     int            `json:"body_bytes_observed"`
	BodyComplete  bool           `json:"body_complete"`
	BodySHA256    string         `json:"body_sha256"`
	CFRay         string         `json:"cf_ray,omitempty"`
	Capability    string         `json:"capability_hash"`
	Identity      string         `json:"identity_hash"`
	HeaderCount   int            `json:"header_count"`
	HeaderBytes   int            `json:"header_bytes"`
	HeaderLengths map[string]int `json:"header_lengths"`
	Outcome       string         `json:"outcome"`
}

func configureDialAdmission(c *poolConfig) {
	if c.dialConcurrent <= 0 {
		c.dialConcurrent = 2
	}
	if c.dialRate <= 0 {
		// Bound new handshakes across every session sharing the credential.
		// Reuse is unlimited by this gate. 0.9/s with burst 2 leaves margin
		// below the observed 60-per-minute refusal boundary; it is not a
		// provider quota guarantee and does not cover other Core processes.
		c.dialRate = 0.9
	}
	if c.dialBurst <= 0 {
		c.dialBurst = 2
	}
	if c.dialBackoff <= 0 {
		c.dialBackoff = 5 * time.Second
	}
	if c.dialBackoffMax <= 0 {
		c.dialBackoffMax = time.Minute
	}
}

// Caller holds p.mu. The identity/session key is unchanged; admission is shared
// by the same upstream credential across its many trusted identities.
func (p *Pool) admitDialLocked(key string, now time.Time) (*dialPermit, string) {
	st := p.dials[key]
	if st == nil {
		if len(p.dials) >= maxNegativeEntries {
			oldestKey := ""
			var oldest time.Time
			for k, candidate := range p.dials {
				// Retain spent buckets and unresolved circuits. Eviction must not
				// mint a fresh burst or erase a recent refusal when accounts churn.
				// Retired failures may age out only after the full quiet window.
				fullTokens := candidate.tokens + now.Sub(candidate.refilled).Seconds()*p.config.dialRate
				if candidate.inFlight != 0 || candidate.probing ||
					(candidate.failures != 0 && now.Sub(candidate.used) < accountActivityWindow) ||
					candidate.blockedUntil.After(now) || candidate.httpErrorUntil.After(now) || now.Sub(candidate.used) < time.Minute ||
					fullTokens < float64(p.config.dialBurst) {
					continue
				}
				if oldestKey == "" || candidate.used.Before(oldest) {
					oldestKey, oldest = k, candidate.used
				}
			}
			if oldestKey == "" {
				return nil, "dial_state_full"
			}
			delete(p.dials, oldestKey)
		}
		st = &dialState{tokens: float64(p.config.dialBurst), refilled: now}
		p.dials[key] = st
	}
	st.used = now
	if st.blockedUntil.After(now) {
		return nil, "dial_backoff"
	}
	if st.failures > 0 && (st.probing || st.inFlight > 0) {
		return nil, "dial_half_open_busy"
	}
	if st.inFlight >= p.config.dialConcurrent {
		return nil, "dial_concurrency_limited"
	}
	st.tokens += now.Sub(st.refilled).Seconds() * p.config.dialRate
	if st.tokens > float64(p.config.dialBurst) {
		st.tokens = float64(p.config.dialBurst)
	}
	st.refilled = now
	if st.tokens < 1 {
		return nil, "dial_rate_limited"
	}
	st.tokens--
	permit := &dialPermit{state: st, epoch: st.epoch, probe: st.failures > 0}
	st.inFlight++
	if permit.probe {
		st.probing = true
	}
	return permit, ""
}

// A successful older concurrent handshake cannot erase a later refusal.
// Only a half-open probe is admitted after the backoff has elapsed.
func (p *Pool) finishDialLocked(permit *dialPermit, outcome string, now time.Time) {
	st := permit.state
	st.inFlight--
	st.used = now
	// Keep explicit upstream errors on the established admission/error path.
	// A later retry must not turn this optimization into a rate/auth bypass.
	if outcome == "http_error" {
		st.httpErrorUntil = now.Add(accountActivityWindow)
	}
	if permit.probe {
		st.probing = false
	}
	if outcome == "blank_403" || (permit.probe && outcome == "connect_error") {
		if st.failures < 16 {
			st.failures++
		}
		st.epoch++
		delay := p.config.dialBackoff
		for n := uint(1); n < st.failures && delay < p.config.dialBackoffMax; n++ {
			delay *= 2
		}
		if delay > p.config.dialBackoffMax {
			delay = p.config.dialBackoffMax
		}
		st.blockedUntil = now.Add(delay)
		return
	}
	if permit.epoch == st.epoch && (outcome == "success" || outcome == "http_error" || outcome == "unsupported") {
		st.failures = 0
		st.blockedUntil = time.Time{}
	}
}

type restoredHandshakeBody struct {
	io.Reader
	io.Closer
}

// Preserve every consumed byte for errors still owned by Core. Only a complete,
// exactly empty response is eligible for the narrow blank-403 fallback.
func sampleHandshakeBody(resp *http.Response) ([]byte, bool) {
	if resp == nil {
		return nil, false
	}
	if resp.Body == nil {
		return nil, resp.ContentLength == 0
	}
	original := resp.Body
	sample, err := io.ReadAll(io.LimitReader(original, maxHandshakeBodySample+1))
	resp.Body = &restoredHandshakeBody{Reader: io.MultiReader(bytes.NewReader(sample), original), Closer: original}
	// Gorilla retains at most 1024 error-body bytes and discards its read error.
	// ContentLength proves completeness; an empty timeout/unknown-length body
	// must not be misclassified as a confirmed empty refusal.
	return sample, err == nil && len(sample) <= maxHandshakeBodySample && resp.ContentLength >= 0 && int64(len(sample)) == resp.ContentLength
}
func diagnosticToken(value string) string {
	if len(value) > 96 {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return value
}
func (p *Pool) recordHandshake(key, capability string, headers http.Header, resp *http.Response, sample []byte, complete bool, outcome string) {
	sum := sha256.Sum256(sample)
	d := handshakeDiagnostic{At: time.Now().UTC().Format(time.RFC3339Nano), BodyBytes: len(sample), BodyComplete: complete,
		BodySHA256: hex.EncodeToString(sum[:]), Capability: capability[:16], Identity: key[:16],
		HeaderCount: len(headers), HeaderLengths: map[string]int{}, Outcome: outcome}
	if resp != nil {
		d.Status = resp.StatusCode
		d.CFRay = diagnosticToken(resp.Header.Get("Cf-Ray"))
	}
	// Only known header names and lengths are retained; no raw values or bodies.
	for name, values := range headers {
		lower := strings.ToLower(name)
		n := 0
		for _, value := range values {
			n += len(value)
			d.HeaderBytes += len(name) + len(value) + 4
		}
		switch lower {
		case "session_id", "session-id", "conversation_id", "conversation-id", "x-codex-turn-state", "x-codex-turn-metadata",
			"x-codex-window-id", "x-codex-installation-id", "x-codex-beta-features", "x-codex-routing-hint", "user-agent", "openai-beta":
			d.HeaderLengths[lower] = n
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.handshakes) == maxHandshakeDiagnostics {
		copy(p.handshakes, p.handshakes[1:])
		p.handshakes[len(p.handshakes)-1] = d
	} else {
		p.handshakes = append(p.handshakes, d)
	}
}

// These headers belong to the initial handshake and are exposed only for its
// first request. Reuse must not resend stale rate snapshots or turn-state.
func handshakeResponseHeaders(resp *http.Response) http.Header {
	out := make(http.Header)
	if resp == nil {
		return out
	}
	for name, values := range resp.Header {
		switch strings.ToLower(name) {
		case "x-codex-turn-state", "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests",
			"x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens",
			"x-codex-primary-used-percent", "x-codex-primary-window-minutes", "x-codex-primary-reset-after-seconds",
			"x-codex-secondary-used-percent", "x-codex-secondary-window-minutes", "x-codex-secondary-reset-after-seconds":
			out[name] = append([]string(nil), values...)
		}
	}
	return out
}
