package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ledongthuc/pdf"
)

//go:embed static
var staticFiles embed.FS

const outputFile = "to_delete.txt"

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".webp": true, ".tiff": true, ".ico": true, ".svg": true,
}
var textExts = map[string]bool{
	".txt": true, ".md": true, ".rst": true, ".csv": true, ".log": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".cfg": true, ".conf": true, ".env": true, ".sh": true, ".bash": true,
	".zsh": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
	".jsx": true, ".html": true, ".htm": true, ".css": true, ".scss": true,
	".sass": true, ".less": true, ".xml": true, ".sql": true, ".rb": true,
	".go": true, ".rs": true, ".c": true, ".cpp": true, ".h": true,
	".hpp": true, ".java": true, ".kt": true, ".swift": true, ".php": true,
	".r": true, ".lua": true, ".vim": true,
}
var audioExts = map[string]bool{
	".mp3": true, ".wav": true, ".flac": true, ".aac": true,
	".ogg": true, ".m4a": true, ".wma": true,
}
var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true,
	".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
}

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

type Entry struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Ext     string `json:"ext"`
	Kind    string `json:"kind"` // image|text|pdf|audio|video|directory|binary
}

type DirLevel struct {
	Dir     string
	Entries []Entry
	Index   int
}

type Session struct {
	mu            sync.Mutex
	RootDir       string
	Stack         []DirLevel
	ToDelete      []string
	AlreadyMarked map[string]bool
	Done          bool
}

var session *Session

// ---------------------------------------------------------------------------
// File helpers
// ---------------------------------------------------------------------------

func fileKind(ext string, isDir bool) string {
	if isDir {
		return "directory"
	}
	switch {
	case imageExts[ext]:
		return "image"
	case textExts[ext]:
		return "text"
	case ext == ".pdf":
		return "pdf"
	case audioExts[ext]:
		return "audio"
	case videoExts[ext]:
		return "video"
	default:
		return "binary"
	}
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func loadDir(dir string, alreadyMarked map[string]bool) ([]Entry, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []Entry
	for _, e := range dirEntries {
		absPath, _ := filepath.Abs(filepath.Join(dir, e.Name()))
		if alreadyMarked[absPath] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		result = append(result, Entry{
			Path:    absPath,
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04"),
			Ext:     ext,
			Kind:    fileKind(ext, e.IsDir()),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func loadAlreadyMarked() map[string]bool {
	marked := make(map[string]bool)
	f, err := os.Open(outputFile)
	if err != nil {
		return marked
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			marked[line] = true
		}
	}
	return marked
}

func appendToDeleteFile(path string) {
	needsHeader := false
	if info, err := os.Stat(outputFile); err != nil || info.Size() == 0 {
		needsHeader = true
	}
	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if needsHeader {
		f.WriteString("# Files/directories marked for deletion\n\n")
	}
	f.WriteString(path + "\n")
}

func readTextLines(path string, maxLines int) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	total := len(lines)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n"), total, nil
}

func extractPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		fmt.Fprintf(&buf, "--- Page %d ---\n%s\n", i, text)
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// Session helpers
// ---------------------------------------------------------------------------

func (s *Session) currentLevel() *DirLevel {
	if len(s.Stack) == 0 {
		return nil
	}
	return &s.Stack[len(s.Stack)-1]
}

func (s *Session) currentEntry() *Entry {
	level := s.currentLevel()
	if level == nil || level.Index >= len(level.Entries) {
		return nil
	}
	e := level.Entries[level.Index]
	return &e
}

func (s *Session) breadcrumb() []string {
	var crumbs []string
	for _, l := range s.Stack {
		crumbs = append(crumbs, filepath.Base(l.Dir))
	}
	return crumbs
}

// advance moves to next entry, auto-popping exhausted directories.
func (s *Session) advance() {
	for len(s.Stack) > 0 {
		level := &s.Stack[len(s.Stack)-1]
		level.Index++
		if level.Index < len(level.Entries) {
			return
		}
		if len(s.Stack) == 1 {
			s.Done = true
			return
		}
		s.Stack = s.Stack[:len(s.Stack)-1]
	}
	s.Done = true
}

// ---------------------------------------------------------------------------
// API handlers
// ---------------------------------------------------------------------------

type StateResponse struct {
	Entry      *Entry   `json:"entry"`
	Progress   Progress `json:"progress"`
	Breadcrumb []string `json:"breadcrumb"`
	Done       bool     `json:"done"`
}

type Progress struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

type PreviewResponse struct {
	Kind     string  `json:"kind"`
	Content  string  `json:"content,omitempty"`
	Lines    int     `json:"lines,omitempty"`
	HasMore  bool    `json:"hasMore,omitempty"`
	Entries  []Entry `json:"entries,omitempty"`
	MimeType string  `json:"mimeType,omitempty"`
	SizeStr  string  `json:"sizeStr,omitempty"`
}

type ActionRequest struct {
	Action string `json:"action"` // keep|delete|skip|enter|back|quit
}

func handleState(w http.ResponseWriter, r *http.Request) {
	session.mu.Lock()
	defer session.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	if session.Done {
		json.NewEncoder(w).Encode(StateResponse{Done: true})
		return
	}

	level := session.currentLevel()
	entry := session.currentEntry()

	var progress Progress
	if level != nil {
		progress = Progress{Current: level.Index + 1, Total: len(level.Entries)}
	}

	json.NewEncoder(w).Encode(StateResponse{
		Entry:      entry,
		Progress:   progress,
		Breadcrumb: session.breadcrumb(),
		Done:       false,
	})
}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	session.mu.Lock()

	if session.Done {
		session.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PreviewResponse{Kind: "done"})
		return
	}

	entry := session.currentEntry()
	alreadyMarked := session.AlreadyMarked
	session.mu.Unlock()

	if entry == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PreviewResponse{Kind: "done"})
		return
	}

	maxLines := 30
	if r.URL.Query().Get("lines") == "100" {
		maxLines = 100
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildPreview(*entry, maxLines, alreadyMarked))
}

