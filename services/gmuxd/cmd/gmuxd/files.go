package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gmuxapp/gmux/packages/paths"
	projectspkg "github.com/gmuxapp/gmux/services/gmuxd/internal/projects"
	"github.com/gmuxapp/gmux/services/gmuxd/internal/store"
)

const (
	workspaceFilePreviewLimit  = 512 * 1024
	workspaceImagePreviewLimit = 10 * 1024 * 1024
	workspaceDirEntryLimit     = 1000
)

type workspaceFileEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     int64  `json:"size,omitempty"`
	ModTime  string `json:"mod_time,omitempty"`
	Hidden   bool   `json:"hidden,omitempty"`
	Symlink  bool   `json:"symlink,omitempty"`
	TooLarge bool   `json:"too_large,omitempty"`
}

type workspaceFileList struct {
	Root      string               `json:"root"`
	Path      string               `json:"path"`
	AbsPath   string               `json:"abs_path"`
	Entries   []workspaceFileEntry `json:"entries"`
	Truncated bool                 `json:"truncated,omitempty"`
}

type workspaceFileContent struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	AbsPath   string `json:"abs_path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	ModTime   string `json:"mod_time,omitempty"`
	Mime      string `json:"mime,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

func workspaceSessionFilesListHandler(w http.ResponseWriter, r *http.Request, sessionID string, sessions *store.Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}
	root, err := workspaceRootForSession(sessionID, sessions)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	serveWorkspaceFilesList(w, r, root)
}

func workspaceSessionFilesContentHandler(w http.ResponseWriter, r *http.Request, sessionID string, sessions *store.Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}
	root, err := workspaceRootForSession(sessionID, sessions)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	serveWorkspaceFilesContent(w, r, root)
}

func workspaceProjectFilesListHandler(w http.ResponseWriter, r *http.Request, slug string, projectMgr *projectspkg.Manager, sessions *store.Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}
	root, err := workspaceRootForProject(slug, projectMgr, sessions)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	serveWorkspaceFilesList(w, r, root)
}

func workspaceProjectFilesContentHandler(w http.ResponseWriter, r *http.Request, slug string, projectMgr *projectspkg.Manager, sessions *store.Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "method not allowed")
		return
	}
	root, err := workspaceRootForProject(slug, projectMgr, sessions)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	serveWorkspaceFilesContent(w, r, root)
}

func workspaceRootForSession(sessionID string, sessions *store.Store) (string, error) {
	sess, ok := sessions.Get(sessionID)
	if !ok {
		return "", workspacePathError{http.StatusNotFound, "not_found", "session not found"}
	}
	root := strings.TrimSpace(sess.WorkspaceRoot)
	if root == "" {
		root = strings.TrimSpace(sess.Cwd)
	}
	if root == "" {
		return "", workspacePathError{http.StatusBadRequest, "no_workspace_root", "session has no workspace root"}
	}
	return root, nil
}

func workspaceRootForProject(slug string, projectMgr *projectspkg.Manager, sessions *store.Store) (string, error) {
	state, err := projectMgr.Load()
	if err != nil {
		return "", workspacePathError{http.StatusInternalServerError, "internal", "failed to load projects"}
	}
	for _, item := range state.Items {
		if item.Slug != slug || item.Peer != "" {
			continue
		}
		for _, rule := range item.Match {
			if strings.TrimSpace(rule.Path) != "" {
				return rule.Path, nil
			}
		}
		for _, sess := range sessions.List() {
			if sess.Peer == "" && sess.ProjectSlug == slug {
				root := strings.TrimSpace(sess.WorkspaceRoot)
				if root == "" {
					root = strings.TrimSpace(sess.Cwd)
				}
				if root != "" {
					return root, nil
				}
			}
		}
		return "", workspacePathError{http.StatusBadRequest, "no_workspace_root", "project has no path or session root"}
	}
	return "", workspacePathError{http.StatusNotFound, "not_found", "project not found"}
}

