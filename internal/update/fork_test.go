package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForkLatestReadsItsOwnRelease(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	sums := hash + "  magpie-darwin-arm64.zip\n"
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := forkSigningKey
	forkSigningKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { forkSigningKey = oldKey })
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("magpie release 0.1.5000\n"+sums)))
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/me/magpie/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.1.5000","body":"**notes**","html_url":"https://github.com/me/magpie/releases/tag/v0.1.5000","assets":[
				{"name":"magpie-darwin-arm64.zip","url":%q,"browser_download_url":%q,"size":123},
				{"name":"SHA256SUMS.txt","url":%q,"browser_download_url":%q,"size":50},
				{"name":"SHA256SUMS.txt.sig","browser_download_url":%q}]}`,
				srv.URL+"/api/app", srv.URL+"/dl/app.zip", srv.URL+"/api/sums", srv.URL+"/dl/sums", srv.URL+"/dl/sig")
		case "/dl/sums":
			fmt.Fprint(w, sums)
		case "/dl/sig":
			fmt.Fprintln(w, sig)
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
	if !ok || a.SHA256 != hash || a.Size != 123 || a.URL != srv.URL+"/dl/app.zip" || a.API != srv.URL+"/api/app" || a.attest {
		t.Fatalf("%+v", a)
	}
	if len(rel.Assets) != 1 || rel.Signed == nil || rel.Signed.Sums != sums {
		t.Fatalf("unexpected signed assets: %+v", rel)
	}
}

func TestForkLatestRejectsUntrustedRelease(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const version = "0.1.5000"
	const sums = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  magpie-darwin-arm64.zip\n"
	sign := func(key ed25519.PrivateKey, version, sums string) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte("magpie release "+version+"\n"+sums)))
	}
	key := base64.StdEncoding.EncodeToString(pub)
	sig := sign(priv, version, sums)
	tests := []struct {
		name    string
		key     string
		sums    string
		sig     string
		status  int
		missing string
		want    string
	}{
		{name: "tampered sums", key: key, sums: strings.ReplaceAll(sums, "01234567", "89abcdef"), sig: sig, want: "signature does not match"},
		{name: "tampered signature", key: key, sums: sums, sig: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)), want: "signature does not match"},
		{name: "other version", key: key, sums: sums, sig: sign(priv, "0.1.5001", sums), want: "signature does not match"},
		{name: "official key cannot sign fork", key: key, sums: sums, sig: sign(otherPriv, version, sums), want: "signature does not match"},
		{name: "missing key", sums: sums, sig: sig, want: "signing key is not valid"},
		{name: "malformed key", key: "not base64", sums: sums, sig: sig, want: "signing key is not valid"},
		{name: "short key", key: base64.StdEncoding.EncodeToString([]byte("short")), sums: sums, sig: sig, want: "signing key is not valid"},
		{name: "missing signature asset", key: key, sums: sums, missing: "SHA256SUMS.txt.sig", want: "has no SHA256SUMS.txt.sig"},
		{name: "missing sums asset", key: key, missing: "SHA256SUMS.txt", want: "has no SHA256SUMS.txt"},
		{name: "empty signature", key: key, sums: sums, want: "signature is not valid"},
		{name: "malformed signature", key: key, sums: sums, sig: "not base64", want: "signature is not valid"},
		{name: "short signature", key: key, sums: sums, sig: base64.StdEncoding.EncodeToString([]byte("short")), want: "signature is not valid"},
		{name: "signature download fails", key: key, sums: sums, sig: sig, status: http.StatusNotFound, want: "SHA256SUMS.txt.sig:"},
		{name: "invalid signed checksum", key: key, sums: "bad  magpie-darwin-arm64.zip\n", sig: sign(priv, version, "bad  magpie-darwin-arm64.zip\n"), want: "has no magpie-darwin-arm64.zip"},
		{name: "app not signed", key: key, sums: "", sig: sign(priv, version, ""), want: "has no magpie-darwin-arm64.zip"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldAPI, oldRepo, oldOS, oldArch, oldKey, oldOfficial := githubAPI, forkRepo, goos, goarch, forkSigningKey, signingKey
			t.Cleanup(func() {
				githubAPI, forkRepo, goos, goarch, forkSigningKey, signingKey = oldAPI, oldRepo, oldOS, oldArch, oldKey, oldOfficial
			})
			t.Setenv("MAGPIE_UPDATE_FEED", "http://127.0.0.1/feed")
			forkRepo, goos, goarch, forkSigningKey = "me/magpie", "darwin", "arm64", test.key
			signingKey = base64.StdEncoding.EncodeToString(otherPub)
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/me/magpie/releases/latest":
					assets := []map[string]any{}
					for name, path := range map[string]string{
						"magpie-darwin-arm64.zip": "/app",
						"SHA256SUMS.txt":          "/sums",
						"SHA256SUMS.txt.sig":      "/sig",
					} {
						if name != test.missing {
							assets = append(assets, map[string]any{"name": name, "browser_download_url": srv.URL + path})
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + version, "assets": assets})
				case "/sums":
					fmt.Fprint(w, test.sums)
				case "/sig":
					if test.status != 0 {
						w.WriteHeader(test.status)
					}
					fmt.Fprint(w, test.sig)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			githubAPI = srv.URL
			if rel, err := forkLatest(context.Background(), ""); err == nil || !strings.Contains(err.Error(), test.want) || rel != nil {
				t.Fatalf("release = %+v, error = %v; want %q", rel, err, test.want)
			}
		})
	}
}

func TestOfficialReleaseRejectsForkKey(t *testing.T) {
	rel := signedRelease(t, "0.1.5000", map[string]string{"a": "1"})
	oldRepo, oldKey := forkRepo, forkSigningKey
	t.Cleanup(func() { forkRepo, forkSigningKey = oldRepo, oldKey })
	forkRepo, forkSigningKey = "", signingKey
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signingKey = base64.StdEncoding.EncodeToString(pub)
	if err := rel.verify(); err == nil {
		t.Fatal("official release accepted a fork signature")
	}
}

func TestForkReleaseRequiresSignature(t *testing.T) {
	old := forkRepo
	t.Cleanup(func() { forkRepo = old })
	forkRepo = "me/magpie"
	for _, version := range []string{"0.1.3000", "0.1.5000"} {
		for _, feed := range []string{"", "http://127.0.0.1/feed"} {
			t.Setenv("MAGPIE_UPDATE_FEED", feed)
			if err := (&Release{Version: version}).verify(); err == nil {
				t.Fatalf("unsigned fork release %s accepted with feed %q", version, feed)
			}
		}
	}
	forkRepo = ""
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	if err := (&Release{Version: "0.1.5000"}).verify(); err == nil {
		t.Fatal("the official feed still needs its signature")
	}
}
