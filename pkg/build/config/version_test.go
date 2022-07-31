package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/grafana/grafana/pkg/build/config"
	"github.com/stretchr/testify/require"
)

func TestGetMetadata(t *testing.T) {
	tcs := []struct {
		version string
		mode    config.VersionMode
	}{
		{"v1.2.3", config.ReleaseMode},
		{"v1.2.3-12345pre", config.PullRequestMode},
		{"v1.2.3-beta1", config.BetaReleaseMode},
		{"v1.2.3-test1", config.TestReleaseMode},
		{"v1.2.3-foobar", config.ReleaseBranchMode},
		{"v1.2.3-foobar", config.MainMode},
	}

	t.Run("Should return empty metadata, dist/ is not present", func(t *testing.T) {
		dir := t.TempDir()
		metadata, err := config.GetMetadata(filepath.Join(dir, "dist"))
		require.NoError(t, err)
		require.Equal(t, metadata, &config.Metadata{})
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	})

	for _, tc := range tcs {
		dir := t.TempDir()
		t.Run("Should return valid metadata, tag mode, ", func(t *testing.T) {
			testMetadata(t, dir, tc.version, tc.mode)
		})
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func testMetadata(t *testing.T, dir string, version string, mode config.VersionMode) {
	t.Helper()
	file := filepath.Join(dir, "version.json")
	createVersionJSON(t, version, file, mode)

	metadata, err := config.GetMetadata(file)
	require.NoError(t, err)
	t.Run("with a valid version", func(t *testing.T) {
		expVersion := metadata.GrafanaVersion
		require.Equal(t, expVersion, version)
	})

	t.Run("with a valid release mode from the built-in list", func(t *testing.T) {
		expMode := metadata.ReleaseMode
		require.NoError(t, err)
		require.Equal(t, expMode, mode)
	})

	t.Run("with a valid configuration from a JSON file", func(t *testing.T) {
		version, err := config.GetVersion(metadata.ReleaseMode)
		require.NoError(t, err)
		parsed := verModeFromConfig(t, metadata)
		require.EqualValues(t, parsed, *version)
	})
}

func verModeFromConfig(t *testing.T, metadata *config.Metadata) config.Version {
	t.Helper()

	metadataComp := config.VersionMap{}

	require.NoError(t, json.Unmarshal(configJSON, &metadataComp))

	return metadataComp[metadata.ReleaseMode]
}

func createVersionJSON(t *testing.T, version string, file string, mode config.VersionMode) {
	t.Helper()

	metadata := &config.Metadata{
		GrafanaVersion: version,
		ReleaseMode:    mode,
	}

	//nolint:gosec
	f, err := os.Create(file)
	require.NoError(t, err)

	require.NoError(t, json.NewEncoder(f).Encode(metadata))
}
