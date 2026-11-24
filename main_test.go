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
)

func TestClassification(t *testing.T) {
	cases := map[string]string{"가족사진.HEIC": "photos", "clip.MP4": "videos", "보고서.hwp": "documents", "song.flac": "audio", "data.7z": "archives", "setup.MSI": "installers", "file.dat": "others"}
	for input, want := range cases {
		if got := categoryOf(input); got != want {
			t.Errorf("%s got %s want %s", input, got, want)
		}
	}
}

func TestScanAndDuplicateContent(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"photo1.jpg": "one image", "photo2.jpg": "one image", "different.jpg": "DIFFERENT",
		"report.pdf": "hello", "video.mp4": "videodata", "archive.zip": "zipdata",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{root})
	if m.scan.State != "done" || len(m.files) != 6 {
		t.Fatalf("scan: state %s files %d", m.scan.State, len(m.files))
	}
	m.findDuplicates(context.Background(), append([]FileItem(nil), m.files...))
	if m.dup.State != "done" || len(m.dupGroups) != 1 {
		t.Fatalf("dup state %s, groups %d", m.dup.State, len(m.dupGroups))
	}
	if m.dupGroups[0].ExtraBytes != int64(len("one image")) {
		t.Errorf("bad savings %v", m.dupGroups[0].ExtraBytes)
	}
	counts := aggregate(m.files)
	total := 0
	for _, c := range counts {
		total += c.Count
	}
	if total != 6 {
		t.Fatal(total)
	}
}

func TestPathSafety(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Dir(dir)
	if !underRoot(filepath.Join(dir, "a.txt"), dir) {
		t.Error("should be inside")
	}
	if underRoot(filepath.Join(parent, "secret"), dir) {
		t.Error("should be outside")
	}
	if _, err := canScanRoot(filepath.Join(dir, "missing")); err == nil {
		t.Error("invalid dir should fail")
	}
	if _, err := uniqueRoots([]string{dir, dir}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizationAndAPIScan(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("a"), 0600)
	m := &Manager{token: "test-token", byID: make(map[string]int), scan: JobState{State: "idle"}, dup: JobState{State: "idle"}, categories: aggregate(nil)}
	srv := httptest.NewServer(m.routes())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/overview")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("unauthorized status %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/scan", strings.NewReader(`{"paths":["`+strings.ReplaceAll(root, `\`, `\\`)+`"]}`))
	req.Header.Set("X-DeskCare-Token", "test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 202 {
		t.Fatalf("scan status %d", resp.StatusCode)
	}
	resp.Body.Close()
	// Wait for short fixture to scan, then validate API summary.
	for i := 0; i < 300; i++ {
		m.mu.RLock()
		done := m.scan.State == "done"
		m.mu.RUnlock()
		if done {
			break
		}
	}
	m.mu.RLock()
	state := m.scan.State
	m.mu.RUnlock()
	if state == "idle" {
		t.Errorf("scan did not start")
	}
	req, _ = http.NewRequest("GET", srv.URL+"/api/files?limit=10", nil)
	req.Header.Set("X-DeskCare-Token", "test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
}