func buildPreview(entry Entry, maxLines int, alreadyMarked map[string]bool) PreviewResponse {
	switch entry.Kind {
	case "directory":
		entries, err := loadDir(entry.Path, alreadyMarked)
		if err != nil {
			return PreviewResponse{Kind: "error", Content: err.Error()}
		}
		return PreviewResponse{Kind: "directory", Entries: entries}

	case "image":
		mimeType := mime.TypeByExtension(entry.Ext)
		return PreviewResponse{Kind: "image", MimeType: mimeType}

	case "text":
		content, total, err := readTextLines(entry.Path, maxLines)
		if err != nil {
			return PreviewResponse{Kind: "error", Content: err.Error()}
		}
		return PreviewResponse{
			Kind:    "text",
			Content: content,
			Lines:   total,
			HasMore: total > maxLines && maxLines < 100,
		}

	case "pdf":
		content, err := extractPDFText(entry.Path)
		if err != nil {
			return PreviewResponse{Kind: "error", Content: fmt.Sprintf("Could not extract PDF text: %v", err)}
		}
		lines := strings.Split(content, "\n")
		total := len(lines)
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		return PreviewResponse{
			Kind:    "pdf",
			Content: strings.Join(lines, "\n"),
			Lines:   total,
			HasMore: total > maxLines && maxLines < 100,
		}

	case "audio":
		mimeType := mime.TypeByExtension(entry.Ext)
		return PreviewResponse{Kind: "audio", MimeType: mimeType, SizeStr: humanSize(entry.Size)}

	case "video":
		mimeType := mime.TypeByExtension(entry.Ext)
		return PreviewResponse{Kind: "video", MimeType: mimeType, SizeStr: humanSize(entry.Size)}

	default:
		mimeType := mime.TypeByExtension(entry.Ext)
		return PreviewResponse{Kind: "binary", MimeType: mimeType, SizeStr: humanSize(entry.Size)}
	}
}

func handleAction(w http.ResponseWriter, r *http.Request) {
	var req ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	entry := session.currentEntry()

	switch req.Action {
	case "delete":
		if entry != nil {
			session.ToDelete = append(session.ToDelete, entry.Path)
			session.AlreadyMarked[entry.Path] = true
			appendToDeleteFile(entry.Path)
			session.advance()
		}

	case "keep", "skip":
		session.advance()

	case "enter":
		if entry == nil || !entry.IsDir {
			break
		}
		entries, err := loadDir(entry.Path, session.AlreadyMarked)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Advance past this directory in parent before pushing
		session.currentLevel().Index++
		session.Stack = append(session.Stack, DirLevel{
			Dir:     entry.Path,
			Entries: entries,
			Index:   0,
		})
		// If the pushed dir is empty, pop it immediately
		if len(entries) == 0 {
			session.Stack = session.Stack[:len(session.Stack)-1]
			// Check if parent is now exhausted
			if session.currentLevel().Index >= len(session.currentLevel().Entries) {
				session.advance()
			}
		}

	case "back":
		if len(session.Stack) > 1 {
			session.Stack = session.Stack[:len(session.Stack)-1]
		}

	case "quit":
		session.Done = true
	}

	w.Write([]byte(`{"ok":true}`))
}

