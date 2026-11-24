package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// This deliberately favours false positives (read-only) over deleting an application dependency.
// A supported filename extension is NOT proof that a file is expendable.
var blockedFolderNames = map[string]bool{
	"windows": true, "windows.old": true, "$windows.~bt": true, "$windows.~ws": true,
	"program files": true, "program files (x86)": true, "program files (arm)": true,
	"programdata": true, "appdata": true, "application data": true, "windowsapps": true,
	"$recycle.bin": true, "system volume information": true, "recovery": true, "perflogs": true,
	"boot": true, "efi": true, "config.msi": true, "msocache": true, "w sxs": true,
	"wpsystem": true, "msixvc": true, "intel": true, "nvidia": true,
	".git": true, ".svn": true, ".hg": true, ".idea": true, ".vscode": true,
	"node_modules": true, "__pycache__": true, ".venv": true, "venv": true,
	"virtualenv": true, "virtualenvs": true, ".tox": true, ".gradle": true,
	".m2": true, ".nuget": true, ".cargo": true, ".rustup": true,
	".config": true, ".cache": true, ".local": true, ".ssh": true,
	".aws": true, ".kube": true, ".gnupg": true,
	"bin": true, "lib": true, "libs": true, "obj": true, "build": true,
	"dist": true, "src": true, "source": true, "packages": true, "plugins": true,
	"extensions": true, "scripts": true, "resources": true, "assets": true,
	"programs": true, "applications": true, "portableapps": true, "software": true,
	"steam": true, "steamapps": true, "epic games": true, "riot games": true,
	"games": true, "mods": true, "minecraft": true,
	"projects": true, "project": true, "workspace": true, "workspaces": true,
	"repos": true, "repositories": true, "development": true,
	"backup": true, "backups": true, "restore": true, "snapshots": true,
	"백업": true, "복구": true, "프로그램": true, "소프트웨어": true, "개발": true,
}

// Project markers identify arbitrary app folders that do not have a conventional name.
var projectMarkers = []string{".git", "go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml"}

