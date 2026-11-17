package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForkLiveReleaseStageAndInstall(t *testing.T) {
	if os.Getenv("MAGPIE_TEST_FORK_LIVE") != "1" {
		t.Skip("set MAGPIE_TEST_FORK_LIVE=1 to download the fork's current release")
	}
	oldRepo, oldKey := forkRepo, forkSigningKey
	forkRepo = "Cuihongsen/magpie"
	forkSigningKey = os.Getenv("MAGPIE_TEST_FORK_PUBLIC_KEY")
	if forkSigningKey == "" {
		t.Fatal("set MAGPIE_TEST_FORK_PUBLIC_KEY to the trusted fork public key")
	}
	t.Cleanup(func() { forkRepo, forkSigningKey = oldRepo, oldKey })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rel, err := forkLatest(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("release %s, asset %s", rel.Version, AppAsset())
	bundle := filepath.Join(t.TempDir(), "magpie.app")
	fakeApp(t, bundle, "old")
	stubUnregister(t)
	staged, err := Stage(ctx, rel, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(staged, bundle); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", bundle).CombinedOutput(); err != nil {
		t.Fatalf("verify installed bundle: %v: %s", err, output)
	}
	version, err := exec.CommandContext(ctx, "plutil", "-extract", "CFBundleShortVersionString", "raw", filepath.Join(bundle, "Contents", "Info.plist")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(version)) != rel.Version {
		t.Fatalf("installed version %q, release %q", version, rel.Version)
	}
}
