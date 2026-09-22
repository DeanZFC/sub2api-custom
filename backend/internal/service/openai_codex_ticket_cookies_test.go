package service

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func codexCookieHeaders(values ...string) http.Header {
	return http.Header{"Set-Cookie": values}
}

func codexCookieTestTime() time.Time {
	return time.Date(2026, 9, 22, 4, 0, 0, 0, time.UTC)
}

type codexCookieRepo struct {
	AccountRepository
	mu      sync.Mutex
	writes  []*openAICodexTicketCookies
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *codexCookieRepo) UpdateExtra(_ context.Context, accountID int64, updates map[string]any) error {
	if accountID == 1 {
		r.once.Do(func() {
			if r.started != nil {
				close(r.started)
				<-r.release
			}
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, updates[openAICodexTicketCookiesExtraKey].(*openAICodexTicketCookies))
	return nil
}

func TestCodexTicketCookiesCaptureScopeAndExpiry(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 3600}, nil)
	account := ticketTestAccount(1)
	repo := &codexCookieRepo{}
	svc.accountRepo = repo
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders(
		"__cflb=route-a; Domain=.chatgpt.com; Path=/; Secure; HttpOnly; Max-Age=3600",
		"__oailb=route-b; Path=/backend-api/codex; Expires="+now.Add(75*time.Second).Format(http.TimeFormat),
		"session=must-not-be-copied; Path=/",
	), now)
	cookies := svc.lookupOpenAICodexTicketCookies(account)
	require.Equal(t, "__cflb=route-a; __oailb=route-b", cookies.header(now))
	require.Equal(t, now.Add(240*time.Second), cookies.CFLB.ExpiresAt)
	require.Equal(t, now.Add(75*time.Second), cookies.expiresAt())
	require.Empty(t, cookies.header(now.Add(75*time.Second)))
	require.Len(t, repo.writes, 1)
	require.Nil(t, account.Extra, "capture must not mutate shared account snapshots")
	encoded, err := json.Marshal(repo.writes[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "must-not-be-copied")

	svc.captureOpenAICodexTicketCookies(context.Background(), account, nil, now.Add(time.Minute))
	require.Len(t, repo.writes, 1, "absence must not extend cookie lifetime or write to DB")
	require.Equal(t, cookies.expiresAt(), svc.lookupOpenAICodexTicketCookies(account).expiresAt())
}

func TestCodexTicketCookiesRejectInvalidHeadersAndScopes(t *testing.T) {
	for _, invalid := range []string{
		"__cflb=bad; Domain=other.example; Path=/",
		"__cflb=bad; Domain=.com; Path=/",
		"__cflb=bad; Domain=chatgpt.com.evil; Path=/",
		"__cflb=bad; Domain=chatgpt.com.; Path=/",
		"__cflb=bad; Path=/backend-api/code",
		"__cflb=bad; Path=/backend-api/codex/responses/",
		"__cflb=bad; Path=/other",
		"__cflb=bad\r\nX-Injected: true",
		"__cflb=bad\x00value",
		"__cflb=bad value; Path=/",
		"__cflb=bad,value; Path=/",
		"__cflb=bad\\value; Path=/",
	} {
		t.Run(invalid, func(t *testing.T) {
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
			account := ticketTestAccount(1)
			svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders(invalid, "__oailb=b; Path=/"), codexCookieTestTime())
			cookies := svc.lookupOpenAICodexTicketCookies(account)
			require.NotNil(t, cookies)
			require.Empty(t, cookies.header(codexCookieTestTime()))
			require.Empty(t, cookies.CFLB.Value)
		})
	}
}

func TestCodexTicketCookiesPartialRefreshDoesNotExtendPartner(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
	account := ticketTestAccount(1)
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=a", "__oailb=b"), now)
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=new-a"), now.Add(time.Minute))
	cookies := svc.lookupOpenAICodexTicketCookies(account)
	require.Equal(t, now.Add(300*time.Second), cookies.CFLB.ExpiresAt)
	require.Equal(t, now.Add(240*time.Second), cookies.OAILB.ExpiresAt)
	require.Equal(t, "__cflb=new-a; __oailb=b", cookies.header(now.Add(time.Minute)))
	require.Empty(t, cookies.header(now.Add(240*time.Second)))
}