func isProjectDirectory(dir string) bool {
	for _, marker := range projectMarkers {
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func projectDirectoryTree(path string) bool {
	return projectDirectoryTreeCached(path, nil)
}

func projectDirectoryTreeCached(path string, cache map[string]bool) bool {
	path = filepath.Clean(path)
	var visited []string
	found := false
	for {
		if cache != nil {
			if v, ok := cache[path]; ok {
				found = v
				break
			}
			visited = append(visited, path)
		}
		if isProjectDirectory(path) {
			found = true
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	for _, p := range visited {
		cache[p] = found
	}
	return found
}

var blockedFileNames = map[string]bool{
	"pagefile.sys": true, "swapfile.sys": true, "hiberfil.sys": true,
	"bootmgr": true, "boot.ini": true, "ntldr": true, "ntdetect.com": true,
	"bootnxt": true, "bootstat.dat": true, "bootsect.bak": true,
	"io.sys": true, "msdos.sys": true, "desktop.ini": true, "thumbs.db": true,
	"autorun.inf": true, ".env": true, ".gitignore": true, ".gitattributes": true,
	"package.json": true, "package-lock.json": true,
	"go.mod": true, "go.sum": true, "requirements.txt": true,
	"dockerfile": true, "makefile": true,
}

// Only personal-content formats can be selected for a recycle-bin move.
// Installers, archives, configuration, scripts, database files and unknown types are read-only.
var personalContentExtensions = map[string]bool{}

func init() {
	for _, ext := range strings.Fields(`
        jpg jpeg png gif bmp webp heic heif tif tiff avif cr2 nef arw dng raw
        mp4 mov avi mkv wmv flv webm m4v mpg mpeg mts m2ts 3gp
        mp3 wav flac m4a aac ogg wma aiff ape opus mid midi
        pdf doc docx xls xlsx ppt pptx hwp hwpx rtf odt ods odp epub
    `) {
		personalContentExtensions["."+ext] = true
	}
}

func cloudFolder(name string) bool {
	name = strings.ToLower(name)
	return name == "onedrive" || strings.HasPrefix(name, "onedrive - ") ||
		name == "dropbox" || name == "google drive" || name == "google drive for desktop" ||
		name == "icloud drive" || name == "box" || name == "synologydrive" || name == "synology drive"
}

func protectedFolderName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if blockedFolderNames[name] || cloudFolder(name) {
		return true
	}
	// Hidden dot-directories and application-created directories are not candidates.
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".app") {
		return true
	}
	return false
}

func protectedDirectoryPath(path string) bool {
	path = filepath.Clean(path)
	for {
		name := filepath.Base(path)
		if name == "." || name == string(filepath.Separator) {
			break
		}
		if protectedFolderName(name) {
			return true
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return false
}

func protectedFileName(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return blockedFileNames[name] || strings.HasPrefix(name, ".") ||
		strings.HasPrefix(name, "~$") || strings.HasPrefix(name, "ntuser.dat") ||
		strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".bak") ||
		strings.HasSuffix(name, ".old") || strings.HasSuffix(name, ".backup") ||
		strings.HasSuffix(name, ".tmp")
}

func wholeVolume(path string) bool {
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(path)
		return volume != "" && strings.EqualFold(filepath.Clean(path), filepath.Clean(volume+`\`))
	}
	return filepath.Clean(path) == string(filepath.Separator)
}

func overlyBroadRoot(root string) bool {
	if wholeVolume(root) {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.EqualFold(filepath.Clean(root), filepath.Clean(home)) {
		return true
	}
	// Users and Public are too broad as explicit deletion scopes.
	name := strings.ToLower(filepath.Base(root))
	parent := strings.ToLower(filepath.Base(filepath.Dir(root)))
	return name == "users" || (name == "public" && parent == "users")
}

func safeDeletionScope(path string, roots []string) bool {
	for _, root := range roots {
		if overlyBroadRoot(root) {
			continue
		}
		if underRoot(path, root) && !strings.EqualFold(filepath.Clean(path), filepath.Clean(root)) {
			return true
		}
	}
	return false
}

// When optional auto protection is switched off, we only relax the file-type
// allowlist. Hard boundaries (system/app/project/cloud folders, hidden files,
// volume roots and Windows reparse attributes) are ALWAYS enforced.
// Switching modes NEVER grants permission to delete a protected directory.
func protectionReason(path string, roots []string) string {
	return protectionReasonWithMode(path, roots, nil, false)
}

func protectionReasonCached(path string, roots []string, cache map[string]bool) string {
	return protectionReasonWithMode(path, roots, cache, false)
}

func protectionReasonWithMode(path string, roots []string, cache map[string]bool, relaxed bool) string {
	if protectedDirectoryPath(filepath.Dir(path)) || projectDirectoryTreeCached(filepath.Dir(path), cache) {
		return "시스템·프로그램·개발·백업·클라우드 폴더"
	}
	if protectedFileName(path) {
		return "운영체제·설정·임시·백업 파일"
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !personalContentExtensions[ext] {
		if !relaxed || (ext != ".txt" && ext != ".md" && ext != ".csv") {
			return "실행·설치·압축·설정 파일 또는 미확인 형식"
		}
	}
	if !safeDeletionScope(path, roots) {
		return "넓은 분석 범위 또는 선택되지 않은 위치"
	}
	if reason := osFileProtectionReason(path, roots); reason != "" {
		return reason
	}
	return ""
}

// Windows uses the SYSTEM and HIDDEN attributes for cosmetic customization of
// ordinary user folders too (notably Downloads and Documents). Such flags on
// the chosen scan root and its ancestors are not enough to reject a scan or
// make every personal file look protected. The same flags below the chosen
// root remain dangerous, and file-level flags are always protected.
const (
	winAttrReadOnly uint32 = 0x0001
	winAttrHidden   uint32 = 0x0002
	winAttrSystem   uint32 = 0x0004
	winAttrReparse  uint32 = 0x0400
	winAttrOffline  uint32 = 0x1000
)

func windowsAttributeProtectionReason(attr uint32, isFile, isNestedDir bool) string {
	if attr&(winAttrReparse|winAttrOffline) != 0 {
		return "연결(재분석 지점)·오프라인 항목"
	}
	if isFile && attr&(winAttrReadOnly|winAttrHidden|winAttrSystem) != 0 {
		return "읽기 전용·숨김·시스템 파일"
	}
	if isNestedDir && attr&(winAttrHidden|winAttrSystem) != 0 {
		return "숨김·시스템 하위 폴더"
	}
	return ""
}
