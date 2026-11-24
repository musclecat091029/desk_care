//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	getLogicalDrives = kernel32.NewProc("GetLogicalDrives")
	getDriveType     = kernel32.NewProc("GetDriveTypeW")
	getDiskSpace     = kernel32.NewProc("GetDiskFreeSpaceExW")
	shell32          = syscall.NewLazyDLL("shell32.dll")
	shellFileOp      = shell32.NewProc("SHFileOperationW")
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	regOpen          = advapi32.NewProc("RegOpenKeyExW")
	regEnum          = advapi32.NewProc("RegEnumKeyExW")
	regQuery         = advapi32.NewProc("RegQueryValueExW")
	regClose         = advapi32.NewProc("RegCloseKey")
)

func listDrives() []DriveInfo {
	mask, _, _ := getLogicalDrives.Call()
	var drives []DriveInfo
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		drive := fmt.Sprintf("%c:\\", 'A'+i)
		p, _ := syscall.UTF16PtrFromString(drive)
		typ, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(p)))
		if typ != 3 && typ != 2 {
			continue
		} // fixed and removable volumes
		var freeCaller, total, freeTotal uint64
		ok, _, _ := getDiskSpace.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&freeCaller)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&freeTotal)))
		if ok == 0 {
			continue
		}
		drives = append(drives, DriveInfo{Name: drive, Total: int64(total), Free: int64(freeTotal), Used: int64(total - freeTotal)})
	}
	return drives
}

const (
	regRead     = 0x20019
	regSz       = 1
	regExpandSz = 2
	regDword    = 4
	keyHKLM     = 0x80000002
	keyHKCU     = 0x80000001
)

