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
)

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
