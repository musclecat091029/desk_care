//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

func listDrives() []DriveInfo  { return []DriveInfo{} }
func installedApps() []AppInfo { return []AppInfo{} }
func moveToRecycleBin(path string) error {
	return errors.New("휴지통 이동은 Windows 버전에서만 지원합니다")
}
func revealInExplorer(path string) error { return exec.Command("xdg-open", path).Start() }
func openBrowser(url string)             { _ = exec.Command("xdg-open", url).Start() }
func pickFolder() (string, error) {
	return "", errors.New("폴더 선택은 Windows 버전에서만 지원합니다")
}
func revealInstallFolder(path string) error {
	return errors.New("Windows에서만 설치 위치를 열 수 있습니다")
}
func openAppSettings() error { return errors.New("Windows 전용 기능입니다") }

// Non-Windows testing uses the existing browser-based lifecycle.
func launchDeskCare(url, marker string, onShown, onClosed func()) {
	openBrowser(url)
}

func closeDeskCareWindow(marker string) {}
