package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSettingsUpdateURLRoundTrip(t *testing.T) {
	t.Cleanup(func() { setUpdateURL("") })
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := newAPI(store, fstest.MapFS{})
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	// a valid custom URL is stored and reaches the updater
	rec := put(`{"updateURL":"https://gitlab.example.com/infra/portanote"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", rec.Code, rec.Body.String())
	}
	if src, _ := resolveUpdateSource(); src == nil || src.kind != "gitlab" || src.host != "gitlab.example.com" {
		t.Fatalf("updater did not pick up the URL: %+v", src)
	}
	// an unusable URL is rejected and nothing changes
	if rec := put(`{"updateURL":"not a url"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad URL status = %d, want 400", rec.Code)
	}
	if src, _ := resolveUpdateSource(); src.host != "gitlab.example.com" {
		t.Error("rejected URL overwrote the stored one")
	}
	// omitting the field (other settings saves) leaves it alone
	put(`{"backupKeep":7}`)
	if src, _ := resolveUpdateSource(); src.host != "gitlab.example.com" {
		t.Error("unrelated save cleared the update URL")
	}
	// clearing goes back to the default, and it all persists on disk
	put(`{"updateURL":""}`)
	if src, _ := resolveUpdateSource(); src.kind != "github" || src.proj != updateRepo {
		t.Errorf("clearing did not restore the default: %+v", src)
	}
	store.loadSettings()
	if got := store.GetSettings().UpdateURL; got == nil || *got != "" {
		t.Errorf("cleared URL not persisted: %v", got)
	}
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.4.0", "1.3.0", true},
		{"1.3.1", "1.3.0", true},
		{"v2.0.0", "1.9.9", true},
		{"v1.3.0", "1.3.0", false},
		{"v1.2.9", "1.3.0", false},
		{"v1.3.0.1", "1.3.0", true},
		{"v1.4.0-rc1", "1.3.0", true}, // pre-release suffix ignored
		{"garbage", "1.3.0", false},
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGitHub serves a latest-release with the given binary bytes and checksum line.
func fakeGitHub(t *testing.T, tag string, bin []byte, sumLine string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/"+updateRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[
			{"name":%q,"url":%q,"size":%d},
			{"name":"sha256sums.txt","url":%q,"size":100}]}`,
			tag, updateAssetName(), srv.URL+"/bin", len(bin), srv.URL+"/sums")
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, sumLine) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestApplyUpdateSwapsBinary(t *testing.T) {
	if updateAssetName() == "" {
		t.Skipf("no release asset for %s", "this platform")
	}
	newBin := bytes.Repeat([]byte("NEWBINARY!"), 1<<17) // ~1.3 MB, over the size floor
	sum := sha256.Sum256(newBin)
	srv := fakeGitHub(t, "v99.0.0", newBin, fmt.Sprintf("%x  %s", sum, updateAssetName()))
	oldBase := updateAPIBase
	updateAPIBase = srv.URL
	defer func() { updateAPIBase = oldBase }()

	exe := filepath.Join(t.TempDir(), "portanote-fake.exe")
	if err := os.WriteFile(exe, []byte("OLDBINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	tag, err := applyUpdate(exe)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v99.0.0" {
		t.Errorf("tag = %q", tag)
	}
	got, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(got, newBin) {
		t.Errorf("exe not replaced (len %d, err %v)", len(got), err)
	}
	old, err := os.ReadFile(exe + ".old")
	if err != nil || string(old) != "OLDBINARY" {
		t.Errorf("previous binary not preserved as .old: %q, %v", old, err)
	}
}

func TestApplyUpdateRejectsBadChecksum(t *testing.T) {
	if updateAssetName() == "" {
		t.Skip("no release asset for this platform")
	}
	newBin := bytes.Repeat([]byte("NEWBINARY!"), 1<<17)
	srv := fakeGitHub(t, "v99.0.0", newBin,
		strings.Repeat("0", 64)+"  "+updateAssetName()) // wrong digest
	oldBase := updateAPIBase
	updateAPIBase = srv.URL
	defer func() { updateAPIBase = oldBase }()

	exe := filepath.Join(t.TempDir(), "portanote-fake.exe")
	if err := os.WriteFile(exe, []byte("OLDBINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := applyUpdate(exe)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum error, got %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLDBINARY" {
		t.Error("exe was modified despite checksum failure")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error(".new leftover not cleaned up")
	}
}

func TestParseUpdateURL(t *testing.T) {
	// empty = the built-in default
	src, err := parseUpdateURL("")
	if err != nil || src.kind != "github" || src.proj != updateRepo || src.api != updateAPIBase {
		t.Fatalf("default source = %+v, %v", src, err)
	}
	// github.com URLs keep using the GitHub API; .git and trailing / are tolerated
	src, err = parseUpdateURL("https://github.com/someone/fork.git/")
	if err != nil || src.kind != "github" || src.proj != "someone/fork" {
		t.Fatalf("github source = %+v, %v", src, err)
	}
	// any other host is a GitLab instance; nested groups stay one escaped path
	src, err = parseUpdateURL("https://gitlab.example.com/infra/tools/portanote")
	if err != nil || src.kind != "gitlab" ||
		src.api != "https://gitlab.example.com/api/v4" ||
		src.proj != "infra%2Ftools%2Fportanote" {
		t.Fatalf("gitlab source = %+v, %v", src, err)
	}
	for _, bad := range []string{"gitlab.example.com/a/b", "https://gitlab.example.com", "https://gitlab.example.com/justowner", "ftp://x/a/b", "::"} {
		if _, err := parseUpdateURL(bad); err == nil {
			t.Errorf("parseUpdateURL(%q) should fail", bad)
		}
	}
}

// fakeGitLab serves a GitLab-shaped latest release plus the assets.
func fakeGitLab(t *testing.T, tag string, bin []byte, sumLine string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	var gotToken string
	mux.HandleFunc("/api/v4/", func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		if !strings.Contains(r.RequestURI, "/releases/permalink/latest") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q,"assets":{"links":[
			{"name":%q,"direct_asset_url":%q},
			{"name":"sha256sums.txt","direct_asset_url":%q}]}}`,
			tag, updateAssetName(), srv.URL+"/bin", srv.URL+"/sums")
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, sumLine) })
	srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		if gotToken != "secret123" {
			t.Errorf("PRIVATE-TOKEN not sent to GitLab (got %q)", gotToken)
		}
	})
	return srv
}

