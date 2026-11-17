package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yetone/magpie/internal/source"
)

// forkRepo, set at build time, makes this build read and install its own
// GitHub releases instead of the official feed:
//
//	-X github.com/yetone/magpie/internal/update.forkRepo=Cuihongsen/magpie
//
// The fork's CI publishes magpie-darwin-arm64.zip and SHA256SUMS.txt on
// each tag; the release is read from GitHub's API and each file is
// checked against that list. No ed25519 signature is asked for: the
// official key lives only in yetone/magpie-releases' secrets, and this
// fork's releases are built by its owner's workflow (GitHub's own
// attestation of the build is still there to look at). The bundle's code
// signature is checked as ever, so an ad-hoc signed app replaces only an
// ad-hoc signed one.
//
// A var, and not a const, so a test can set it and a release build can
// name another repository.
var forkRepo = ""

// Fork reports whether this build updates from its own GitHub releases.
func Fork() bool { return forkRepo != "" }

// forkSums is the file the fork's release carries its hashes in.
const forkSums = "SHA256SUMS.txt"

// forkLatest is githubLatest for the fork's own repository: the newest
// release, its files named by SHA256SUMS.txt, no signature required.
func forkLatest(ctx context.Context, lang string) (*Release, error) {
	rel, err := forkRelease(ctx)
	if err != nil {
		return nil, err
	}
	sumsAsset, ok := rel.Assets[forkSums]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Version, forkSums)
	}
	sums, err := githubFile(ctx, sumsAsset.URL, sumsAsset.API)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", forkSums, err)
	}
	byName := map[string]Asset{}
	for name, a := range rel.Assets {
		byName[name] = a
	}
	rel.Assets = map[string]Asset{}
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		name := strings.TrimPrefix(f[1], "*")
		a, ok := byName[name]
		if !ok || name == forkSums || strings.ContainsAny(name, `/\`) {
			continue
		}
		a.SHA256 = strings.ToLower(f[0])
		rel.Assets[name] = a
	}
	if _, ok := rel.Assets[AppAsset()]; !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Version, AppAsset())
	}
	if err := rel.verify(); err != nil {
		return nil, err
	}
	rel.Notes = InLang(rel.Notes, lang)
	return rel, nil
}

// forkRelease is the newest release as the fork's GitHub API gives it,
// every file it lists with its github.com and API addresses.
func forkRelease(ctx context.Context) (*Release, error) {
	api := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPI, forkRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := source.Do(client, req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fork release: %s", res.Status)
	}
	var in struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			URL                string `json:"url"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(res.Body).Decode(&in); err != nil {
		return nil, fmt.Errorf("fork release: %w", err)
	}
	version := strings.TrimPrefix(in.TagName, "v")
	if parse(version) == nil {
		return nil, fmt.Errorf("fork release: tag %q is no version", in.TagName)
	}
	rel := &Release{Version: version, Notes: in.Body, URL: in.HTMLURL, Assets: map[string]Asset{}}
	for _, a := range in.Assets {
		name := a.Name
		if name == "" || strings.ContainsAny(name, `/\`) {
			continue
		}
		u := a.BrowserDownloadURL
		if u == "" {
			u = fmt.Sprintf("%s/%s/releases/download/%s/%s", githubWeb, forkRepo, in.TagName, url.PathEscape(name))
		}
		rel.Assets[name] = Asset{URL: u, API: a.URL, Size: a.Size}
	}
	return rel, nil
}

// forkRepoName is the repository releases are read from, the official one
// unless this build is a fork's.
func forkRepoName() string {
	if forkRepo != "" {
		return forkRepo
	}
	return releaseRepo
}
