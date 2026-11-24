package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func touch(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyRules(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "MyPhotos")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		canTrash   bool
	}{
		{"user photo", filepath.Join(root, "family.jpg"), true},
		{"user pdf", filepath.Join(root, "report.pdf"), true},
		{"executable", filepath.Join(root, "app.exe"), false},
		{"installer", filepath.Join(root, "setup.msi"), false},
		{"configuration", filepath.Join(root, "config.json"), false},
		{"database", filepath.Join(root, "save.db"), false},
		{"backup archive", filepath.Join(root, "copy.zip"), false},
		{"unknown", filepath.Join(root, "unknown.blob"), false},
		{"hidden", filepath.Join(root, ".hidden.jpg"), false},
		{"temporary", filepath.Join(root, "~$draft.docx"), false},
		{"windows folder", filepath.Join(root, "Windows", "logo.jpg"), false},
		{"node dependencies", filepath.Join(root, "node_modules", "logo.jpg"), false},
		{"app data", filepath.Join(root, "AppData", "photo.jpg"), false},
		{"source assets", filepath.Join(root, "assets", "logo.png"), false},
		{"backup directory", filepath.Join(root, "Backups", "photo.jpg"), false},
		{"cloud", filepath.Join(root, "OneDrive", "photo.jpg"), false},
		{"git", filepath.Join(root, ".git", "photo.jpg"), false},
		{"out of root", filepath.Join(dir, "other.jpg"), false},
	}
	for _, tc := range cases {
		// Windows correctly rejects missing files when checking their attributes.
		// Create only the expected deletable fixtures; protected paths can be absent.
		if tc.canTrash {
			if err := os.WriteFile(tc.path, []byte("sample"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		reason := protectionReason(tc.path, []string{root})
		if (reason == "") != tc.canTrash {
			t.Errorf("%s: reason %q canTrash %v", tc.name, reason, tc.canTrash)
		}
	}
	// The root of a whole volume must never grant deletion permission.
	if protectionReason(filepath.Join(root, "a.jpg"), []string{string(filepath.Separator)}) == "" {
		t.Fatal("root volume must not allow deletion")
	}
}

func TestProjectMarkerSafety(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "CompletelyUnknownName")
	touch(t, filepath.Join(app, "package.json"), `{"private":true}`)
	touch(t, filepath.Join(app, "images", "icon.jpg"), "image")
	touch(t, filepath.Join(root, "family.jpg"), "family")
	if reason := protectionReason(filepath.Join(app, "images", "icon.jpg"), []string{root}); reason == "" {
		t.Fatal("app resource must be protected")
	}
	if _, err := canScanRoot(app); err == nil {
		t.Fatal("project root must be blocked from scan")
	}
	m := &Manager{byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{root})
	if len(m.files) != 1 || m.files[0].Name != "family.jpg" {
		t.Fatalf("project must be excluded: %+v", m.files)
	}
}

func TestProtectedItemsNeverTrashedViaAPI(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "program.exe"), "MZ not executable")
	touch(t, filepath.Join(root, "notes.json"), "{}")
	touch(t, filepath.Join(root, "photo.jpg"), "photo")
	m := &Manager{token: "token", byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}, dup: JobState{State: "idle"}}
	m.scanFiles(context.Background(), []string{root})
	var protectedIDs []string
	for _, f := range m.files {
		if !f.CanTrash {
			protectedIDs = append(protectedIDs, f.ID)
		}
	}
	if len(protectedIDs) != 2 {
		t.Fatalf("expected two protected items: %+v", m.files)
	}
	srv := httptest.NewServer(m.routes())
	defer srv.Close()
	for _, id := range protectedIDs {
		req, _ := http.NewRequest("POST", srv.URL+"/api/trash", strings.NewReader(`{"ids":["`+id+`"]}`))
		req.Header.Set("X-DeskCare-Token", "token")
		req.Header.Set("Content-Type", "application/json")
		rsp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Removed []string `json:"removed"`
			Failed  []string `json:"failed"`
		}
		if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		rsp.Body.Close()
		if len(out.Removed) != 0 || len(out.Failed) != 1 || !strings.Contains(out.Failed[0], "보호된") {
			t.Fatalf("unexpected response: %+v", out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "program.exe")); err != nil {
		t.Fatal("protected program removed", err)
	}
}

