// Portanote — a portable, single-binary notes app.
// Notes are plain .md files with YAML frontmatter, organized in real
// subdirectories that mirror the folder tree; the UI is embedded
// and served to your default browser on localhost only.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// a var so test builds can override it with -ldflags "-X main.version=..."
var version = "1.6.4"

//go:embed all:ui
var uiEmbed embed.FS

//go:embed pandoc/eisvogel.latex
var eisvogelTemplate []byte

func main() {
	var (
		dir       = flag.String("dir", "", "notes directory (default: ./notes next to the executable)")
		port      = flag.Int("port", 8737, "port to listen on (increments if busy)")
		host      = flag.String("host", "127.0.0.1", "bind address. 127.0.0.1 (default, localhost only), 0.0.0.0 (whole network), or \"subnet\" (whole network but only accept clients on this device's local subnet, e.g. 10.10.10.0/24)")
		noBrowser = flag.Bool("no-browser", false, "do not open the browser on start")
	)
	flag.Parse()

	// leftovers from a previous self-update (best effort; the old binary may
	// still be exiting, in which case the next start gets it)
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
		os.Remove(exe + ".new")
	}

	notesDir := *dir
	if notesDir == "" {
		exe, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		notesDir = filepath.Join(filepath.Dir(exe), "notes")
	}
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		log.Fatal(err)
	}
	loadClaudeConfig(notesDir) // claude exe/settings overrides + activity log

	store, err := NewStore(notesDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("portanote v%s — %d notes loaded from %s", version, store.Count(), notesDir)
	store.StartBackups()

	uiFS, err := fs.Sub(uiEmbed, "ui")
	if err != nil {
		log.Fatal(err)
	}

	mux := newAPI(store, uiFS)
	var handler http.Handler = mux

	// "subnet" mode: bind everywhere, but only serve clients on this device's
	// local subnet (loopback always allowed). Anyone else gets a 403.
	bindHost := *host
	subnetMode := *host == "subnet" || *host == "lan" || *host == "auto"
	var allowed *net.IPNet
	if subnetMode {
		allowed = lanSubnet()
		if allowed == nil {
			log.Fatal("-host subnet: could not detect a local subnet — connect to a network or pass an explicit -host")
		}
		bindHost = "0.0.0.0"
		handler = subnetGuard(mux, allowed)
	}

	ln, actualPort := listen(bindHost, *port)
	// the spawned claude CLI must call back into this instance's real port
	claudeMCPURL = fmt.Sprintf("http://127.0.0.1:%d/mcp", actualPort)
	localURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	log.Printf("serving at %s  (Ctrl+C to quit)", localURL)
	log.Printf("MCP endpoint at %s/mcp  (Streamable HTTP)", localURL)
	if p := claudePath(); p != "" {
		log.Printf("Ask Claude enabled: %s", p)
	} else {
		log.Printf("Ask Claude disabled: no claude CLI found (install Claude Code to enable)")
	}

	if subnetMode {
		log.Printf("on your network:  http://%s:%d", lanIP(), actualPort)
		log.Printf("access restricted to %s (+ localhost); other hosts get 403. Still no password within the subnet.", allowed.String())
	} else if *host == "0.0.0.0" || *host == "::" {
		if ip := lanIP(); ip != "" {
			log.Printf("on your network:  http://%s:%d   (same Wi-Fi; allow the port through the firewall)", ip, actualPort)
		}
		log.Printf("NOTE: -host %s exposes your notes to everyone on this network — there is no password.", *host)
	}

	if !*noBrowser {
		openBrowser(localURL)
	}
	log.Fatal(http.Serve(ln, handler))
}

// subnetGuard rejects requests whose source IP is neither loopback nor inside
// allowed. RemoteAddr is the real TCP peer, so this can't be spoofed by a header.
func subnetGuard(next http.Handler, allowed *net.IPNet) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip != nil && (ip.IsLoopback() || allowed.Contains(ip)) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden: outside the allowed subnet", http.StatusForbidden)
	})
}

// listen binds host on the requested port, walking upward if taken.
func listen(host string, want int) (net.Listener, int) {
	// after a self-update relaunch, wait briefly for the exiting parent to
	// free its port instead of walking up — bookmarks keep working
	if os.Getenv("PORTANOTE_RELAUNCH") != "" {
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			if ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, want)); err == nil {
				return ln, want
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	for p := want; p < want+50; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, p))
		if err == nil {
			return ln, p
		}
	}
	log.Fatalf("no free port found in range %d-%d", want, want+49)
	return nil, 0
}

// lanIP returns this machine's primary private IPv4 address, or "".
func lanIP() string {
	// UDP dial to a public address picks the outbound interface without sending anything.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).IP.String()
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}

// lanSubnet returns the network this device sits on, using the interface's real
// netmask (e.g. 10.10.10.100/24 -> 10.10.10.0/24), or nil if it can't be found.
func lanSubnet() *net.IPNet {
	ip := net.ParseIP(lanIP())
	if ip == nil {
		return nil
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if ok && ipnet.IP.Equal(ip) {
			return &net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask), Mask: ipnet.Mask}
		}
	}
	// couldn't read the mask — fall back to assuming a /24
	if ip4 := ip.To4(); ip4 != nil {
		mask := net.CIDRMask(24, 32)
		return &net.IPNet{IP: ip4.Mask(mask), Mask: mask}
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser (%v) — open %s manually", err, url)
	}
}