// ---------------------------------------------------------------------------
// Execute delete handler
// ---------------------------------------------------------------------------

type ExecuteResult struct {
	Path    string `json:"path"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type ExecuteResponse struct {
	Results []ExecuteResult `json:"results"`
	Trashed int             `json:"trashed"`
	Failed  int             `json:"failed"`
}

func trashPath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		// Prefer `trash` CLI (brew install trash) — faster than AppleScript
		if _, err := exec.LookPath("trash"); err == nil {
			return exec.Command("trash", path).Run()
		}
		// Fall back to AppleScript / Finder
		script := fmt.Sprintf(`tell application "Finder" to delete POSIX file %q`, path)
		out, err := exec.Command("osascript", "-e", script).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "linux":
		if _, err := exec.LookPath("gio"); err == nil {
			return exec.Command("gio", "trash", path).Run()
		}
		if _, err := exec.LookPath("trash-put"); err == nil {
			return exec.Command("trash-put", path).Run()
		}
		return fmt.Errorf("no trash utility found — install gio (GNOME) or trash-cli")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func handleExecuteDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	mode := r.URL.Query().Get("mode") // "trash" (default) or "permanent"

	f, err := os.Open(outputFile)
	if err != nil {
		http.Error(w, `{"error":"could not open to_delete.txt"}`, http.StatusInternalServerError)
		return
	}
	defer f.Close()

	var paths []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			paths = append(paths, line)
		}
	}

	var results []ExecuteResult
	trashed, failed := 0, 0
	for _, path := range paths {
		var execErr error
		if mode == "permanent" {
			execErr = os.RemoveAll(path)
		} else {
			execErr = trashPath(path)
		}
		if execErr != nil {
			results = append(results, ExecuteResult{Path: path, Success: false, Error: execErr.Error()})
			failed++
		} else {
			results = append(results, ExecuteResult{Path: path, Success: true})
			trashed++
		}
	}

	json.NewEncoder(w).Encode(ExecuteResponse{
		Results: results,
		Trashed: trashed,
		Failed:  failed,
	})
}

func handleServeFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, path)
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "linux":
		cmd, args = "xdg-open", []string{url}
	default:
		return
	}
	exec.Command(cmd, args...).Start()
}

func main() {
	var dir string
	if len(os.Args) >= 2 {
		dir = os.Args[1]
	} else {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error resolving executable path:", err)
			os.Exit(1)
		}
		// Resolve symlinks so we get the real location on disk
		exe, _ = filepath.EvalSymlinks(exe)
		dir = filepath.Dir(exe)
	}

	dir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	alreadyMarked := loadAlreadyMarked()
	entries, err := loadDir(dir, alreadyMarked)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading directory:", err)
		os.Exit(1)
	}

	session = &Session{
		RootDir:       dir,
		Stack:         []DirLevel{{Dir: dir, Entries: entries, Index: 0}},
		AlreadyMarked: alreadyMarked,
	}
	if len(entries) == 0 {
		session.Done = true
	}

	staticFS, _ := fs.Sub(staticFiles, "static")
	http.Handle("/", http.FileServer(http.FS(staticFS)))
	http.HandleFunc("/api/state", handleState)
	http.HandleFunc("/api/preview", handlePreview)
	http.HandleFunc("/api/action", handleAction)
	http.HandleFunc("/api/execute-delete", handleExecuteDelete)
	http.HandleFunc("/file", handleServeFile)

	addr := ":8080"
	fmt.Printf("Reviewing: %s\n", dir)
	fmt.Printf("Open: http://localhost%s\n", addr)
	if len(alreadyMarked) > 0 {
		fmt.Printf("Skipping %d already-marked paths\n", len(alreadyMarked))
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		openBrowser("http://localhost" + addr)
	}()

	log.Fatal(http.ListenAndServe(addr, nil))
}
