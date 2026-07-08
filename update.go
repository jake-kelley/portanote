package main

// Self-update from GitHub Releases. The check hits the public releases API
// (a private repo needs a token in PORTANOTE_GITHUB_TOKEN or GITHUB_TOKEN).
// The platform binary is verified against the release's sha256sums.txt, the
// running executable is swapped by rename (legal on Windows and macOS even
// while running), and the process relaunches itself with the same arguments;
// the PORTANOTE_RELAUNCH env makes the child wait for the parent's port.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const updateRepo = "jake-kelley/portanote"

// a var so tests can point it at a stub server
var updateAPIBase = "https://api.github.com"

var updateInFlight atomic.Bool

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"` // API asset URL — downloadable for private repos too
	Size int64  `json:"size"`
}

type ghRelease struct {
	Tag    string    `json:"tag_name"`
	Assets []ghAsset `json:"assets"`
}

func ghGet(url, accept string, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "portanote/"+version)
	token := os.Getenv("PORTANOTE_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token != "" {
		// Go strips Authorization on the cross-host redirect to the CDN,
		// which is exactly what GitHub's asset downloads require
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("GitHub returned %d — if the repository is private, set PORTANOTE_GITHUB_TOKEN", resp.StatusCode)
		}
		return nil, fmt.Errorf("GitHub returned %d for %s", resp.StatusCode, url)
	}
	return resp, nil
}

func latestRelease() (*ghRelease, error) {
	resp, err := ghGet(updateAPIBase+"/repos/"+updateRepo+"/releases/latest",
		"application/vnd.github+json", 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("could not parse the release: %w", err)
	}
	if rel.Tag == "" {
		return nil, errors.New("latest release has no tag")
	}
	return &rel, nil
}

// versionNewer reports whether a (like "v1.2.3" or "1.2.3") is newer than b.
func versionNewer(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		va, vb := 0, 0
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va != vb {
			return va > vb
		}
	}
	return false
}

func versionParts(s string) []int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 { // ignore pre-release/build suffixes
		s = s[:i]
	}
	segs := strings.Split(s, ".")
	out := make([]int, 0, len(segs))
	for _, seg := range segs {
		n, _ := strconv.Atoi(seg)
		out = append(out, n)
	}
	return out
}

// updateAssetName is the release asset built for this platform ("" if none).
func updateAssetName() string {
	switch {
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "portanote-windows-amd64.exe"
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "portanote-macos-arm64"
	}
	return ""
}

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Asset     string `json:"asset,omitempty"`
}

func checkUpdate() (UpdateInfo, error) {
	rel, err := latestRelease()
	if err != nil {
		return UpdateInfo{}, err
	}
	return UpdateInfo{
		Current:   "v" + version,
		Latest:    rel.Tag,
		Available: versionNewer(rel.Tag, version),
		Asset:     updateAssetName(),
	}, nil
}

// applyUpdate downloads the latest platform binary next to exe, verifies its
// checksum, and swaps it into place. The caller relaunches afterwards.
func applyUpdate(exe string) (string, error) {
	rel, err := latestRelease()
	if err != nil {
		return "", err
	}
	if !versionNewer(rel.Tag, version) {
		return "", fmt.Errorf("already up to date (v%s)", version)
	}
	name := updateAssetName()
	if name == "" {
		return "", fmt.Errorf("no prebuilt binary for %s/%s — update manually", runtime.GOOS, runtime.GOARCH)
	}
	var bin, sums *ghAsset
	for i := range rel.Assets {
		switch rel.Assets[i].Name {
		case name:
			bin = &rel.Assets[i]
		case "sha256sums.txt":
			sums = &rel.Assets[i]
		}
	}
	if bin == nil {
		return "", fmt.Errorf("release %s has no asset %q", rel.Tag, name)
	}
	if sums == nil {
		return "", fmt.Errorf("release %s has no sha256sums.txt — refusing an unverifiable update", rel.Tag)
	}

	want, err := fetchChecksum(sums.URL, name)
	if err != nil {
		return "", err
	}
	tmp := exe + ".new"
	sum, size, err := downloadTo(tmp, bin.URL)
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if size < 1<<20 {
		os.Remove(tmp)
		return "", fmt.Errorf("downloaded binary is suspiciously small (%d bytes)", size)
	}
	if !strings.EqualFold(sum, want) {
		os.Remove(tmp)
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, sum, want)
	}

	// swap: renaming a running executable is allowed on Windows and macOS
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe) // roll back
		os.Remove(tmp)
		return "", err
	}
	return rel.Tag, nil
}

// fetchChecksum pulls sha256sums.txt and returns the hex digest listed for name.
func fetchChecksum(url, name string) (string, error) {
	resp, err := ghGet(url, "application/octet-stream", 30*time.Second)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("sha256sums.txt has no entry for %s", name)
}

// downloadTo streams url into path (0755) and returns the sha256 and size.
func downloadTo(path, url string) (string, int64, error) {
	resp, err := ghGet(url, "application/octet-stream", 5*time.Minute)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// relaunch starts the just-swapped executable with the same arguments and
// exits this process. PORTANOTE_RELAUNCH tells the child to wait for our
// port instead of walking up to the next free one.
func relaunch(exe string) {
	time.Sleep(500 * time.Millisecond) // let the HTTP response flush
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "PORTANOTE_RELAUNCH=1")
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		log.Printf("update installed, but relaunch failed: %v — restart Portanote manually", err)
		updateInFlight.Store(false)
		return
	}
	log.Printf("restarting into the updated binary (pid %d)", cmd.Process.Pid)
	os.Exit(0)
}
