//go:build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var getFileAttributesSafe = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFileAttributesW")

func windowsFileAttributes(path string) (uint32, bool) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	attr, _, _ := getFileAttributesSafe.Call(uintptr(unsafe.Pointer(name)))
	if uint32(attr) == 0xffffffff {
		return 0, false
	}
	return uint32(attr), true
}

// A user-selected folder can be safely READ even if Windows marks it SYSTEM
// for folder customization. But never follow reparse points, junctions, or
// offline cloud placeholders, including those in its parent chain.
func osScanRootProtectionReason(root string) string {
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		attr, ok := windowsFileAttributes(p)
		if !ok {
			return "Windows 폴더 속성을 확인할 수 없습니다"
		}
		if reason := windowsAttributeProtectionReason(attr, false, false); reason != "" {
			return reason
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
	}
	return ""
}

// Never descend into hidden/system children of a selected folder.
// Attributes on the selected root itself are handled by osScanRootProtectionReason.
func osSkipScanDirectory(dir string) bool {
	attr, ok := windowsFileAttributes(dir)
	return !ok || windowsAttributeProtectionReason(attr, false, true) != ""
}

// Deletion guard: a selected root (or its ancestors) can have cosmetic SYSTEM
// attributes, but hidden/system folders beneath it remain protected.
// All file-level special attributes are always protected. Reparse/offline
// entries remain blocked at every path component.
func osFileProtectionReason(path string, roots []string) string {
	path = filepath.Clean(path)
	for p := path; ; p = filepath.Dir(p) {
		attr, ok := windowsFileAttributes(p)
		if !ok {
			return "Windows 파일 속성을 확인할 수 없습니다"
		}
		nested := p != path
		if nested {
			for _, root := range roots {
				// p is an ancestor of or identical to the selected root.
				if underRoot(root, p) {
					nested = false
					break
				}
			}
		}
		if reason := windowsAttributeProtectionReason(attr, p == path, nested); reason != "" {
			return reason
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
	}
	return ""
}
