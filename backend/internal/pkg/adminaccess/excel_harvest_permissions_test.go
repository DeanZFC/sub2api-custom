package adminaccess

import "testing"

func TestExcelAuthorizationAndHarvestRequireFullAdmin(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{"GET", "/accounts/:id/openai-reauth"}, {"POST", "/accounts/:id/openai-reauth"}, {"PUT", "/accounts/:id/openai-reauth/config"},
		{"GET", "/openai/credential-encryption"}, {"POST", "/openai/credential-encryption"}, {"GET", "/openai/reauth-runtime"},
		{"PUT", "/settings/astra-routing"}, {"POST", "/accounts/astra-gateway/test"}, {"POST", "/system/mihomo/nodes/static-proxies"},
	} {
		path := "/api/v1/admin" + route.path
		if RouteAllowed(Policy{}, route.method, path) {
			t.Errorf("restricted administrator reached %s %s", route.method, path)
		}
		if !RouteAllowed(Policy{FullAccess: true}, route.method, path) {
			t.Errorf("full administrator denied %s %s", route.method, path)
		}
	}
}