func serveWorkspaceFilesList(w http.ResponseWriter, r *http.Request, rootHint string) {
	root, rel, abs, err := resolveWorkspaceFilePath(r, rootHint)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeWorkspaceStatError(w, err)
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "not_directory", "path is not a directory")
		return
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		writeWorkspaceStatError(w, err)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		ai, aj := entries[i], entries[j]
		if ai.IsDir() != aj.IsDir() {
			return ai.IsDir()
		}
		return strings.ToLower(ai.Name()) < strings.ToLower(aj.Name())
	})

	truncated := len(entries) > workspaceDirEntryLimit
	if truncated {
		entries = entries[:workspaceDirEntryLimit]
	}
	out := make([]workspaceFileEntry, 0, len(entries))
	for _, ent := range entries {
		info, err := ent.Info()
		if err != nil {
			continue
		}
		typ := "file"
		if info.IsDir() {
			typ = "dir"
		}
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if isSymlink {
			typ = "symlink"
			if target, err := filepath.EvalSymlinks(filepath.Join(abs, ent.Name())); err == nil {
				if st, statErr := os.Stat(target); statErr == nil && st.IsDir() {
					typ = "dir"
				}
			}
		}
		out = append(out, workspaceFileEntry{
			Name:     ent.Name(),
			Path:     joinWorkspaceRel(rel, ent.Name()),
			Type:     typ,
			Size:     info.Size(),
			ModTime:  formatModTime(info.ModTime()),
			Hidden:   strings.HasPrefix(ent.Name(), "."),
			Symlink:  isSymlink,
			TooLarge: !info.IsDir() && info.Size() > workspacePreviewLimitForPath(filepath.Join(abs, ent.Name())),
		})
	}

	writeJSON(w, map[string]any{"ok": true, "data": workspaceFileList{
		Root:      root,
		Path:      rel,
		AbsPath:   abs,
		Entries:   out,
		Truncated: truncated,
	}})
}

func workspacePreviewLimitForPath(path string) int64 {
	mimeType := workspaceMimeType(path, nil)
	if isWorkspaceImageMime(mimeType) {
		return workspaceImagePreviewLimit
	}
	return workspaceFilePreviewLimit
}

func workspaceMimeType(path string, data []byte) string {
	extType := normalizeMime(mime.TypeByExtension(filepath.Ext(path)))
	if len(data) == 0 {
		return extType
	}
	if imageType := workspaceImageMimeFromMagic(data); imageType != "" {
		return imageType
	}
	sniffed := normalizeMime(http.DetectContentType(data))
	if isWorkspaceImageMime(extType) {
		return sniffed
	}
	if extType != "" {
		return extType
	}
	return sniffed
}

func workspaceImageMimeFromMagic(data []byte) string {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(data) >= 2 && string(data[:2]) == "BM" {
		return "image/bmp"
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		for i := 8; i+4 <= len(data) && i < 32; i += 4 {
			brand := string(data[i : i+4])
			if brand == "avif" || brand == "avis" {
				return "image/avif"
			}
		}
	}
	return ""
}

func normalizeMime(mimeType string) string {
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = mimeType[:i]
	}
	return strings.ToLower(strings.TrimSpace(mimeType))
}

func isWorkspaceImageMime(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp":
		return true
	default:
		return false
	}
}

func serveWorkspaceFileRaw(w http.ResponseWriter, r *http.Request, abs string, info os.FileInfo) {
	limit := workspacePreviewLimitForPath(abs)
	if info.Size() > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("file exceeds %d byte preview limit", limit))
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		writeWorkspaceStatError(w, err)
		return
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if n == 0 {
		if off, err := f.Seek(0, io.SeekStart); err != nil || off != 0 {
			writeError(w, http.StatusInternalServerError, "internal", "failed to read file")
			return
		}
	} else if off, err := f.Seek(0, io.SeekStart); err != nil || off != 0 {
		writeError(w, http.StatusInternalServerError, "internal", "failed to read file")
		return
	}
	mimeType := workspaceMimeType(abs, head[:n])
	if !isWorkspaceImageMime(mimeType) {
		writeError(w, http.StatusUnsupportedMediaType, "binary", "only image files can be opened raw")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(abs), info.ModTime(), f)
}

