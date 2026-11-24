package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed ui/*
var staticFiles embed.FS

const maxIndexedFiles = 350000

type FileItem struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	Category        string `json:"category"`
	Extension       string `json:"extension"`
	Size            int64  `json:"size"`
	Modified        int64  `json:"modified"`
	InDownloads     bool   `json:"inDownloads"`
	DupGroup        string `json:"dupGroup,omitempty"`
	CanTrash        bool   `json:"canTrash"`
	SafetyReason    string `json:"safetyReason,omitempty"`
	DuplicateKeeper bool   `json:"duplicateKeeper,omitempty"`
	ModifiedNanos   int64  `json:"-"`
}

type RootInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type CategoryInfo struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
}

type DriveInfo struct {
	Name  string `json:"name"`
	Total int64  `json:"total"`
	Free  int64  `json:"free"`
	Used  int64  `json:"used"`
}

type AppInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Publisher   string `json:"publisher"`
	Size        int64  `json:"size"`
	SizeKnown   bool   `json:"sizeKnown"`
	InstallPath string `json:"installPath"`
	PathKnown   bool   `json:"pathKnown"`
	PathExists  bool   `json:"pathExists"`
}

type JobState struct {
	State     string `json:"state"`
	Current   string `json:"current"`
	Processed int    `json:"processed"`
	Skipped   int    `json:"skipped"`
	Errors    int    `json:"errors"`
	Limited   bool   `json:"limited"`
	Message   string `json:"message"`
	Started   int64  `json:"started"`
	Completed int64  `json:"completed"`
}

type DuplicateGroup struct {
	ID         string     `json:"id"`
	Size       int64      `json:"size"`
	ExtraBytes int64      `json:"extraBytes"`
	Files      []FileItem `json:"files"`
}

type Manager struct {
	mu                sync.RWMutex
	files             []FileItem
	byID              map[string]int
	categories        []CategoryInfo
	scan              JobState
	dup               JobState
	dupGroups         []DuplicateGroup
	scanCancel        context.CancelFunc
	dupCancel         context.CancelFunc
	roots             []RootInfo
	selectedRoots     []string
	drives            []DriveInfo
	apps              []AppInfo
	token             string
	allowedHost       string // Exact loopback address+port; prevents DNS rebinding
	srv               *http.Server
	sessionMu         sync.Mutex
	sessions          map[string]time.Time
	hadSession        bool
	noSessionAt       time.Time
	stopOnce          sync.Once
	windowSeen        atomic.Bool // A real Windows app window is the authoritative owner.
	windowMarker      string
	relaxedProtection bool               // defaults to false: safer mode always ON at launch
	recycleFile       func(string) error // test seam; nil uses OS recycle bin
}

var categoryNames = []struct{ Key, Name string }{
	{"photos", "사진"}, {"videos", "동영상"}, {"documents", "문서"},
	{"audio", "음악"}, {"archives", "압축 파일"}, {"installers", "설치 파일"}, {"others", "기타 파일"},
}

var extensionCategories = map[string]string{}

func init() {
	for key, exts := range map[string]string{
		"photos":     "jpg jpeg png gif bmp webp heic heif tif tiff svg raw cr2 nef arw dng avif ico",
		"videos":     "mp4 mov avi mkv wmv flv webm m4v mpg mpeg mts m2ts 3gp ts",
		"documents":  "pdf doc docx xls xlsx xlsm ppt pptx hwp hwpx txt rtf odt ods odp csv md json xml log ini ipynb epub pages numbers key",
		"audio":      "mp3 wav flac m4a aac ogg wma aiff ape opus mid midi",
		"archives":   "zip rar 7z tar gz bz2 xz tgz cab zst lzh",
		"installers": "exe msi msix msixbundle appx appxbundle iso img apk bat cmd ps1",
	} {
		for _, ext := range strings.Fields(exts) {
			extensionCategories["."+ext] = key
		}
	}
}

func categoryOf(name string) string {
	if c, ok := extensionCategories[strings.ToLower(filepath.Ext(name))]; ok {
		return c
	}
	return "others"
}

func jsonResponse(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, code int, message string) {
	jsonResponse(w, code, map[string]string{"error": message})
}

// Reject oversized and concatenated/trailing JSON instead of accepting the
// first object while ignoring attacker-controlled bytes after it.
func readBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	const maxBody = 128 * 1024
	content, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(content) > maxBody {
		return errors.New("요청 본문이 너무 큽니다")
	}
	d := json.NewDecoder(bytes.NewReader(content))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("요청 본문에는 JSON 객체 1개만 포함할 수 있습니다")
	}
	return nil
}

func newManager() *Manager {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	m := &Manager{token: hex.EncodeToString(b), windowMarker: "DeskCare-" + hex.EncodeToString(b[:6]), byID: make(map[string]int), sessions: make(map[string]time.Time), roots: recommendedRoots(), drives: listDrives(), apps: installedApps()}
	m.scan = JobState{State: "idle"}
	m.dup = JobState{State: "idle"}
	m.categories = aggregate(nil)
	for _, root := range m.roots {
		m.selectedRoots = append(m.selectedRoots, root.Path)
	}
	return m
}

func aggregate(files []FileItem) []CategoryInfo {
	stats := map[string]*CategoryInfo{}
	for _, x := range categoryNames {
		stats[x.Key] = &CategoryInfo{Key: x.Key, Name: x.Name}
	}
	for _, f := range files {
		x := stats[f.Category]
		if x != nil {
			x.Count++
			x.Size += f.Size
		}
	}
	out := make([]CategoryInfo, 0, len(categoryNames))
	for _, x := range categoryNames {
		out = append(out, *stats[x.Key])
	}
	return out
}

func (m *Manager) overview(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	var totalSize int64
	for _, c := range m.categories {
		totalSize += c.Size
	}
	c := append([]CategoryInfo(nil), m.categories...)
	roots := append([]RootInfo(nil), m.roots...)
	drives := listDrives()
	selected := append([]string(nil), m.selectedRoots...)
	count, appCount := len(m.files), len(m.apps)
	protectedFiles, protectedBytes := protectedCount(m.files), protectedSize(m.files)
	protectionOn := !m.relaxedProtection
	s, d := m.scan, m.dup
	m.mu.RUnlock()
	jsonResponse(w, 200, map[string]any{"categories": c, "roots": roots, "selectedRoots": selected,
		"drives": drives, "fileCount": count, "totalSize": totalSize, "appCount": appCount,
		"scan": s, "duplicates": d, "duplicateCount": m.duplicateCount(),
		"protectedFiles": protectedFiles, "protectedSize": protectedBytes,
		"autoProtectionEnabled": protectionOn})
}
func protectedCount(files []FileItem) int {
	n := 0
	for _, f := range files {
		if !f.CanTrash {
			n++
		}
	}
	return n
}
func protectedSize(files []FileItem) int64 {
	var size int64
	for _, f := range files {
		if !f.CanTrash {
			size += f.Size
		}
	}
	return size
}

func (m *Manager) duplicateCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.dupGroups)
}

func recommendedRoots() []RootInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	defs := []struct{ Name, Folder string }{{"다운로드", "Downloads"}, {"바탕화면", "Desktop"}, {"문서", "Documents"}, {"사진", "Pictures"}, {"동영상", "Videos"}, {"음악", "Music"}}
	candidates := []string{home}
	if oneDrive := os.Getenv("OneDrive"); oneDrive != "" && !strings.EqualFold(filepath.Clean(oneDrive), filepath.Clean(home)) {
		candidates = append(candidates, oneDrive)
	}
	seen := map[string]bool{}
	var roots []RootInfo
	for _, base := range candidates {
		for _, d := range defs {
			p := filepath.Join(base, d.Folder)
			st, err := os.Stat(p)
			if err != nil || !st.IsDir() || blockedDirectory(p) {
				continue
			}
			norm := strings.ToLower(filepath.Clean(p))
			if seen[norm] {
				continue
			}
			seen[norm] = true
			name := d.Name
			if base != home {
				name += " (OneDrive)"
			}
			roots = append(roots, RootInfo{Name: name, Path: p})
		}
	}
	return roots
}

func blockedFile(path string) bool { return protectedFileName(path) }
func blockedDirectory(path string) bool {
	return protectedDirectoryPath(path) || projectDirectoryTree(path)
}

func canScanRoot(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("폴더가 비어 있습니다")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	st, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("실제 폴더만 선택할 수 있습니다")
	}
	if blockedDirectory(abs) {
		return "", errors.New("시스템 또는 프로그램 폴더는 보호되어 분석하지 않습니다")
	}
	if overlyBroadRoot(abs) {
		return "", errors.New("드라이브 전체나 사용자 홈 전체는 분석할 수 없습니다. 더 작은 폴더를 선택하세요")
	}
	if reason := osScanRootProtectionReason(abs); reason != "" {
		return "", fmt.Errorf("안전하지 않은 폴더 경로: %s", reason)
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(abs, `\\`) {
		return "", errors.New("네트워크 폴더는 지원하지 않습니다")
	}
	return abs, nil
}

func uniqueRoots(paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > 30 {
		return nil, errors.New("1~30개의 폴더를 선택하세요")
	}
	seen := map[string]bool{}
	var result []string
	for _, path := range paths {
		clean, err := canScanRoot(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		key := strings.ToLower(clean)
		if !seen[key] {
			seen[key] = true
			result = append(result, clean)
		}
	}
	return result, nil
}

func underRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func isDownloads(path string) bool {
	sep := string(filepath.Separator)
	lower := strings.ToLower(path)
	return strings.Contains(lower, sep+"downloads"+sep)
}

func (m *Manager) startScan(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Paths []string `json:"paths"`
	}
	if err := readBody(r, &input); err != nil {
		apiError(w, 400, "잘못된 요청입니다")
		return
	}
	paths, err := uniqueRoots(input.Paths)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	m.mu.Lock()
	if m.scan.State == "running" || m.dup.State == "running" {
		m.mu.Unlock()
		apiError(w, 409, "다른 분석 작업이 실행 중입니다")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.scanCancel = cancel
	m.scan = JobState{State: "running", Started: time.Now().Unix(), Message: "파일을 확인하는 중입니다"}
	m.selectedRoots = paths
	m.dup = JobState{State: "idle"}
	m.dupGroups = nil
	m.mu.Unlock()
	go m.scanFiles(ctx, paths)
	jsonResponse(w, 202, map[string]bool{"started": true})
}

func (m *Manager) scanFiles(ctx context.Context, paths []string) {
	files := make([]FileItem, 0, 8192)
	seen := make(map[string]struct{})
	projectCache := make(map[string]bool)
	var processed, skipped, errs int
	limited := false
	lastUpdate := time.Now()
	m.mu.RLock()
	relaxed := m.relaxedProtection
	m.mu.RUnlock()
	update := func(current string) {
		m.mu.Lock()
		m.scan.Processed = processed
		m.scan.Skipped = skipped
		m.scan.Errors = errs
		m.scan.Current = current
		m.scan.Limited = limited
		m.mu.Unlock()
	}
	for _, root := range paths {
		if ctx.Err() != nil {
			break
		}
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				errs++
				return nil
			}
			if entry.IsDir() {
				if path != root && (blockedDirectory(path) || osSkipScanDirectory(path)) {
					skipped++
					return filepath.SkipDir
				}
				if entry.Type()&os.ModeSymlink != 0 {
					skipped++
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() && entry.Type() != 0 {
				skipped++
				return nil
			}
			if blockedFile(path) {
				skipped++
				return nil
			}
			st, err := entry.Info()
			if err != nil {
				errs++
				return nil
			}
			if !st.Mode().IsRegular() {
				skipped++
				return nil
			}
			norm := strings.ToLower(filepath.Clean(path))
			if _, ok := seen[norm]; ok {
				return nil
			}
			seen[norm] = struct{}{}
			if len(files) >= maxIndexedFiles {
				limited = true
				return errors.New("file_limit")
			}
			processed++
			reason := protectionReasonWithMode(path, paths, projectCache, relaxed)
			files = append(files, FileItem{
				ID: strconv.Itoa(len(files) + 1), Name: entry.Name(), Path: path,
				Category: categoryOf(entry.Name()), Extension: strings.ToLower(filepath.Ext(entry.Name())),
				Size: st.Size(), Modified: st.ModTime().Unix(), ModifiedNanos: st.ModTime().UnixNano(),
				InDownloads: isDownloads(path), CanTrash: reason == "", SafetyReason: reason,
			})
			if processed%500 == 0 || time.Since(lastUpdate) > 650*time.Millisecond {
				update(filepath.Dir(path))
				lastUpdate = time.Now()
			}
			return nil
		})
		if limited {
			break
		}
		update(root)
	}
	m.mu.Lock()
	// Cancellation discards the incomplete new index, preserving the previous scan.
	if ctx.Err() != nil {
		m.scan.State = "cancelled"
		m.scan.Message = "분석이 취소되어 이전 결과를 유지했습니다"
	} else {
		m.files = files
		m.byID = make(map[string]int, len(files))
		for i, f := range files {
			m.byID[f.ID] = i
		}
		m.categories = aggregate(files)
		m.scan.State = "done"
		m.scan.Message = "분석이 완료되었습니다"
		if limited {
			m.scan.Message = "파일 수 제한으로 일부만 분석했습니다"
		}
	}
	m.scan.Processed = processed
	m.scan.Skipped = skipped
	m.scan.Errors = errs
	m.scan.Limited = limited
	m.scan.Current = ""
	m.scan.Completed = time.Now().Unix()
	m.scanCancel = nil
	m.mu.Unlock()
}

func (m *Manager) cancel(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	if m.scanCancel != nil {
		m.scanCancel()
	}
	if m.dupCancel != nil {
		m.dupCancel()
	}
	m.mu.Unlock()
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// All smart-cleanup views operate on the same scanned inventory as the
// "전체 파일" page. Installed applications are a separate Windows registry list.
func matchesSmartView(f FileItem, view string, now time.Time) bool {
	switch view {
	case "", "all":
		return true
	case "large":
		return f.Size >= 200*1024*1024
	case "old":
		// All scanned folders, not only Downloads. Use modified time, not last access.
		return f.Modified > 0 && f.Modified <= now.AddDate(0, 0, -90).Unix()
	case "old_downloads":
		return f.InDownloads && f.Modified > 0 && f.Modified <= now.AddDate(0, 0, -90).Unix()
	case "installers":
		return f.Category == "installers"
	case "duplicates":
		return f.DupGroup != ""
	default:
		return false
	}
}

func (m *Manager) getFiles(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	cat := r.URL.Query().Get("category")
	view := r.URL.Query().Get("view")
	showProtected := r.URL.Query().Get("showProtected") == "true"
	order := r.URL.Query().Get("sort")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 300 {
		limit = 100
	}
	m.mu.RLock()
	files := make([]FileItem, 0)
	now := time.Now()
	for _, f := range m.files {
		if cat != "" && cat != "all" && f.Category != cat {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(f.Name), q) && !strings.Contains(strings.ToLower(f.Path), q) {
			continue
		}
		// Visibility is independent of deletion permission. Protected items can
		// be inspected, but their checkbox stays disabled. Applies to all views.
		if !showProtected && !f.CanTrash {
			continue
		}
		if !matchesSmartView(f, view, now) {
			continue
		}
		files = append(files, f)
	}
	m.mu.RUnlock()
	switch order {
	case "size_asc":
		sort.Slice(files, func(i, j int) bool { return files[i].Size < files[j].Size })
	case "name":
		sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	case "modified":
		sort.Slice(files, func(i, j int) bool { return files[i].Modified > files[j].Modified })
	case "modified_asc":
		sort.Slice(files, func(i, j int) bool { return files[i].Modified < files[j].Modified })
	default:
		sort.Slice(files, func(i, j int) bool { return files[i].Size > files[j].Size })
	}
	total := len(files)
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	jsonResponse(w, 200, map[string]any{"files": files[offset:end], "total": total, "totalSize": totalSize, "offset": offset, "limit": limit})
}

// setProtectionMode switches only the optional extension allowlist. No API
// request can bypass hard path/attribute checks or the duplicate keeper rule.
// Rebuild the displayed eligibility from the server's indexed paths so a stale
// client selection or a toggle does not silently widen deletion permission.
func (m *Manager) setProtectionMode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readBody(r, &input); err != nil || input.Enabled == nil {
		apiError(w, 400, "자동 보호 사용 여부를 지정하세요")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scan.State == "running" || m.dup.State == "running" {
		apiError(w, 409, "분석이 끝난 뒤 보호 설정을 변경하세요")
		return
	}
	relaxed := !*input.Enabled
	if m.relaxedProtection != relaxed {
		m.relaxedProtection = relaxed
		cache := make(map[string]bool)
		for i := range m.files {
			f := &m.files[i]
			reason := protectionReasonWithMode(f.Path, m.selectedRoots, cache, relaxed)
			if f.DuplicateKeeper {
				reason = "중복 그룹에서 보호하는 보관용 사본"
			}
			f.CanTrash = reason == ""
			f.SafetyReason = reason
			f.DupGroup = ""
		}
		// Duplicate keepers must be recalculated after permission changes.
		// No saved duplicate group or keeper eligibility can become stale.
		m.dupGroups = nil
		m.dup = JobState{State: "idle", Message: "보호 설정이 변경되어 중복 검사를 다시 실행해야 합니다"}
	}
	jsonResponse(w, 200, map[string]any{
		"autoProtectionEnabled": !m.relaxedProtection,
		"protectedFiles":        protectedCount(m.files),
		"protectedSize":         protectedSize(m.files),
	})
}

func (m *Manager) startDuplicates(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	if m.scan.State == "running" || m.dup.State == "running" {
		m.mu.Unlock()
		apiError(w, 409, "분석이 실행 중입니다")
		return
	}
	if len(m.files) == 0 {
		m.mu.Unlock()
		apiError(w, 400, "먼저 파일 분석을 실행하세요")
		return
	}
	copyFiles := append([]FileItem(nil), m.files...)
	ctx, cancel := context.WithCancel(context.Background())
	m.dupCancel = cancel
	m.dup = JobState{State: "running", Started: time.Now().Unix(), Message: "크기가 같은 파일을 비교하고 있습니다"}
	m.dupGroups = nil
	for i := range m.files {
		m.files[i].DupGroup = ""
	}
	m.mu.Unlock()
	go m.findDuplicates(ctx, copyFiles)
	jsonResponse(w, 202, map[string]bool{"started": true})
}

func (m *Manager) findDuplicates(ctx context.Context, files []FileItem) {
	bySize := make(map[int64][]FileItem)
	for _, f := range files {
		// A keeper is still a hash candidate when the user runs a new check,
		// but it must never become deletable merely by changing modes.
		if !f.CanTrash && !f.DuplicateKeeper {
			continue
		}
		if f.Size > 0 && f.Size <= 4*1024*1024*1024 {
			bySize[f.Size] = append(bySize[f.Size], f)
		}
	}
	candidates := make([]FileItem, 0)
	for _, group := range bySize {
		if len(group) > 1 {
			candidates = append(candidates, group...)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Size < candidates[j].Size })
	const maxCandidates = 25000
	limited := len(candidates) > maxCandidates
	if limited {
		candidates = candidates[:maxCandidates]
	}
	match := make(map[string][]FileItem)
	checked := 0
	for _, f := range candidates {
		if ctx.Err() != nil {
			break
		}
		// Re-check after the scan. A path may have become a junction, a
		// symlink, a cloud placeholder or a protected file in the meantime.
		if reason := protectionReasonWithMode(f.Path, m.selectedRoots, nil, m.relaxedProtection); reason != "" {
			continue
		}
		actual, err := filepath.EvalSymlinks(f.Path)
		if err != nil || !strings.EqualFold(filepath.Clean(actual), filepath.Clean(f.Path)) {
			continue
		}
		file, err := os.Open(f.Path)
		if err != nil {
			continue
		}
		st, err := file.Stat()
		if err != nil || st.Size() != f.Size || st.ModTime().Unix() != f.Modified {
			file.Close()
			continue
		}
		hasher := sha256.New()
		_, err = io.Copy(hasher, file)
		file.Close()
		if err != nil {
			continue
		}
		key := strconv.FormatInt(f.Size, 10) + ":" + hex.EncodeToString(hasher.Sum(nil))
		match[key] = append(match[key], f)
		checked++
		if checked%10 == 0 {
			m.mu.Lock()
			m.dup.Processed = checked
			m.dup.Current = fmt.Sprintf("%d / %d개 파일 확인", checked, len(candidates))
			m.mu.Unlock()
		}
	}
	var groups []DuplicateGroup
	for hash, items := range match {
		if len(items) < 2 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Modified > items[j].Modified })
		gid := hash[len(hash)-16:]
		groups = append(groups, DuplicateGroup{ID: gid, Size: items[0].Size, ExtraBytes: int64(len(items)-1) * items[0].Size, Files: items})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ExtraBytes > groups[j].ExtraBytes })
	m.mu.Lock()
	if ctx.Err() != nil {
		m.dup.State = "cancelled"
		m.dup.Message = "중복 검사가 취소되었습니다"
	} else {
		byID := make(map[string]string)
		keepers := make(map[string]bool)
		for gi := range groups {
			// Keep one copy from every duplicate group, even if the user selects
			// all rows or batches multiple deletion requests.
			keepers[groups[gi].Files[0].ID] = true
			for fi := range groups[gi].Files {
				f := &groups[gi].Files[fi]
				byID[f.ID] = groups[gi].ID
				if fi == 0 {
					f.DuplicateKeeper = true
					f.CanTrash = false
					f.SafetyReason = "중복 그룹에서 보호하는 보관용 사본"
				}
			}
		}
		for i := range m.files {
			m.files[i].DupGroup = byID[m.files[i].ID]
			if keepers[m.files[i].ID] {
				m.files[i].DuplicateKeeper = true
				m.files[i].CanTrash = false
				m.files[i].SafetyReason = "중복 그룹에서 보호하는 보관용 사본"
			}
		}
		m.dupGroups = groups
		m.dup.State = "done"
		m.dup.Message = "중복 검사가 완료되었습니다. 각 그룹의 보관용 사본 1개는 보호합니다"
	}
	m.dup.Limited = limited
	m.dup.Processed = checked
	m.dup.Current = ""
	m.dup.Completed = time.Now().Unix()
	m.dupCancel = nil
	m.mu.Unlock()
}

func (m *Manager) getDuplicates(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	groups := append([]DuplicateGroup(nil), m.dupGroups...)
	job := m.dup
	m.mu.RUnlock()
	var reclaim int64
	for _, g := range groups {
		reclaim += g.ExtraBytes
	}
	if groups == nil {
		groups = []DuplicateGroup{}
	}
	jsonResponse(w, 200, map[string]any{"state": job, "groups": groups, "reclaimable": reclaim})
}

func (m *Manager) trash(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := readBody(r, &input); err != nil {
		apiError(w, 400, "잘못된 요청입니다")
		return
	}
	if len(input.IDs) == 0 || len(input.IDs) > 50 {
		apiError(w, 400, "1회에 최대 50개 파일을 선택할 수 있습니다")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scan.State == "running" || m.dup.State == "running" {
		apiError(w, 409, "분석이 진행 중일 때는 삭제할 수 없습니다")
		return
	}
	// IDs refer only to files from the current scan; external paths are never accepted.
	done := map[string]bool{}
	// Keep both JSON fields as arrays, including when empty. A nil slice
	// encodes to null, which caused the UI to crash *after* moving files.
	removed := make([]string, 0, len(input.IDs))
	failed := make([]string, 0)
	for _, id := range input.IDs {
		if done[id] {
			continue
		}
		done[id] = true
		idx, exists := m.byID[id]
		if !exists {
			failed = append(failed, "알 수 없는 파일 ID: "+id)
			continue
		}
		f := m.files[idx]
		safe := false
		for _, root := range m.selectedRoots {
			if underRoot(f.Path, root) {
				safe = true
				break
			}
		}
		if !safe {
			failed = append(failed, f.Name+": 분석 범위 밖")
			continue
		}
		// Never rely on a client-provided checkbox or on a stale scan result.
		// Deny by default, and re-check the full policy and filesystem attributes.
		if !f.CanTrash {
			failed = append(failed, f.Name+": 보호된 항목 - "+f.SafetyReason)
			continue
		}
		if reason := protectionReasonWithMode(f.Path, m.selectedRoots, nil, m.relaxedProtection); reason != "" {
			failed = append(failed, f.Name+": 보호된 항목 - "+reason)
			continue
		}
		st, err := os.Lstat(f.Path)
		if err != nil || !st.Mode().IsRegular() || st.Size() != f.Size || st.ModTime().UnixNano() != f.ModifiedNanos {
			failed = append(failed, f.Name+": 파일이 변경되었거나 접근할 수 없음")
			continue
		}
		real, err := filepath.EvalSymlinks(f.Path)
		if err != nil || !strings.EqualFold(filepath.Clean(real), filepath.Clean(f.Path)) {
			failed = append(failed, f.Name+": 바로가기/연결 폴더는 이동할 수 없음")
			continue
		}
		recycler := m.recycleFile
		if recycler == nil {
			recycler = moveToRecycleBin
		}
		if err := recycler(f.Path); err != nil {
			failed = append(failed, f.Name+": "+err.Error())
			continue
		}
		removed = append(removed, id)
	}
	if len(removed) > 0 {
		gone := map[string]bool{}
		for _, id := range removed {
			gone[id] = true
		}
		keep := make([]FileItem, 0, len(m.files)-len(removed))
		m.byID = make(map[string]int, len(m.files)-len(removed))
		for _, f := range m.files {
			if !gone[f.ID] {
				m.byID[f.ID] = len(keep)
				keep = append(keep, f)
			}
		}
		m.files = keep
		m.categories = aggregate(keep)
		// Dup groups can be stale after removal; reset and invite another check.
		for i := range m.files {
			m.files[i].DupGroup = ""
		}
		m.dupGroups = nil
		m.dup = JobState{State: "idle", Message: "파일 정리 후 중복 검사 결과가 초기화되었습니다"}
	}
	jsonResponse(w, 200, map[string]any{"removed": removed, "failed": failed})
}

func (m *Manager) openFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if err := readBody(r, &input); err != nil {
		apiError(w, 400, "잘못된 요청입니다")
		return
	}
	m.mu.RLock()
	idx, ok := m.byID[input.ID]
	var p string
	if ok {
		p = m.files[idx].Path
	}
	m.mu.RUnlock()
	if !ok {
		apiError(w, 404, "파일을 찾을 수 없습니다")
		return
	}
	if err := revealInExplorer(p); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func (m *Manager) chooseFolder(w http.ResponseWriter, r *http.Request) {
	p, err := pickFolder()
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	path, err := canScanRoot(p)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"path": path, "name": filepath.Base(path)})
}

func (m *Manager) getApps(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	sortBy := r.URL.Query().Get("sort")
	m.mu.RLock()
	filtered := make([]AppInfo, 0, len(m.apps))
	for _, app := range m.apps {
		if query == "" || strings.Contains(strings.ToLower(app.Name+" "+app.Publisher+" "+app.InstallPath), query) {
			filtered = append(filtered, app)
		}
	}
	m.mu.RUnlock()
	switch sortBy {
	case "name":
		sort.Slice(filtered, func(i, j int) bool { return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name) })
	default:
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Size > filtered[j].Size })
	}
	var knownSize int64
	for _, app := range filtered {
		if app.SizeKnown {
			knownSize += app.Size
		}
	}
	jsonResponse(w, 200, map[string]any{"apps": filtered, "total": len(filtered), "estimatedTotal": knownSize})
}

func (m *Manager) openSettings(w http.ResponseWriter, r *http.Request) {
	if err := openAppSettings(); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// Only IDs from the server-side installed-app registry can open an Explorer
// folder. Never accept an arbitrary path from the browser.
func (m *Manager) openAppFolder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if err := readBody(r, &input); err != nil || input.ID == "" {
		apiError(w, 400, "앱 식별자를 확인하세요")
		return
	}
	m.mu.RLock()
	var path string
	for _, app := range m.apps {
		if app.ID == input.ID && app.PathKnown && app.PathExists {
			path = app.InstallPath
			break
		}
	}
	m.mu.RUnlock()
	if path == "" {
		apiError(w, 404, "확인된 설치 경로가 없습니다")
		return
	}
	if err := revealInstallFolder(path); err != nil {
		apiError(w, 400, "설치 폴더를 열 수 없습니다: "+err.Error())
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// Excel/LibreOffice may interpret CSV fields beginning with =, +, - or @
// as formulas. Prefix spreadsheet-dangerous cells with a literal apostrophe.
func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	}
	return value
}

func (m *Manager) exportCSV(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	files := append([]FileItem(nil), m.files...)
	m.mu.RUnlock()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="deskcare-files.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	out := csv.NewWriter(w)
	_ = out.Write([]string{"파일명", "경로", "분류", "크기(바이트)", "수정일"})
	cats := map[string]string{}
	for _, x := range categoryNames {
		cats[x.Key] = x.Name
	}
	for _, f := range files {
		_ = out.Write([]string{safeCSVCell(f.Name), safeCSVCell(f.Path), cats[f.Category], strconv.FormatInt(f.Size, 10), time.Unix(f.Modified, 0).Format("2006-01-02 15:04")})
	}
	out.Flush()
}

// Browser tabs are tracked independently: one tab closing must not shut down
// the app while another is still connected. A pagehide beacon is the fast path;
// lease expiry handles browser crashes, forced closes and missed beacons.
const (
	sessionExpiry = 12 * time.Second
	closeGrace    = 2 * time.Second
	startupGrace  = 90 * time.Second
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,100}$`)

type sessionRequest struct {
	ID string `json:"id"`
}

func (m *Manager) markSession(id string, now time.Time) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[string]time.Time)
	}
	m.sessions[id] = now
	m.hadSession = true
	m.noSessionAt = time.Time{}
}

func (m *Manager) forgetSession(id string, now time.Time) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	delete(m.sessions, id)
	if m.hadSession && len(m.sessions) == 0 && m.noSessionAt.IsZero() {
		m.noSessionAt = now
	}
}

// Called by the watchdog. The returned true value means the window(s) are gone
// long enough that the executable should stop (and release its file lock).
func (m *Manager) shouldExit(now, started time.Time) bool {
	// Native Windows window detection overrides JS timers, which may be throttled.
	// The native watcher calls stopServer immediately after that window closes.
	if m.windowSeen.Load() {
		return false
	}
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if !m.hadSession {
		return now.Sub(started) >= startupGrace
	}
	for id, seen := range m.sessions {
		if now.Sub(seen) > sessionExpiry {
			delete(m.sessions, id)
		}
	}
	if len(m.sessions) > 0 {
		m.noSessionAt = time.Time{}
		return false
	}
	if m.noSessionAt.IsZero() {
		m.noSessionAt = now
	}
	return now.Sub(m.noSessionAt) >= closeGrace
}

func (m *Manager) stopServer() {
	m.stopOnce.Do(func() {
		// Graceful server shutdown releases the EXE after Serve returns.
		// Cancel long-running background scans before shutdown.
		m.mu.Lock()
		if m.scanCancel != nil {
			m.scanCancel()
		}
		if m.dupCancel != nil {
			m.dupCancel()
		}
		m.mu.Unlock()
		go func() {
			if m.srv == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = m.srv.Shutdown(ctx)
			_ = m.srv.Close()
		}()
	})
}

func (m *Manager) sessionOpen(w http.ResponseWriter, r *http.Request) {
	var body sessionRequest
	if err := readBody(r, &body); err != nil || !sessionIDPattern.MatchString(body.ID) {
		apiError(w, 400, "유효하지 않은 창 식별자입니다")
		return
	}
	m.markSession(body.ID, time.Now())
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func (m *Manager) sessionClose(w http.ResponseWriter, r *http.Request) {
	var body sessionRequest
	if err := readBody(r, &body); err != nil || !sessionIDPattern.MatchString(body.ID) {
		apiError(w, 400, "유효하지 않은 창 식별자입니다")
		return
	}
	m.forgetSession(body.ID, time.Now())
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// navigator.sendBeacon cannot send a custom authentication header, so it
// submits the same session ID and per-launch secret in the request body.
func (m *Manager) sessionBeacon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, 405, "지원하지 않는 요청 방식입니다")
		return
	}
	var body struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := readBody(r, &body); err != nil || !sessionIDPattern.MatchString(body.ID) ||
		subtle.ConstantTimeCompare([]byte(body.Token), []byte(m.token)) != 1 {
		apiError(w, 403, "유효하지 않은 종료 요청입니다")
		return
	}
	m.forgetSession(body.ID, time.Now())
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func (m *Manager) sessionWatchdog() {
	started := time.Now()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if m.shouldExit(time.Now(), started) {
			m.stopServer()
			return
		}
	}
}

func (m *Manager) shutdown(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]bool{"ok": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		closeDeskCareWindow(m.windowMarker)
		m.stopServer()
	}()
}

// Without an exact Host check a malicious website could potentially reach
// the unprivileged loopback server via DNS rebinding, read the launch token,
// and make authenticated filesystem requests. Only this launched 127.0.0.1
// origin can use the app. CORS is intentionally not enabled.
func (m *Manager) allowLocalRequest(r *http.Request) bool {
	if m.allowedHost == "" {
		return true
	} // Explicit unit-test/handler mode only.
	if r.Host != m.allowedHost {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" || u.Host != m.allowedHost || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return true
}

func (m *Manager) routes() http.Handler {
	mux := http.NewServeMux()
	asset := http.FileServer(http.FS(staticFiles))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := staticFiles.ReadFile("ui/index.html")
		if err != nil {
			http.Error(w, "UI 파일을 읽을 수 없습니다", 500)
			return
		}
		html := strings.ReplaceAll(string(data), "{{APP_TOKEN}}", m.token)
		html = strings.ReplaceAll(html, "{{WINDOW_MARKER}}", m.windowMarker)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, html)
	})
	mux.Handle("/ui/", asset)
	api := func(method string, fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				apiError(w, 405, "지원하지 않는 요청 방식입니다")
				return
			}
			if r.Header.Get("X-DeskCare-Token") != m.token {
				apiError(w, 403, "앱 인증이 필요합니다. 앱을 다시 실행하세요")
				return
			}
			fn(w, r)
		}
	}
	mux.HandleFunc("/api/overview", api("GET", m.overview))
	mux.HandleFunc("/api/scan", api("POST", m.startScan))
	mux.HandleFunc("/api/cancel", api("POST", m.cancel))
	mux.HandleFunc("/api/files", api("GET", m.getFiles))
	mux.HandleFunc("/api/trash", api("POST", m.trash))
	mux.HandleFunc("/api/protection", api("POST", m.setProtectionMode))
	mux.HandleFunc("/api/open", api("POST", m.openFile))
	mux.HandleFunc("/api/pick-folder", api("POST", m.chooseFolder))
	mux.HandleFunc("/api/duplicates", api("GET", m.getDuplicates))
	mux.HandleFunc("/api/duplicates/start", api("POST", m.startDuplicates))
	mux.HandleFunc("/api/apps", api("GET", m.getApps))
	mux.HandleFunc("/api/apps/settings", api("POST", m.openSettings))
	mux.HandleFunc("/api/apps/open", api("POST", m.openAppFolder))
	mux.HandleFunc("/api/export", api("GET", m.exportCSV))
	mux.HandleFunc("/api/session/open", api("POST", m.sessionOpen))
	mux.HandleFunc("/api/session/ping", api("POST", m.sessionOpen))
	mux.HandleFunc("/api/session/close", api("POST", m.sessionClose))
	mux.HandleFunc("/api/session/beacon", m.sessionBeacon)
	mux.HandleFunc("/api/exit", api("POST", m.shutdown))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.allowLocalRequest(r) {
			http.Error(w, "이 PC에서 시작한 DeskCare 창만 연결할 수 있습니다", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; font-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	})
}

func main() {
	m := newManager()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + ln.Addr().String() + "/"
	m.allowedHost = ln.Addr().String()
	m.srv = &http.Server{Handler: m.routes(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 45 * time.Second}
	go launchDeskCare(url, m.windowMarker, func() { m.windowSeen.Store(true) }, m.stopServer)
	go m.sessionWatchdog()
	log.Printf("DeskCare running at %s", url)
	err = m.srv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Println("서버 오류:", err)
	}
}
