package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSmartCleanupSharedWithAllFiles(t *testing.T) {
	now := time.Now()
	old := now.AddDate(0, 0, -110).Unix()
	recent := now.AddDate(0, 0, -3).Unix()
	files := []FileItem{
		{ID: "1", Name: "old-video.mp4", Path: "/Pictures/old-video.mp4", Category: "videos", Size: 300 * 1024 * 1024, Modified: old, CanTrash: true},
		{ID: "2", Name: "old-pdf.pdf", Path: "/Documents/old-pdf.pdf", Category: "documents", Size: 1234, Modified: old, CanTrash: true},
		{ID: "3", Name: "new.jpg", Path: "/Downloads/new.jpg", InDownloads: true, Category: "photos", Size: 1, Modified: recent, CanTrash: true},
		{ID: "4", Name: "old-installer.exe", Path: "/Downloads/old-installer.exe", InDownloads: true, Category: "installers", Size: 1234, Modified: old, CanTrash: false},
	}
	m := &Manager{files: files, byID: map[string]int{}}
	for _, tc := range []struct {
		view          string
		count         int
		showProtected bool
	}{
		{"all", 4, true}, {"large", 1, true}, {"old", 3, true}, {"old_downloads", 1, true}, {"installers", 1, true}, {"installers", 0, false},
	} {
		r := httptest.NewRequest("GET", "/api/files?view="+tc.view+"&showProtected="+map[bool]string{true: "true", false: "false"}[tc.showProtected], nil)
		w := httptest.NewRecorder()
		m.getFiles(w, r)
		var got struct {
			Total int        `json:"total"`
			Files []FileItem `json:"files"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Total != tc.count {
			t.Fatalf("view %s protected %v: want %d, got %+v", tc.view, tc.showProtected, tc.count, got)
		}
	}
	// No reliance on app-installation list when selecting smart-cleanup views.
	if m.apps != nil {
		t.Fatal("fixture unexpectedly contains installed apps")
	}
	if !matchesSmartView(files[1], "old", now) {
		t.Fatal("old document outside Downloads was missed")
	}
	if matchesSmartView(files[1], "old_downloads", now) {
		t.Fatal("old downloads must not include Documents")
	}
}

func TestInstalledAppsPathSearchAndNoArbitraryFolderOpen(t *testing.T) {
	m := &Manager{token: "test-token", apps: []AppInfo{
		{ID: "a", Name: "Editor", Publisher: "Example", InstallPath: `C:\\Program Files\\Example`, PathKnown: true, PathExists: true, Size: 1024, SizeKnown: true},
		{ID: "b", Name: "Store App", PathKnown: false},
	}}
	req := httptest.NewRequest("GET", "/api/apps?q=Example&sort=name", nil)
	w := httptest.NewRecorder()
	m.getApps(w, req)
	var result struct {
		Apps []AppInfo `json:"apps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Apps) != 1 || result.Apps[0].ID != "a" || !strings.Contains(result.Apps[0].InstallPath, "Example") {
		t.Fatalf("unexpected apps %+v", result.Apps)
	}
	for _, input := range []string{`{"id":"unknown"}`, `{"path":"C:\\\\Windows"}`, `{"id":"b"}`} {
		r := httptest.NewRequest("POST", "/api/apps/open", strings.NewReader(input))
		w := httptest.NewRecorder()
		m.openAppFolder(w, r)
		if w.Code < 400 {
			t.Fatalf("unsafe open request accepted %s: %d", input, w.Code)
		}
	}
}
