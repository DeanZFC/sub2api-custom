package smartws

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// These are local resource budgets, not claims about an upstream account limit.
const (
	accountActivityWindow = 2 * time.Minute
	maxAccountStates      = 256
)

type accountState struct {
	used             time.Time
	sockets, opening int
	waiters          []*capacityWaiter
}
type capacityWaiter struct {
	key   string
	bytes int
}

// Keep account admission stable across session headers, token refreshes and
// proxy changes. Socket affinity still includes the complete original headers,
// trusted billing scope and proxy; this key never permits cross-identity reuse.
func accountAdmissionKey(target *url.URL, headers http.Header) string {
	host := strings.ToLower(target.Hostname())
	if id := strings.TrimSpace(headers.Get("ChatGPT-Account-Id")); id != "" {
		return digest("upstream-account", host, id)
	}
	return digest("upstream-credential", host, headers.Get("Authorization"))
}

func (p *Pool) signalLocked() {
	if p.changed != nil {
		close(p.changed)
	}
	p.changed = make(chan struct{})
}
func (p *Pool) accountLive(st *accountState, now time.Time) bool {
	return st.sockets > 0 || st.opening > 0 || len(st.waiters) > 0 ||
		now.Sub(st.used) < accountActivityWindow
}
func (p *Pool) pruneAccountsLocked(now time.Time) {
	for key, st := range p.accounts {
		if !p.accountLive(st, now) {
			delete(p.accounts, key)
		}
	}
}
func (p *Pool) accountLocked(key string, now time.Time) *accountState {
	if st := p.accounts[key]; st != nil {
		st.used = now
		return st
	}
	p.pruneAccountsLocked(now)
	if len(p.accounts) >= maxAccountStates {
		return nil
	}
	st := &accountState{used: now}
	p.accounts[key] = st
	return st
}
func (p *Pool) capacityLocked(now time.Time) (int, int) {
	accounts := 0
	for _, st := range p.accounts {
		if p.accountLive(st, now) {
			accounts++
		}
	}
	capacity := p.config.capacity
	if p.config.adaptive {
		scaled := accounts * p.config.perAccount
		if scaled > capacity {
			capacity = scaled
		}
		if capacity > p.config.hardCapacity {
			capacity = p.config.hardCapacity
		}
	}
	return capacity, accounts
}
func (p *Pool) detachLocked(c *connection) bool {
	if _, exists := p.connections[c]; !exists {
		return false
	}
	delete(p.connections, c)
	if st := p.accounts[c.accountKey]; st != nil {
		st.sockets--
	}
	return true
}
func (p *Pool) expiredIdleLocked(now time.Time) []*connection {
	var stale []*connection
	for c := range p.connections {
		if c.active == nil && p.expired(c, now) {
			p.detachLocked(c)
			stale = append(stale, c)
			p.counters["idle_expired"]++
			if now.Sub(c.created) >= p.config.lifetime {
				p.counters["idle_expired_lifetime"]++
			} else {
				p.counters["idle_expired_timeout"]++
			}
		}
	}
	if len(stale) > 0 {
		p.signalLocked()
	}
	return stale
}
func closeDetached(items []*connection) {
	for _, c := range items {
		c.discard()
	}
}
func (p *Pool) idleVictimLocked(account string, sameAccount bool) *connection {
	var victim *connection
	for c := range p.connections {
		if c.active != nil || (sameAccount && c.accountKey != account) {
			continue
		}
		if victim == nil || c.used.Before(victim.used) {
			victim = c
		}
	}
	return victim
}
func (p *Pool) removeWaiterLocked(st *accountState, waiter *capacityWaiter) {
	if waiter == nil {
		return
	}
	for i, candidate := range st.waiters {
		if candidate == waiter {
			copy(st.waiters[i:], st.waiters[i+1:])
			st.waiters[len(st.waiters)-1] = nil
			st.waiters = st.waiters[:len(st.waiters)-1]
			p.waiting--
			p.waitingBytes -= waiter.bytes
			p.signalLocked()
			return
		}
	}
}
func (p *Pool) reuseTurnLocked(st *accountState, waiter *capacityWaiter, key string) bool {
	// A hot reusable socket may bypass a waiter for another identity, but never
	// a prior waiter for this exact identity. New dials remain FIFO per account.
	for _, candidate := range st.waiters {
		if candidate == waiter {
			return true
		}
		if candidate.key == key {
			return false
		}
	}
	return true
}
func (p *Pool) fallbackCountLocked(reason string) {
	switch reason {
	case "global_capacity_full", "account_capacity_full", "identity_capacity_full":
		p.counters["pool_full"]++
	}
	p.counters[reason]++
}
func (p *Pool) dialDelayLocked(key, reason string, now time.Time) time.Duration {
	st := p.dials[key]
	if st == nil {
		return 100 * time.Millisecond
	}
	switch reason {
	case "dial_rate_limited":
		delay := time.Duration((1 - st.tokens) / p.config.dialRate * float64(time.Second))
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		return delay
	case "dial_backoff":
		return st.blockedUntil.Sub(now)
	default:
		// A release/handshake completion normally wakes the waiter immediately.
		// This bounded timer also rechecks socket expiry and a lost external event.
		return 100 * time.Millisecond
	}
}

