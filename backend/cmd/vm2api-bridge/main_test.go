package main

import (
	"strings"
	"testing"
	"time"
)

func TestSettingsDefaultNamespaceIsStableAndSecretsAreRequired(t *testing.T) {
	env := map[string]string{"VM2API_BRIDGE_SECRET": "long-random-bridge-secret-for-tests-only", "VM2API_URL": "http://127.0.0.1:8000", "VM2API_MASTER_KEY": "private-admin-key"}
	get := func(key string) string { return env[key] }
	first, err := readSettings(get)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readSettings(get)
	if err != nil {
		t.Fatal(err)
	}
	if first.namespace == "" || first.namespace != second.namespace || strings.Contains(first.namespace, env["VM2API_BRIDGE_SECRET"]) {
		t.Fatal("namespace was not stable and opaque")
	}
	if first.listen != "127.0.0.1:8097" || first.drainTimeout != 30*time.Second {
		t.Fatal("unsafe configuration defaults")
	}
	env["VM2API_BRIDGE_DRAIN_TIMEOUT"] = "110s"
	third, err := readSettings(get)
	if err != nil || third.drainTimeout != 110*time.Second {
		t.Fatal("timeout override failed")
	}
	env["VM2API_BRIDGE_DRAIN_TIMEOUT"] = "-1s"
	if _, err := readSettings(get); err == nil {
		t.Fatal("accepted negative timeout")
	}
	delete(env, "VM2API_BRIDGE_DRAIN_TIMEOUT")
	env["VM2API_BRIDGE_SECRET"] = "short"
	if _, err := readSettings(get); err == nil {
		t.Fatal("accepted weak secret")
	}
}
