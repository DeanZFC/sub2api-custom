package adminaccess

import "testing"

func TestNewGatewayManagementRoutesRequireFullAdminAccess(t *testing.T) {
	policy := Policy{}
	for _, permission := range PermissionCatalog() {
		policy.GlobalPermissions = append(policy.GlobalPermissions, permission.ID)
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/accounts/codex-harvest-flow"}, {"GET", "/accounts/codex-harvest-controls"},
		{"PUT", "/accounts/codex-harvest-controls"}, {"GET", "/accounts/codex-harvest-nodes"},
		{"POST", "/accounts/codex-harvest-nodes/reset"}, {"PUT", "/accounts/:id/codex-skip-harvest"},
		{"POST", "/accounts/:id/manual-harvest"}, {"GET", "/system/mihomo"},
		{"POST", "/system/mihomo"}, {"PUT", "/system/mihomo/download-mode"},
	} {
		path := "/api/v1/admin" + route.path
		if RouteAllowed(policy, route.method, path) {
			t.Errorf("restricted key unexpectedly granted %s %s", route.method, path)
		}
		if !RouteAllowed(Policy{FullAccess: true}, route.method, path) {
			t.Errorf("full admin denied %s %s", route.method, path)
		}
	}
}