func serveWorkspaceFilesContent(w http.ResponseWriter, r *http.Request, rootHint string) {
	root, rel, abs, err := resolveWorkspaceFilePath(r, rootHint)
	if err != nil {
		writeWorkspaceFileError(w, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeWorkspaceStatError(w, err)
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "is_directory", "path is a directory")
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		serveWorkspaceFileRaw(w, r, abs, info)
		return
	}

	limit := workspacePreviewLimitForPath(abs)
	if info.Size() > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("file exceeds %d byte preview limit", limit))
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		writeWorkspaceStatError(w, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "failed to read file")
		return
	}
	mimeType := workspaceMimeType(abs, data)
	if isWorkspaceImageMime(mimeType) {
		writeJSON(w, map[string]any{"ok": true, "data": workspaceFileContent{
			Root:    root,
			Path:    rel,
			AbsPath: abs,
			Name:    filepath.Base(abs),
			Size:    info.Size(),
			ModTime: formatModTime(info.ModTime()),
			Mime:    mimeType,
			Kind:    "image",
			Content: "",
		}})
		return
	}
	if isBinaryPreview(data) {
		writeError(w, http.StatusUnsupportedMediaType, "binary", "binary files cannot be previewed")
		return
	}
	if !utf8.Valid(data) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_encoding", "file is not valid UTF-8")
		return
	}

	writeJSON(w, map[string]any{"ok": true, "data": workspaceFileContent{
		Root:    root,
		Path:    rel,
		AbsPath: abs,
		Name:    filepath.Base(abs),
		Size:    info.Size(),
		ModTime: formatModTime(info.ModTime()),
		Mime:    mimeType,
		Kind:    "text",
		Content: string(data),
	}})
}

type workspacePathError struct {
	status  int
	code    string
	message string
}

func (e workspacePathError) Error() string { return e.message }

func resolveWorkspaceFilePath(r *http.Request, rootRaw string) (rootDisplay, rel, abs string, err error) {
	rootRaw = strings.TrimSpace(rootRaw)
	if rootRaw == "" {
		return "", "", "", workspacePathError{http.StatusBadRequest, "bad_request", "workspace root is required"}
	}
	root := paths.NormalizePath(rootRaw)
	if !filepath.IsAbs(root) {
		return "", "", "", workspacePathError{http.StatusBadRequest, "bad_root", "root must resolve to an absolute path"}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", "", workspacePathError{http.StatusBadRequest, "bad_root", "invalid root"}
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", "", workspacePathError{http.StatusNotFound, "root_not_found", "root does not exist"}
	}

	rel = normalizeWorkspaceRel(r.URL.Query().Get("path"))
	candidate := filepath.Join(rootReal, filepath.FromSlash(rel))
	candidateReal, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", "", err
	}
	if !pathWithinRoot(rootReal, candidateReal) {
		return "", "", "", workspacePathError{http.StatusForbidden, "outside_root", "path escapes workspace root"}
	}
	return rootRaw, filepath.ToSlash(rel), candidateReal, nil
}

func normalizeWorkspaceRel(raw string) string {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(raw))))
	if clean == "." || clean == "/" {
		return ""
	}
	return strings.TrimPrefix(clean, "/")
}

func pathWithinRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

func joinWorkspaceRel(base, name string) string {
	if base == "" {
		return name
	}
	return filepath.ToSlash(filepath.Join(base, name))
}

func formatModTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func isBinaryPreview(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

func writeWorkspaceFileError(w http.ResponseWriter, err error) {
	var pathErr workspacePathError
	if errors.As(err, &pathErr) {
		writeError(w, pathErr.status, pathErr.code, pathErr.message)
		return
	}
	writeWorkspaceStatError(w, err)
}

func writeWorkspaceStatError(w http.ResponseWriter, err error) {
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return
	}
	if os.IsPermission(err) {
		writeError(w, http.StatusForbidden, "forbidden", "permission denied")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", "filesystem error")
}
