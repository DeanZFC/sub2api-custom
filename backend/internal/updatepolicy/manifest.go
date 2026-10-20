// Package updatepolicy defines the release readiness contract shared by the
// application and the privileged host updater.
package updatepolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	Repository      = "DeanZFC/sub2api-custom"
	ImageRepository = "ghcr.io/deanzfc/sub2api"
	AssetName       = "update-manifest.json"
	UpdaterPath     = "/usr/local/libexec/sub2api-updater"
	MaxManifestSize = 64 << 10
)

var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+-custom\.[0-9]+$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Version       string            `json:"version"`
	Revision      string            `json:"revision"`
	Images        map[string]string `json:"images"` // Linux architecture -> immutable image reference
}

func ValidVersion(version string) bool { return versionPattern.MatchString(version) }
func AssetURL(version string) (string, error) {
	if !ValidVersion(version) {
		return "", errors.New("invalid custom release version")
	}
	return "https://github.com/" + Repository + "/releases/download/v" + strings.TrimPrefix(version, "v") + "/" + AssetName, nil
}
func Parse(data []byte, version string) (*Manifest, error) {
	if len(data) > MaxManifestSize {
		return nil, errors.New("update manifest too large")
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid update manifest: %w", err)
	}
	if m.SchemaVersion != 1 || !ValidVersion(m.Version) || strings.TrimPrefix(m.Version, "v") != strings.TrimPrefix(version, "v") || !revisionPattern.MatchString(m.Revision) {
		return nil, errors.New("update manifest version, schema or revision mismatch")
	}
	if len(m.Images) == 0 {
		return nil, errors.New("update manifest contains no images")
	}
	for arch, ref := range m.Images {
		if (arch != "amd64" && arch != "arm64") || !strings.HasPrefix(ref, ImageRepository+"@") || !digestPattern.MatchString(strings.TrimPrefix(ref, ImageRepository+"@")) {
			return nil, errors.New("update manifest contains an untrusted image")
		}
	}
	return &m, nil
}
func Fetch(ctx context.Context, version string) (*Manifest, error) {
	url, err := AssetURL(version)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return errors.New("unsafe release redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release manifest unavailable (HTTP %d); release may not be ready", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestSize+1))
	if err != nil {
		return nil, err
	}
	return Parse(data, version)
}
