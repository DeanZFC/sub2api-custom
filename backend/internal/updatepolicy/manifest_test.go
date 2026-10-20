package updatepolicy

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestManifestPolicy(t *testing.T) {
	valid := Manifest{SchemaVersion: 1, Version: "0.2.17-custom.13", Revision: strings.Repeat("a", 40), Images: map[string]string{"amd64": ImageRepository + "@sha256:" + strings.Repeat("b", 64)}}
	data, _ := json.Marshal(valid)
	_, err := Parse(data, "v0.2.17-custom.13")
	require.NoError(t, err)
	for _, mutate := range []func(*Manifest){func(m *Manifest) { m.SchemaVersion = 2 }, func(m *Manifest) { m.Version = "0.2.17-custom.14" }, func(m *Manifest) { m.Revision = "main" }, func(m *Manifest) { m.Images = map[string]string{"amd64": ImageRepository + ":latest"} }, func(m *Manifest) {
		m.Images = map[string]string{"amd64": "evil.example/app@sha256:" + strings.Repeat("b", 64)}
	}} {
		m := valid
		mutate(&m)
		data, _ := json.Marshal(m)
		_, err := Parse(data, valid.Version)
		require.Error(t, err)
	}
	_, err = AssetURL("../../other")
	require.Error(t, err)
	_, err = Parse([]byte(strings.Repeat(" ", MaxManifestSize+1)), valid.Version)
	require.Error(t, err)
}