func TestApplyUpdateFromGitLab(t *testing.T) {
	if updateAssetName() == "" {
		t.Skip("no release asset for this platform")
	}
	newBin := bytes.Repeat([]byte("GITLABBIN!"), 1<<17)
	sum := sha256.Sum256(newBin)
	srv := fakeGitLab(t, "v99.0.0", newBin, fmt.Sprintf("%x  %s", sum, updateAssetName()))
	t.Setenv("PORTANOTE_GITLAB_TOKEN", "secret123")
	setUpdateURL(srv.URL + "/jake/portanote")
	t.Cleanup(func() { setUpdateURL("") })

	exe := filepath.Join(t.TempDir(), "portanote-fake.exe")
	if err := os.WriteFile(exe, []byte("OLDBINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	tag, err := applyUpdate(exe)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v99.0.0" {
		t.Errorf("tag = %q", tag)
	}
	got, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(got, newBin) {
		t.Errorf("exe not replaced from the GitLab source (len %d, err %v)", len(got), err)
	}
}

func TestApplyUpdateAlreadyCurrent(t *testing.T) {
	if updateAssetName() == "" {
		t.Skip("no release asset for this platform")
	}
	srv := fakeGitHub(t, "v0.0.1", []byte("x"), "irrelevant")
	oldBase := updateAPIBase
	updateAPIBase = srv.URL
	defer func() { updateAPIBase = oldBase }()

	_, err := applyUpdate(filepath.Join(t.TempDir(), "x.exe"))
	if err == nil || !strings.Contains(err.Error(), "already up to date") {
		t.Fatalf("want already-up-to-date, got %v", err)
	}
}