func openKey(root uintptr, sub string) (uintptr, bool) {
	name, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		return 0, false
	}
	var key uintptr
	result, _, _ := regOpen.Call(root, uintptr(unsafe.Pointer(name)), 0, regRead, uintptr(unsafe.Pointer(&key)))
	return key, result == 0
}
func queryRegistry(key uintptr, name string) (uint32, []byte, bool) {
	p, _ := syscall.UTF16PtrFromString(name)
	var typ, size uint32
	code, _, _ := regQuery.Call(key, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)))
	if code != 0 || size > 1024*1024 || size == 0 {
		return 0, nil, false
	}
	b := make([]byte, size)
	code, _, _ = regQuery.Call(key, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&size)))
	if code != 0 {
		return 0, nil, false
	}
	return typ, b[:size], true
}
func regString(key uintptr, name string) string {
	typ, b, ok := queryRegistry(key, name)
	if !ok || (typ != regSz && typ != regExpandSz) {
		return ""
	}
	vals := make([]uint16, len(b)/2)
	for i := range vals {
		vals[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return strings.TrimSpace(syscall.UTF16ToString(vals))
}
func regDWORD(key uintptr, name string) uint32 {
	typ, b, ok := queryRegistry(key, name)
	if !ok || typ != regDword || len(b) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func installedApps() []AppInfo {
	uninstallPaths := []struct {
		root uintptr
		path string
	}{
		{keyHKLM, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{keyHKLM, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{keyHKCU, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{keyHKCU, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	seen := map[string]int{}
	var out []AppInfo
	for _, entry := range uninstallPaths {
		key, ok := openKey(entry.root, entry.path)
		if !ok {
			continue
		}
		for index := uint32(0); index < 100000; index++ {
			nameBuf := make([]uint16, 1024)
			nameLen := uint32(len(nameBuf))
			result, _, _ := regEnum.Call(key, uintptr(index), uintptr(unsafe.Pointer(&nameBuf[0])), uintptr(unsafe.Pointer(&nameLen)), 0, 0, 0, 0)
			if result == 259 {
				break
			}
			if result != 0 {
				continue
			}
			child := syscall.UTF16ToString(nameBuf[:nameLen])
			sub, ok := openKey(key, child)
			if !ok {
				continue
			}
			name := regString(sub, "DisplayName")
			publisher := regString(sub, "Publisher")
			installPath, pathExists := registryInstallLocation(regString(sub, "InstallLocation"))
			sizeKB := regDWORD(sub, "EstimatedSize")
			systemComponent := regDWORD(sub, "SystemComponent")
			parent := regString(sub, "ParentKeyName")
			release := regString(sub, "ReleaseType")
			regClose.Call(sub)
			if name == "" || systemComponent == 1 || parent != "" || strings.Contains(strings.ToLower(release), "update") {
				continue
			}
			keyName := strings.ToLower(name + "|" + publisher)
			if existing, ok := seen[keyName]; ok {
				// Duplicate uninstall entries are common: prefer a confirmed path
				// and nonzero registered size over a blank duplicate entry.
				if !out[existing].PathKnown && installPath != "" {
					out[existing].InstallPath = installPath
					out[existing].PathKnown = true
					out[existing].PathExists = pathExists
				}
				if !out[existing].SizeKnown && sizeKB > 0 {
					out[existing].Size = int64(sizeKB) * 1024
					out[existing].SizeKnown = true
				}
				continue
			}
			seen[keyName] = len(out)
			digest := sha256.Sum256([]byte(keyName))
			out = append(out, AppInfo{ID: hex.EncodeToString(digest[:12]), Name: name, Publisher: publisher,
				Size: int64(sizeKB) * 1024, SizeKnown: sizeKB > 0,
				InstallPath: installPath, PathKnown: installPath != "", PathExists: pathExists})
		}
		regClose.Call(key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

// Accept only InstallLocation explicitly registered by the application.
// Never infer the location from DisplayIcon or UninstallString, which may
// point to a shared folder or an uninstaller outside the installed program.
func registryInstallLocation(raw string) (string, bool) {
	p := strings.Trim(strings.TrimSpace(raw), `"`)
	if p == "" {
		return "", false
	}
	// Expand REG_EXPAND_SZ variables, e.g. %ProgramFiles%.
	if strings.Contains(p, "%") {
		var expanded strings.Builder
		for i := 0; i < len(p); {
			if p[i] == '%' {
				if j := strings.IndexByte(p[i+1:], '%'); j >= 0 {
					key := p[i+1 : i+1+j]
					if val := os.Getenv(key); val != "" {
						expanded.WriteString(val)
						i += j + 2
						continue
					}
				}
			}
			expanded.WriteByte(p[i])
			i++
		}
		p = expanded.String()
	}
	if len(p) < 3 || !((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) || p[1] != ':' || p[2] != '\\' || strings.ContainsAny(p, `"*?<>|`) {
		return "", false
	}
	clean := filepath.Clean(p)
	if clean == filepath.VolumeName(clean)+`\` {
		return "", false
	}
	st, err := os.Stat(clean)
	if err != nil || !st.IsDir() {
		return clean, false
	}
	return clean, true
}

// Explorer can open only a currently existing registered local directory.
func revealInstallFolder(path string) error {
	clean, exists := registryInstallLocation(path)
	if !exists || clean == "" {
		return errors.New("설치 경로가 없거나 이동되었습니다")
	}
	return exec.Command("explorer.exe", clean).Start()
}

type shFileOpStruct struct {
	hwnd          uintptr
	operation     uint32
	from          *uint16
	to            *uint16
	flags         uint16
	aborted       int32
	mappings      uintptr
	progressTitle *uint16
}

func moveToRecycleBin(path string) error {
	if strings.HasPrefix(path, `\\`) {
		return errors.New("네트워크 파일은 휴지통 이동을 지원하지 않습니다")
	}
	volume := filepath.VolumeName(path)
	if volume == "" {
		return errors.New("로컬 드라이브가 아닙니다")
	}
	root := volume + `\`
	p, _ := syscall.UTF16PtrFromString(root)
	typ, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(p)))
	if typ != 3 {
		return errors.New("안전을 위해 고정 디스크의 파일만 휴지통으로 옮깁니다")
	}
	// SHFileOperation needs a double-NULL-terminated list.
	paths, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	paths = append(paths, 0)
	op := shFileOpStruct{operation: 3, from: &paths[0], flags: 0x0040 | 0x0010 | 0x0400 | 0x0004}
	code, _, _ := shellFileOp.Call(uintptr(unsafe.Pointer(&op)))
	if code != 0 || op.aborted != 0 {
		return fmt.Errorf("휴지통 이동 실패 (코드 %d)", code)
	}
	return nil
}

func revealInExplorer(path string) error {
	return exec.Command("explorer.exe", "/select,", path).Start()
}
func openBrowser(url string) {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}
func pickFolder() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'DeskCare에서 분석할 폴더를 선택하세요'; $d.ShowNewFolderButton = $false; if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; [Console]::Write($d.SelectedPath) }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("폴더 선택 창 실행 실패: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", errors.New("폴더 선택을 취소했습니다")
	}
	return p, nil
}

func openAppSettings() error { return exec.Command("explorer.exe", "ms-settings:appsfeatures").Start() }

// launchDeskCare starts DeskCare in its OWN Edge/Chrome application window.
// Unlike a regular tab, this window has an OS-managed lifetime: closing the
// Windows X is detected by EnumWindows even when pagehide/sendBeacon is lost.
// If no supported browser exists, the old default-browser path remains a fallback.
func launchDeskCare(url, marker string, onShown, onClosed func()) {
	if err := launchIsolatedWindow(url, marker, onShown, onClosed); err != nil {
		// Keep the page usable on unusual Windows setups (no Edge/Chrome).
		// In this fallback the pagehide beacon and short lease handle exit.
		openBrowser(url)
	}
}

func chromiumExecutable() string {
	candidates := []string{}
	for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
		)
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, name := range []string{"msedge.exe", "chrome.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func launchIsolatedWindow(url, marker string, onShown, onClosed func()) error {
	browser := chromiumExecutable()
	if browser == "" {
		return errors.New("Microsoft Edge/Chrome를 찾지 못했습니다")
	}
	profile, err := os.MkdirTemp("", "DeskCare-Browser-*")
	if err != nil {
		return err
	}
	cmd := exec.Command(browser,
		"--app="+url,
		"--user-data-dir="+profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-mode",
		"--disable-extensions",
		"--disable-sync",
	)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return err
	}
	// Wait and reap only this dedicated browser's launcher process. Never
	// terminate the user's normal Edge/Chrome windows or profile.
	browserDone := make(chan struct{})
	go func() { _ = cmd.Wait(); close(browserDone) }()
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		state := &appWindowObserver{}
		start := time.Now()
		var hwnd uintptr
		for range ticker.C {
			if hwnd == 0 {
				hwnd = findDeskCareWindow(marker)
			}
			// Once the native window is found, track its OS handle, not its
			// title. A refresh can temporarily clear the title without
			// closing the actual window.
			present := hwnd != 0 && isNativeWindow(hwnd)
			newWindow := !state.seen && present
			if state.observe(present) {
				// Let Edge shut down and release its temporary profile before
				// the Go process exits. Never terminate normal browser windows.
				removeTempBrowserProfile(profile, browserDone)
				onClosed()
				return
			}
			if newWindow {
				onShown()
			}
			// If a browser never presented its app window, the JS lease
			// still controls shutdown (the default browser fallback).
			if !state.seen && time.Since(start) > 35*time.Second {
				return
			}
		}
	}()
	return nil
}

type appWindowObserver struct {
	seen   bool
	missed int
}

// Two consecutive missed OS enumerations avoid false exit from a transient
// window-title update. After close the Go server stops within ~0.6 seconds.
func (o *appWindowObserver) observe(present bool) bool {
	if present {
		o.seen = true
		o.missed = 0
		return false
	}
	if !o.seen {
		return false
	}
	o.missed++
	return o.missed >= 2
}

var (
	windowUser32         = syscall.NewLazyDLL("user32.dll")
	enumWindows          = windowUser32.NewProc("EnumWindows")
	getWindowText        = windowUser32.NewProc("GetWindowTextW")
	getWindowTextLength  = windowUser32.NewProc("GetWindowTextLengthW")
	isWindowVisible      = windowUser32.NewProc("IsWindowVisible")
	isWindow             = windowUser32.NewProc("IsWindow")
	postWindowMessage    = windowUser32.NewProc("PostMessageW")
	enumDeskCareCallback = syscall.NewCallback(deskCareWindowCallback)
)

type windowTitleLookup struct {
	marker string
	hwnd   uintptr
}

func deskCareWindowCallback(hwnd, param uintptr) uintptr {
	lookup := (*windowTitleLookup)(unsafe.Pointer(param))
	visible, _, _ := isWindowVisible.Call(hwnd)
	if visible == 0 {
		return 1
	}
	length, _, _ := getWindowTextLength.Call(hwnd)
	if length == 0 || length > 512 {
		return 1
	}
	buf := make([]uint16, int(length)+2)
	_, _, _ = getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if strings.Contains(syscall.UTF16ToString(buf), lookup.marker) {
		lookup.hwnd = hwnd
		return 0 // Stop enumerating after finding this exact DeskCare window.
	}
	return 1
}

func findDeskCareWindow(marker string) uintptr {
	lookup := &windowTitleLookup{marker: marker}
	_, _, _ = enumWindows.Call(enumDeskCareCallback, uintptr(unsafe.Pointer(lookup)))
	runtime.KeepAlive(lookup)
	return lookup.hwnd
}

func isNativeWindow(hwnd uintptr) bool {
	ok, _, _ := isWindow.Call(hwnd)
	return ok != 0
}

func removeTempBrowserProfile(path string, browserDone <-chan struct{}) {
	// Browser may still be flushing its cache for a moment after X is clicked.
	select {
	case <-browserDone:
	case <-time.After(3 * time.Second):
		// Don't kill a running browser or delete files while it still uses them.
		return
	}
	for i := 0; i < 5; i++ {
		if err := os.RemoveAll(path); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// The explicit Exit button also closes the standalone browser app window.
func closeDeskCareWindow(marker string) {
	hwnd := findDeskCareWindow(marker)
	if hwnd != 0 {
		const wmClose = 0x0010
		_, _, _ = postWindowMessage.Call(hwnd, wmClose, 0, 0)
	}
}
