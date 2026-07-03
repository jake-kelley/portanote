// Portanote — a portable, single-binary notes app.
// Notes are plain .md files with YAML frontmatter; the UI is embedded
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
)

const version = "1.0.0"

//go:embed all:ui
var uiEmbed embed.FS

//go:embed pandoc/eisvogel.latex
var eisvogelTemplate []byte

func main() {
	var (
		dir       = flag.String("dir", "", "notes directory (default: ./notes next to the executable)")
		port      = flag.Int("port", 8737, "port to listen on (increments if busy)")
		host      = flag.String("host", "127.0.0.1", "interface to bind (use 0.0.0.0 to reach it from your phone on the same network)")
		noBrowser = flag.Bool("no-browser", false, "do not open the browser on start")
	)
	flag.Parse()

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

	store, err := NewStore(notesDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("portanote v%s — %d notes loaded from %s", version, store.Count(), notesDir)

	uiFS, err := fs.Sub(uiEmbed, "ui")
	if err != nil {
		log.Fatal(err)
	}

	ln, actualPort := listen(*host, *port)
	mux := newAPI(store, uiFS)
	localURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	log.Printf("serving at %s  (Ctrl+C to quit)", localURL)

	// When bound to all interfaces, print the LAN address to open on a phone.
	if *host == "0.0.0.0" || *host == "::" {
		if ip := lanIP(); ip != "" {
			log.Printf("on your network:  http://%s:%d   (same Wi-Fi; allow the port through the firewall)", ip, actualPort)
		}
		log.Printf("NOTE: -host %s exposes your notes to everyone on this network — there is no password.", *host)
	}

	if !*noBrowser {
		openBrowser(localURL)
	}
	log.Fatal(http.Serve(ln, mux))
}

// listen binds host on the requested port, walking upward if taken.
func listen(host string, want int) (net.Listener, int) {
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
