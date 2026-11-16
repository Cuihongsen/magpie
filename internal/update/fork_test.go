package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForkLatestReadsItsOwnRelease(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/me/magpie/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.1.5000","body":"**notes**","html_url":"https://github.com/me/magpie/releases/tag/v0.1.5000","assets":[
				{"name":"magpie-darwin-arm64.zip","url":%q,"browser_download_url":%q,"size":123},
				{"name":"SHA256SUMS.txt","url":%q,"browser_download_url":%q,"size":50}]}`,
				srv.URL+"/api/app", srv.URL+"/dl/app.zip", srv.URL+"/api/sums", srv.URL+"/dl/sums")
		case "/dl/sums":
			fmt.Fprintf(w, "%s  magpie-darwin-arm64.zip\n", hash)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldAPI, oldRepo, oldOS, oldArch := githubAPI, forkRepo, goos, goarch
	githubAPI, forkRepo, goos, goarch = srv.URL, "me/magpie", "darwin", "arm64"
	t.Cleanup(func() { githubAPI, forkRepo, goos, goarch = oldAPI, oldRepo, oldOS, oldArch })

	rel, err := forkLatest(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.1.5000" || !strings.Contains(rel.Notes, "notes") {
		t.Fatalf("%s %q", rel.Version, rel.Notes)
	}
	a, ok := rel.Assets["magpie-darwin-arm64.zip"]
	if !ok || a.SHA256 != hash || a.Size != 123 || a.URL != srv.URL+"/dl/app.zip" || a.API != srv.URL+"/api/app" {
		t.Fatalf("%+v", a)
	}
	if _, ok := rel.Assets["SHA256SUMS.txt"]; ok {
		t.Fatal("the sums list is not an installable file")
	}
}

func TestForkReleaseNeedsNoOfficialSignature(t *testing.T) {
	old := forkRepo
	t.Cleanup(func() { forkRepo = old })
	forkRepo = "me/magpie"
	if err := (&Release{Version: "0.1.5000"}).verify(); err != nil {
		t.Fatalf("a fork release: %v", err)
	}
	forkRepo = ""
	if err := (&Release{Version: "0.1.5000"}).verify(); err == nil {
		t.Fatal("the official feed still needs its signature")
	}
}
