package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

//go:embed all:web
var webFS embed.FS

// spaHandler serves the frontend as a single-page application. Static files
// are served directly; all other paths fall back to index.html so client-side
// routing works.
//
// Precedence:
//  1. GMUXD_DEV_PROXY reverse-proxies to a Vite dev server.
//  2. GMUXD_WEB_DIR serves a local built frontend directory.
//  3. config web_dir serves a local built frontend directory.
//  4. embedded assets compiled into gmuxd.
func spaHandler(configWebDir string) http.Handler {
	if devProxy := os.Getenv("GMUXD_DEV_PROXY"); devProxy != "" {
		return devProxyHandler(devProxy)
	}
	if envWebDir := strings.TrimSpace(os.Getenv("GMUXD_WEB_DIR")); envWebDir != "" {
		h, err := externalFrontendHandler(envWebDir)
		if err != nil {
			log.Fatalf("GMUXD_WEB_DIR: %v", err)
		}
		return h
	}
	if configWebDir = strings.TrimSpace(configWebDir); configWebDir != "" {
		h, err := externalFrontendHandler(configWebDir)
		if err != nil {
			log.Fatalf("config web_dir: %v", err)
		}
		return h
	}
	return embeddedHandler()
}

func devProxyHandler(target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("GMUXD_DEV_PROXY: invalid URL %q: %v", target, err)
	}
	log.Printf("frontend: proxying to dev server at %s", target)
	proxy := httputil.NewSingleHostReverseProxy(u)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func embeddedHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("embedded web directory missing: " + err.Error())
	}
	return frontendFSHandler(sub)
}

func externalFrontendHandler(dir string) (http.Handler, error) {
	dir = expandHome(dir)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	if _, err := os.Stat(filepath.Join(abs, "index.html")); err != nil {
		return nil, fmt.Errorf("%s must contain index.html: %w", abs, err)
	}
	log.Printf("frontend: serving external assets from %s", abs)
	return frontendFSHandler(os.DirFS(abs)), nil
}

func frontendFSHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/ws/") {
			http.NotFound(w, r)
			return
		}

		fsPath := frontendFSPath(path)
		if _, err := fs.Stat(root, fsPath); err == nil {
			if fsPath == "index.html" {
				serveFrontendFile(w, root, fsPath)
				return
			}
			if cache := frontendCacheControl(fsPath); cache != "" {
				w.Header().Set("Cache-Control", cache)
			}
			r.URL.Path = "/" + fsPath
			fileServer.ServeHTTP(w, r)
			return
		}

		serveFrontendFile(w, root, "index.html")
	})
}

func serveFrontendFile(w http.ResponseWriter, root fs.FS, name string) {
	data, err := fs.ReadFile(root, name)
	if err != nil {
		http.Error(w, "frontend asset unavailable", http.StatusInternalServerError)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if cache := frontendCacheControl(name); cache != "" {
		w.Header().Set("Cache-Control", cache)
	}
	_, _ = w.Write(data)
}

func frontendCacheControl(name string) string {
	switch name {
	case "index.html", "manifest.json", "sw.js":
		return "no-cache"
	}
	if strings.HasPrefix(name, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return ""
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func frontendFSPath(urlPath string) string {
	clean := pathpkg.Clean("/" + urlPath)
	fsPath := strings.TrimPrefix(clean, "/")
	if fsPath == "" || fsPath == "." {
		return "index.html"
	}
	return fsPath
}
