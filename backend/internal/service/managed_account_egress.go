package service

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func managedAccountOwnerID(a *Account) int64 {
	if a == nil {
		return 0
	}
	if a.ParentAccountID != nil {
		return *a.ParentAccountID
	}
	return a.ID
}

// IsRetiredVMRoute only recognizes legacy isolation state; it cannot enable VM transport.
func (a *Account) IsRetiredVMRoute() bool {
	if a == nil {
		return false
	}
	for _, key := range []string{"openai_vm_bound", "openai_vm_retired"} {
		if value, ok := a.Extra[key].(bool); ok && value {
			return true
		}
		if value, ok := a.Extra[key].(string); ok && value == "true" {
			return true
		}
	}
	return false
}

var errRetiredVMRoute = infraerrors.Conflict("OPENAI_VM_RETIRED", "This account retains a retired VM binding; restore its original route using the previous version before enabling it")

type retiredVMAccountChecker interface {
	HasRetiredVMRoute(context.Context, int64) (bool, error)
}
type retiredVMProxyChecker interface {
	IsRetiredVMProxy(context.Context, int64) (bool, error)
}

type ovhIPv6RouteChecker interface {
	CheckOVHIPv6Route(context.Context, *Account) error
}

// Check the authoritative binding as well as markers so stale snapshots cannot
// turn a retired VM account into native HTTP, WS, a plugin, BPS or direct egress.
func rejectRetiredVMAccount(ctx context.Context, repo AccountRepository, a *Account) error {
	if a == nil {
		return nil
	}
	if a.IsRetiredVMRoute() {
		return errRetiredVMRoute
	}
	if check, ok := repo.(retiredVMAccountChecker); ok && a.ID > 0 {
		blocked, err := check.HasRetiredVMRoute(ctx, a.ID)
		if err != nil {
			return errors.New("cannot verify retired account route; outbound request blocked")
		}
		if blocked {
			return errRetiredVMRoute
		}
	}
	return nil
}

func validateManagedAccountProxy(a *Account, p *Proxy) error {
	if a == nil {
		return nil
	}
	if a.IsRetiredVMRoute() {
		return errRetiredVMRoute
	}
	return validateOpenAIIPv6Proxy(a, p)
}

func resolveOpenAIAccountProxyURL(ctx context.Context, repo ProxyRepository, a *Account) (string, error) {
	if a == nil {
		return "", nil
	}
	if a.IsRetiredVMRoute() {
		return "", errRetiredVMRoute
	}
	if check, ok := repo.(ovhIPv6RouteChecker); ok {
		if err := check.CheckOVHIPv6Route(ctx, a); err != nil {
			return "", err
		}
	}
	var p *Proxy
	if a.ProxyID != nil {
		if check, ok := repo.(retiredVMProxyChecker); ok {
			blocked, err := check.IsRetiredVMProxy(ctx, *a.ProxyID)
			if err != nil {
				return "", errors.New("cannot verify managed proxy; direct fallback is disabled")
			}
			if blocked {
				return "", errRetiredVMRoute
			}
		}
		p = a.Proxy
		if repo != nil {
			loaded, err := repo.GetByID(ctx, *a.ProxyID)
			if err == nil {
				p = loaded
			} else if a.IsManagedIPv6Bound() {
				return "", errors.New("managed proxy lookup failed; direct fallback is disabled")
			}
		}
	}
	if err := validateManagedAccountProxy(a, p); err != nil {
		return "", err
	}
	if p != nil {
		return p.URL(), nil
	}
	return "", nil
}

func validateManagedEgressEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", infraerrors.BadRequest("MANAGED_EGRESS_INVALID_URL", "Use an HTTP(S) managed egress origin without credentials, path or query")
	}
	// A shared credential must not cross an untrusted network in cleartext.
	ip := net.ParseIP(u.Hostname())
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", infraerrors.BadRequest("MANAGED_EGRESS_INVALID_URL", "Managed egress port must be between 1 and 65535")
		}
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || (!ip.IsLoopback() && !ip.IsPrivate())) {
		return "", infraerrors.BadRequest("MANAGED_EGRESS_TLS_REQUIRED", "Public managed egress endpoints require HTTPS; HTTP is limited to loopback/private IPs")
	}
	u.Path = ""
	return u.String(), nil
}