// acquire either leases an exact-affinity socket or reserves a rate-governed
// handshake. All waiting is before any generation dispatch. It is bounded by
// both time and the number/bytes of retained messages.
func (p *Pool) acquire(ctx context.Context, key, capability, account string, reusable bool, l *lease) (*connection, *dialPermit, error) {
	var waiter *capacityWaiter
	var state *accountState
	deadline := time.Now().Add(p.config.wait)
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.removeWaiterLocked(state, waiter)
			if waiter != nil {
				p.counters["wait_cancelled"]++
			}
			p.mu.Unlock()
			return nil, nil, err
		}
		if p.closed {
			p.removeWaiterLocked(state, waiter)
			p.counters["pool_closed"]++
			p.mu.Unlock()
			return nil, nil, nil
		}
		now := time.Now()
		stale := p.expiredIdleLocked(now)
		state = p.accountLocked(account, now)
		if state == nil {
			p.fallbackCountLocked("account_state_full")
			p.mu.Unlock()
			closeDetached(stale)
			return nil, nil, nil
		}
		var found *connection
		keyCount := p.openingKeys[key]
		for item := range p.connections {
			if item.key != key {
				continue
			}
			keyCount++
			if reusable && item.reusable && item.active == nil &&
				(found == nil || item.used.After(found.used)) {
				found = item
			}
		}
		if found != nil && p.reuseTurnLocked(state, waiter, key) {
			found.active = l
			p.active++
			p.counters["reused"]++
			if now.Sub(found.used) >= 60*time.Second {
				p.counters["reused_after_60s_idle"]++
			}
			if now.Sub(found.used) >= 120*time.Second {
				p.counters["reused_after_120s_idle"]++
			}
			if now.Sub(found.used) >= 240*time.Second {
				p.counters["reused_after_240s_idle"]++
			}
			if waiter != nil {
				p.counters["wait_reused"]++
			}
			p.removeWaiterLocked(state, waiter)
			p.mu.Unlock()
			closeDetached(stale)
			return found, nil, nil
		}
		reason := ""
		delay := 100 * time.Millisecond
		var victim *connection
		victimReason := "idle_reclaimed_global"
		capacity, _ := p.capacityLocked(now)
		switch {
		case p.negative[capability].After(now):
			reason = "unsupported_cached"
		case len(state.waiters) > 0 && state.waiters[0] != waiter:
			reason = "admission_fairness"
		case keyCount >= p.config.perKey:
			reason = "identity_capacity_full"
		case p.config.adaptive && state.sockets+state.opening >= p.config.perAccount:
			victimReason = "idle_reclaimed_account"
			victim = p.idleVictimLocked(account, true)
			if victim == nil {
				reason = "account_capacity_full"
			}
		}
		if reason == "" && len(p.connections)+p.opening >= capacity && victim == nil {
			victim = p.idleVictimLocked(account, true)
			if victim == nil {
				victim = p.idleVictimLocked(account, false)
			}
			if victim == nil {
				reason = "global_capacity_full"
			}
		}
		if reason == "" {
			permit, denied := p.admitDialLocked(account, now)
			if permit != nil {
				// Do not discard reusable sockets merely because a token is unavailable.
				// Selection, detachment and reservation occur atomically under p.mu.
				if victim != nil {
					p.detachLocked(victim)
					stale = append(stale, victim)
					p.counters["idle_reclaimed"]++
					p.counters[victimReason]++
					if now.Sub(victim.used) < 30*time.Second {
						p.counters["idle_reclaimed_under_30s"]++
					}
				}
				p.opening++
				p.openingKeys[key]++
				state.opening++
				if waiter != nil {
					p.counters["wait_admitted"]++
				}
				p.removeWaiterLocked(state, waiter)
				p.mu.Unlock()
				closeDetached(stale)
				return nil, permit, nil
			}
			reason = denied
			delay = p.dialDelayLocked(account, reason, now)
		}
		// Unsupported status/circuit evidence is already a concrete reason to use
		// HTTP. Do not retain bodies while a proven outage exceeds the wait budget.
		immediate := p.config.wait <= 0 || reason == "unsupported_cached" ||
			reason == "dial_state_full" ||
			(reason == "dial_backoff" && now.Add(delay).After(deadline))
		// A different session/window cannot reuse another identity's sockets.
		// If there is no matching socket or handshake to wait for, keep the
		// complete request on the existing transport instead of delaying it
		// solely to obtain a new WS permit. Exact reuse and immediate permits
		// were considered above; neither identity nor account budgets change.
		recentHTTPError := p.dials[account] != nil && p.dials[account].httpErrorUntil.After(now)
		if !immediate && !recentHTTPError && (!reusable || keyCount == 0) {
			p.counters["cold_identity_fast_fallback"]++
			immediate = true
		}
		if immediate || !now.Before(deadline) {
			if waiter != nil && !now.Before(deadline) {
				p.counters["wait_timeout"]++
			}
			p.removeWaiterLocked(state, waiter)
			p.fallbackCountLocked(reason)
			p.mu.Unlock()
			closeDetached(stale)
			return nil, nil, nil
		}
		if waiter == nil {
			if p.waiting >= p.config.maxWaiters || len(state.waiters) >= p.config.accountWaiters {
				p.counters["wait_queue_full"]++
				p.fallbackCountLocked(reason)
				p.mu.Unlock()
				closeDetached(stale)
				return nil, nil, nil
			}
			if l.requestBytes > p.config.maxWaitingBytes-p.waitingBytes {
				p.counters["wait_bytes_full"]++
				p.fallbackCountLocked(reason)
				p.mu.Unlock()
				closeDetached(stale)
				return nil, nil, nil
			}
			waiter = &capacityWaiter{key: key, bytes: l.requestBytes}
			state.waiters = append(state.waiters, waiter)
			p.waiting++
			p.waitingBytes += l.requestBytes
			p.counters["wait_started"]++
		}
		if delay > deadline.Sub(now) {
			delay = deadline.Sub(now)
		}
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		wake := p.changed
		p.mu.Unlock()
		closeDetached(stale)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
		case <-p.stop:
		case <-wake:
		case <-timer.C:
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
}
func (p *Pool) finishOpeningLocked(key, account string) {
	p.opening--
	p.openingKeys[key]--
	if p.openingKeys[key] == 0 {
		delete(p.openingKeys, key)
	}
	if st := p.accounts[account]; st != nil {
		st.opening--
	}
	p.signalLocked()
}
