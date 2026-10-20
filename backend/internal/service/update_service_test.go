//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/updatepolicy"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	manifests         map[string][]byte
	release           *GitHubRelease
	recentReleases    []*GitHubRelease
	recentErr         error
	repositoryFile    []byte
	repositoryFileErr error
	latestRepo        string
	fileRepo          string
	fileRef           string
	filePath          string
	fileCalls         int
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.latestRepo = repo
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error) {
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) FetchRepositoryFile(_ context.Context, repo, ref, filePath string) ([]byte, error) {
	s.fileCalls++
	s.fileRepo = repo
	s.fileRef = ref
	s.filePath = filePath
	return s.repositoryFile, s.repositoryFileErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(_ context.Context, url string) ([]byte, error) {
	data, ok := s.manifests[url]
	if !ok {
		return nil, errors.New("manifest missing")
	}
	return data, nil
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func publishedFixture(version string) (*GitHubRelease, []byte) {
	url, _ := updatepolicy.AssetURL(version)
	raw, _ := json.Marshal(updatepolicy.Manifest{SchemaVersion: 1, Version: version, Revision: strings.Repeat("a", 40), Images: map[string]string{runtime.GOARCH: updatepolicy.ImageRepository + "@sha256:" + strings.Repeat("b", 64)}})
	return &GitHubRelease{TagName: "v" + version, Prerelease: true, HTMLURL: "https://github.com/" + githubRepo + "/releases/tag/v" + version, Assets: []GitHubAsset{{Name: updatepolicy.AssetName, BrowserDownloadURL: url, Size: int64(len(raw))}}}, raw
}
func TestUpdateServiceAdvertisesOnlyReadyPublishedReleases(t *testing.T) {
	ready, raw := publishedFixture("0.2.17-custom.13")
	client := &updateServiceGitHubClientStub{recentReleases: []*GitHubRelease{{TagName: "v0.2.17-custom.14"}, ready}, manifests: map[string][]byte{ready.Assets[0].BrowserDownloadURL: raw}}
	for _, build := range []string{"source", "release"} {
		svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.17-custom.12", build)
		info, err := svc.CheckUpdate(context.Background(), true)
		require.NoError(t, err)
		require.True(t, info.HasUpdate)
		require.Equal(t, "0.2.17-custom.13", info.LatestVersion)
		require.Equal(t, ready.HTMLURL, info.ReleaseInfo.HTMLURL)
		require.Zero(t, client.fileCalls)
	}
}
func TestUpdateServiceIgnoresLegacyBranchCache(t *testing.T) {
	cache := &updateServiceCacheStub{data: `{"latest":"0.2.17-custom.99","timestamp":4102444800,"repository":"DeanZFC/sub2api-custom","build_type":"release"}`}
	client := &updateServiceGitHubClientStub{repositoryFile: []byte("v0.2.17-custom.99")}
	svc := NewUpdateService(cache, client, "0.2.17-custom.12", "release")
	info, err := svc.CheckUpdate(context.Background(), false)
	require.NoError(t, err)
	require.False(t, info.HasUpdate)
	require.Equal(t, "0.2.17-custom.12", info.LatestVersion)
	require.Zero(t, client.fileCalls)
}
func TestUpdateServiceManifestMismatchDoesNotAdvertiseUpdate(t *testing.T) {
	ready, raw := publishedFixture("0.2.17-custom.13")
	client := &updateServiceGitHubClientStub{recentReleases: []*GitHubRelease{ready}, manifests: map[string][]byte{ready.Assets[0].BrowserDownloadURL: []byte(strings.ReplaceAll(string(raw), "custom.13", "custom.14"))}}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.17-custom.12", "release")
	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.False(t, info.HasUpdate)
	require.Contains(t, info.Warning, "mismatch")
}
func TestUpdateServiceSourceBuildRejectsBinaryUpdate(t *testing.T) {
	svc := NewUpdateService(&updateServiceCacheStub{}, &updateServiceGitHubClientStub{}, "0.2.17-custom.12", "source")
	require.ErrorIs(t, svc.PerformUpdate(context.Background()), ErrSourceBuildUpdateRequired)
}

type offlineUpdateAgent struct{}

func (offlineUpdateAgent) Health(context.Context) error { return errors.New("offline") }
func (offlineUpdateAgent) Start(context.Context, string, string) (SourceUpdateJob, error) {
	return SourceUpdateJob{}, ErrSourceUpdaterUnavailable
}
func (offlineUpdateAgent) Status(context.Context, string) (SourceUpdateJob, error) {
	return SourceUpdateJob{}, ErrSourceUpdaterUnavailable
}
func TestManagedReleaseNeverFallsBackToBinaryMutation(t *testing.T) {
	svc := NewUpdateService(&updateServiceCacheStub{}, &updateServiceGitHubClientStub{}, "0.2.17-custom.12", "release")
	svc.SetSourceUpdateAgent(offlineUpdateAgent{})
	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.True(t, info.HostUpdateManaged)
	require.False(t, info.SourceUpdateEnabled)
	require.ErrorIs(t, svc.PerformUpdate(context.Background()), ErrSourceUpdaterUnavailable)
	require.Error(t, svc.Rollback())
	require.Error(t, svc.RollbackToVersion(context.Background(), "0.2.17-custom.11"))
}

func TestCompareVersionsSupportsForkPrereleaseRevisions(t *testing.T) {
	require.Less(t, compareVersions("0.1.176-overdraft.1", "0.1.176-overdraft.2"), 0)
	require.Zero(t, compareVersions("v0.1.176-overdraft.2", "0.1.176-overdraft.2"))
	require.Greater(t, compareVersions("0.1.177-overdraft.1", "0.1.176-overdraft.9"), 0)
	require.Less(t, compareVersions("0.1.183-overdraft.8", "0.1.184-custom.1"), 0)
	require.Less(t, compareVersions("0.1.184-custom.1", "0.1.184-custom.2"), 0)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}