func TestCodexTicketCookiesDeletionPreventsSnapshotResurrection(t *testing.T) {
	for _, deletion := range []string{
		"__cflb=old; Max-Age=0",
		"__cflb=old; Max-Age=-10",
		"__cflb=old; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		"__cflb=; Path=/",
	} {
		t.Run(deletion, func(t *testing.T) {
			now := codexCookieTestTime()
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
			account := ticketTestAccount(1)
			repo := &codexCookieRepo{}
			svc.accountRepo = repo
			svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=a", "__oailb=b"), now)
			account.Extra = map[string]any{openAICodexTicketCookiesExtraKey: repo.writes[0]}
			svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders(deletion), now.Add(time.Second))
			current := svc.lookupOpenAICodexTicketCookies(account)
			require.Empty(t, current.header(now.Add(2*time.Second)))
			require.Empty(t, current.CFLB.Value)
			require.Equal(t, now.Add(time.Second), current.UpdatedAt)
			require.Len(t, repo.writes, 2)
			// Restart with the deletion record, then encounter a stale snapshot.
			restarted := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
			fresh := *account
			fresh.Extra = map[string]any{openAICodexTicketCookiesExtraKey: repo.writes[1]}
			require.Empty(t, restarted.lookupOpenAICodexTicketCookies(&fresh).header(now.Add(2*time.Second)))
			require.Empty(t, restarted.lookupOpenAICodexTicketCookies(account).header(now.Add(2*time.Second)))
		})
	}
}

func TestCodexTicketCookiesIdentityAndExitIsolation(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
	account := ticketTestAccount(1)
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=a", "__oailb=b"), now)
	account.Extra = map[string]any{openAICodexTicketCookiesExtraKey: svc.lookupOpenAICodexTicketCookies(account)}
	for _, change := range []struct {
		name string
		edit func(*Account)
	}{
		{"different account", func(a *Account) { a.ID++ }},
		{"different workspace", func(a *Account) { a.Credentials["chatgpt_account_id"] = "another" }},
		{"different proxy", func(a *Account) {
			id := int64(3)
			a.ProxyID = &id
			a.Proxy = &Proxy{ID: id, Protocol: "http", Host: "proxy.example", Port: 8080}
		}},
		{"unavailable proxy", func(a *Account) { id := int64(3); a.ProxyID = &id }},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := *account
			changed.Credentials = maps.Clone(account.Credentials)
			change.edit(&changed)
			require.Nil(t, svc.lookupOpenAICodexTicketCookies(&changed))
		})
	}
	account.Credentials["access_token"] = "refreshed-token"
	require.NotEmpty(t, svc.lookupOpenAICodexTicketCookies(account).header(now))
}

func TestCodexTicketCookiesRepeatedDeletionRejectsOutOfOrderResponse(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
	account := ticketTestAccount(1)
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=a", "__oailb=b"), now)
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=; Max-Age=0"), now.Add(10*time.Second))
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=; Max-Age=0"), now.Add(20*time.Second))
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=stale"), now.Add(15*time.Second))
	cookies := svc.lookupOpenAICodexTicketCookies(account)
	require.Equal(t, now.Add(20*time.Second), cookies.CFLB.CapturedAt)
	require.Empty(t, cookies.CFLB.Value)
	require.Empty(t, cookies.header(now.Add(25*time.Second)))
}

func TestCodexTicketCookiesRespectsConfiguredTTLAndMaxAge(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 120}, nil)
	account := ticketTestAccount(1)
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=a; Max-Age=3600", "__oailb=b; Max-Age=30"), now)
	cookies := svc.lookupOpenAICodexTicketCookies(account)
	require.Equal(t, now.Add(120*time.Second), cookies.CFLB.ExpiresAt)
	require.Equal(t, now.Add(30*time.Second), cookies.expiresAt())
	svc.cfg.Gateway.OpenAICodexTicket.TTLSeconds = 15
	require.Equal(t, now.Add(15*time.Second), svc.lookupOpenAICodexTicketCookies(account).expiresAt())
}

