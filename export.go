package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// True-Eisvogel PDF export: pandoc + a LaTeX engine, both looked up first in a
// ./tools folder next to the executable (portable, no installation) and then
// on PATH. The eisvogel.latex template itself ships embedded in this binary.
//
// tools/
//   pandoc(.exe)
//   tectonic(.exe)        <- single-binary LaTeX engine; downloads its LaTeX
//                            packages to tools/tectonic-cache on first export

type ExportOpts struct {
	TOC       bool
	TitlePage bool
	NotesDir  string
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func findTool(name string) string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	candidates := []string{
		filepath.Join(exeDir(), "tools", name+ext),
		filepath.Join(exeDir(), "tools", name, name+ext),
		filepath.Join(exeDir(), "tools", name, "bin", name+ext),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func pandocPath() string { return findTool("pandoc") }

// pdfEnginePath prefers tectonic (self-contained) but accepts a system TeX.
func pdfEnginePath() string {
	for _, name := range []string{"tectonic", "xelatex", "pdflatex"} {
		if p := findTool(name); p != "" {
			return p
		}
	}
	return ""
}

func ExportEisvogelPDF(n *Note, opts ExportOpts) ([]byte, error) {
	pandoc := pandocPath()
	engine := pdfEnginePath()
	if pandoc == "" || engine == "" {
		return nil, fmt.Errorf("pandoc/LaTeX engine not found — run scripts/get-tools to drop portable pandoc + tectonic into the tools folder")
	}

	tmp, err := os.MkdirTemp("", "portanote-export-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	src := filepath.Join(tmp, "note.md")
	if err := os.WriteFile(src, []byte(n.Body), 0o644); err != nil {
		return nil, err
	}
	tmpl := filepath.Join(tmp, "eisvogel.latex")
	if err := os.WriteFile(tmpl, eisvogelTemplate, 0o644); err != nil {
		return nil, err
	}

	out := filepath.Join(tmp, "note.pdf")
	args := []string{
		src, "-o", out,
		"--from", "markdown+emoji",
		"--template", tmpl,
		"--pdf-engine", engine,
		"--listings",
		"--metadata", "title=" + n.Title,
		"--metadata", "date=" + n.Updated.Format("January 2, 2006"),
		"--resource-path", opts.NotesDir,
	}
	if len(n.Tags) > 0 {
		subtitle := ""
		for i, t := range n.Tags {
			if i > 0 {
				subtitle += " · "
			}
			subtitle += t
		}
		args = append(args, "--metadata", "subtitle="+subtitle)
	}
	if opts.TitlePage {
		args = append(args, "-V", "titlepage=true")
	}
	if opts.TOC {
		args = append(args, "--toc", "--toc-depth=3")
	}

	// first export downloads LaTeX packages, so allow plenty of time
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, pandoc, args...)
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(),
		"TECTONIC_CACHE_DIR="+filepath.Join(exeDir(), "tools", "tectonic-cache"))
	if stderr, err := cmd.CombinedOutput(); err != nil {
		msg := string(stderr)
		if len(msg) > 2000 {
			msg = "…" + msg[len(msg)-2000:]
		}
		return nil, fmt.Errorf("pandoc failed: %v\n%s", err, msg)
	}
	return os.ReadFile(out)
}
