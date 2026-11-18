// Command serve builds the WASM app and serves web/ for local development.
//
//	go run ./cmd/serve            # http://localhost:8080
//	go run ./cmd/serve -addr :3000 -build=false
//
// The site itself is static: in production, host web/ on any static host
// (GitHub Pages, Cloudflare Pages). This server is only a convenience.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	dir := flag.String("dir", "web", "directory to serve")
	build := flag.Bool("build", true, "build web/wasm/app.wasm first")
	flag.Parse()

	if *build {
		if err := Build(*dir); err != nil {
			log.Fatal(err)
		}
	}

	fs := http.FileServer(http.Dir(*dir))
	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
			// Serve a pre-compressed copy if there is one (the Docker image has).
			gz := filepath.Join(*dir, filepath.FromSlash(r.URL.Path)) + ".gz"
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				if _, err := os.Stat(gz); err == nil {
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Cache-Control", "no-cache")
					http.ServeFile(w, r, gz)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		fs.ServeHTTP(w, r)
	}))
	log.Printf("serving %s on http://%s", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// Build compiles cmd/wasm into dir/wasm/app.wasm and copies wasm_exec.js.
func Build(dir string) error {
	out := filepath.Join(dir, "wasm", "app.wasm")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./cmd/wasm")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	log.Printf("building %s", out)
	if err := cmd.Run(); err != nil {
		return err
	}
	src := filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js")
	if _, err := os.Stat(src); err != nil {
		src = filepath.Join(runtime.GOROOT(), "misc", "wasm", "wasm_exec.js")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "wasm", "wasm_exec.js"), b, 0o644)
}