func TestCodexTicketCookiesPersistedValuesCannotBypassValidation(t *testing.T) {
	now := codexCookieTestTime()
	account := ticketTestAccount(1)
	valid := openAICodexTicketCookies{
		AccountID: account.ID, Scope: openAICodexTicketScope(account), UpdatedAt: now,
		CFLB:  openAICodexTicketCookie{Value: "a", CapturedAt: now, ExpiresAt: now.Add(time.Hour)},
		OAILB: openAICodexTicketCookie{Value: "b", CapturedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	account.Extra = map[string]any{openAICodexTicketCookiesExtraKey: valid}
	restored := parseOpenAICodexTicketCookies(account)
	require.Equal(t, now.Add(240*time.Second), restored.expiresAt())
	require.Empty(t, restored.header(now.Add(240*time.Second)))
	for _, edit := range []func(*openAICodexTicketCookies){
		func(c *openAICodexTicketCookies) { c.CFLB.Value = "a\r\nX-Injected: yes" },
		func(c *openAICodexTicketCookies) { c.CFLB.Value = "a; session=bad" },
		func(c *openAICodexTicketCookies) { c.CFLB.CapturedAt = time.Time{} },
		func(c *openAICodexTicketCookies) { c.OAILB.CapturedAt = now.Add(time.Minute) },
	} {
		invalid := valid
		edit(&invalid)
		account.Extra[openAICodexTicketCookiesExtraKey] = invalid
		require.Empty(t, parseOpenAICodexTicketCookies(account).header(now))
	}
}

func TestCodexTicketCookiesConcurrentUpdatesPersistInOrderAndLookupDoesNotBlock(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
	account := ticketTestAccount(1)
	repo := &codexCookieRepo{started: make(chan struct{}), release: make(chan struct{})}
	svc.accountRepo = repo
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__cflb=a"), now)
	}()
	<-repo.started
	lookup := make(chan *openAICodexTicketCookies, 1)
	go func() { lookup <- svc.lookupOpenAICodexTicketCookies(account) }()
	select {
	case got := <-lookup:
		require.Equal(t, "a", got.CFLB.Value)
	case <-time.After(time.Second):
		close(repo.release)
		t.Fatal("lookup blocked on persistence")
	}
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__oailb=b"), now.Add(time.Second))
	}()
	close(repo.release)
	<-firstDone
	<-secondDone
	require.Len(t, repo.writes, 2)
	require.Equal(t, "__cflb=a; __oailb=b", repo.writes[1].header(now.Add(time.Second)))
	require.Equal(t, "__cflb=a; __oailb=b", svc.lookupOpenAICodexTicketCookies(account).header(now.Add(time.Second)))
	// Reprocessing an identical response is not a persisted change.
	svc.captureOpenAICodexTicketCookies(context.Background(), account, codexCookieHeaders("__oailb=b"), now.Add(time.Second))
	require.Len(t, repo.writes, 2)
}

func TestCodexTicketCookiesSlowPersistenceDoesNotBlockOtherAccounts(t *testing.T) {
	now := codexCookieTestTime()
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
	repo := &codexCookieRepo{started: make(chan struct{}), release: make(chan struct{})}
	svc.accountRepo = repo
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		svc.captureOpenAICodexTicketCookies(context.Background(), ticketTestAccount(1), codexCookieHeaders("__cflb=a", "__oailb=b"), now)
	}()
	<-repo.started
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		svc.captureOpenAICodexTicketCookies(context.Background(), ticketTestAccount(2), codexCookieHeaders("__cflb=c", "__oailb=d"), now)
	}()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		close(repo.release)
		t.Fatal("one account's persistence blocked another account")
	}
	close(repo.release)
	<-firstDone
	require.Equal(t, "__cflb=c; __oailb=d", svc.lookupOpenAICodexTicketCookies(ticketTestAccount(2)).header(now))
}
