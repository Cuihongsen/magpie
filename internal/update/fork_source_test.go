package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestForkDownloadUsesGitHubAPIWithSitePreference(t *testing.T) {
	previousRepo := forkRepo
	forkRepo = "Cuihongsen/magpie"
	t.Cleanup(func() { forkRepo = previousRepo })
	sourceFor(t, settings.UpdateSourceSite)
	github := newFakeGitHub(t)
	asset := github.asset0()
	asset.API = github.srv.URL + "/repos/yetone/magpie-releases/releases/assets/7"
	path := filepath.Join(t.TempDir(), "magpie.zip")
	if err := download(context.Background(), asset, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(github.body) || github.blob.Load() != 1 {
		t.Fatalf("download error %v, API downloads %d", err, github.blob.Load())
	}
}