func TestCleanupFiltersExcludeProtectedAndKeepsDuplicateCopy(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "Downloads")
	touch(t, filepath.Join(downloads, "a.jpg"), "same content")
	touch(t, filepath.Join(downloads, "b.jpg"), "same content")
	touch(t, filepath.Join(downloads, "install.exe"), "large but protected")
	touch(t, filepath.Join(downloads, "config.json"), "{}")
	old := time.Now().AddDate(0, 0, -100)
	for _, name := range []string{"a.jpg", "b.jpg", "install.exe", "config.json"} {
		path := filepath.Join(downloads, name)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{root})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/files?view=old&limit=200", nil)
	m.getFiles(recorder, req)
	var out struct {
		Total int        `json:"total"`
		Files []FileItem `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 {
		t.Fatalf("old cleanup should contain only personal photos, got %+v", out)
	}
	m.findDuplicates(context.Background(), append([]FileItem(nil), m.files...))
	if len(m.dupGroups) != 1 {
		t.Fatalf("duplicate groups %d", len(m.dupGroups))
	}
	g := m.dupGroups[0]
	if g.Files[0].CanTrash || !g.Files[1].CanTrash {
		t.Fatalf("keeper protection failed: %+v", g.Files)
	}
	keeperID := g.Files[0].ID
	idx := m.byID[keeperID]
	if m.files[idx].CanTrash {
		t.Fatal("protected keeper must remain non-selectable in all views")
	}
	// This state is intentionally retained until re-scan so the second delete batch
	// can never accidentally destroy the only surviving duplicate copy.
}

// Regression: Windows may mark the Downloads/Documents folder with SYSTEM as
// part of Explorer folder customization. That must not make scans impossible.
func TestWindowsFolderAttributesDontBlockUserRoot(t *testing.T) {
	for _, tc := range []struct {
		label                        string
		attr                         uint32
		isFile, isNestedDir, blocked bool
	}{
		{"Downloads root SYSTEM", winAttrSystem, false, false, false},
		{"Documents root HIDDEN", winAttrHidden, false, false, false},
		{"parent directory SYSTEM", winAttrSystem, false, false, false},
		{"ordinary photo", 0, true, false, false},
		{"hidden photo", winAttrHidden, true, false, true},
		{"system photo", winAttrSystem, true, false, true},
		{"read-only document", winAttrReadOnly, true, false, true},
		{"hidden subdir", winAttrHidden, false, true, true},
		{"system subdir", winAttrSystem, false, true, true},
		{"linked subdir", winAttrReparse, false, false, true},
		{"cloud placeholder", winAttrOffline, false, false, true},
	} {
		got := windowsAttributeProtectionReason(tc.attr, tc.isFile, tc.isNestedDir)
		if (got != "") != tc.blocked {
			t.Errorf("%s: reason=%q blocked expected %v", tc.label, got, tc.blocked)
		}
	}
}

func TestDownloadsRootStillAllowedWhileProgramFoldersBlocked(t *testing.T) {
	home := t.TempDir()
	downloads := filepath.Join(home, "Downloads")
	touch(t, filepath.Join(downloads, "family.jpg"), "image")
	if _, err := canScanRoot(downloads); err != nil {
		t.Fatalf("Downloads must be scannable: %v", err)
	}
	m := &Manager{byID: map[string]int{}, selectedRoots: []string{downloads}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{downloads})
	if m.scan.State != "done" || len(m.files) != 1 || !m.files[0].CanTrash {
		t.Fatalf("downloads scan: %+v", m.files)
	}
	protected := filepath.Join(home, "Windows")
	touch(t, filepath.Join(protected, "system.jpg"), "x")
	if _, err := canScanRoot(protected); err == nil {
		t.Fatal("must not allow system folder root")
	}
}
