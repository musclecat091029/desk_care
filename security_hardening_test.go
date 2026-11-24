package main

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoopbackHostAndOriginGuard(t *testing.T) {
	m := &Manager{token: "launch-token", allowedHost: "127.0.0.1:48721", byID: map[string]int{}, categories: aggregate(nil)}
	handler := m.routes()
	for _, tc := range []struct {
		name, host, origin, site string
		want                     int
	}{
		{"valid direct", "127.0.0.1:48721", "", "none", http.StatusOK},
		{"valid local origin", "127.0.0.1:48721", "http://127.0.0.1:48721", "same-origin", http.StatusOK},
		{"DNS rebinding", "attacker.test:48721", "", "", http.StatusForbidden},
		{"foreign origin", "127.0.0.1:48721", "https://attacker.test", "cross-site", http.StatusForbidden},
		{"other localhost port", "127.0.0.1:48721", "http://127.0.0.1:48722", "same-site", http.StatusForbidden},
		{"cross site navigation", "127.0.0.1:48721", "", "cross-site", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:48721/", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status %d, expected %d", rr.Code, tc.want)
			}
		})
	}
}

func TestRejectOversizedOrExtraJSON(t *testing.T) {
	for _, tc := range []string{`{"id":"x"}{"id":"y"}`, `{"id":"x"} trailing`, strings.Repeat(" ", 128*1024+1)} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc))
		var body struct {
			ID string `json:"id"`
		}
		if err := readBody(req, &body); err == nil {
			t.Fatalf("accepted invalid JSON %d bytes", len(tc))
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"only"}  `))
	var body struct {
		ID string `json:"id"`
	}
	if err := readBody(req, &body); err != nil || body.ID != "only" {
		t.Fatalf("valid JSON rejected: %v %+v", err, body)
	}
}

func TestSpreadsheetFormulaCellsAreEscaped(t *testing.T) {
	for _, bad := range []string{"=HYPERLINK(\"http://bad\")", "+CMD(...) ", " @SUM(A1:A2)", "-1+2", "\t=2+2"} {
		if !strings.HasPrefix(safeCSVCell(bad), "'") {
			t.Fatalf("formula cell not escaped: %q", bad)
		}
	}
	if safeCSVCell("C:\\Users\\Test") != "C:\\Users\\Test" {
		t.Fatal("ordinary Windows path altered")
	}
	m := &Manager{files: []FileItem{{Name: "=1+1.csv", Path: "C:\\Users\\test\\=1+1.csv", Category: "documents", Size: 3}}}
	rr := httptest.NewRecorder()
	m.exportCSV(rr, httptest.NewRequest("GET", "/api/export", nil))
	got, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\uFEFF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1][0] != "'=1+1.csv" {
		t.Fatalf("export not protected: %v", got)
	}
}

func TestRelaxedModeDoesNotEnableDangerousDeletes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"app.exe", "plugin.dll", "archive.zip", "settings.json", "script.ps1", "recovery.db", "project.py", "launch.lnk", "installer.msi"} {
		if got := protectionReasonWithMode(filepath.Join(root, name), []string{root}, nil, true); got == "" {
			t.Errorf("dangerous type allowed: %s", name)
		}
	}
	for _, name := range []string{"notes.txt", "plan.md", "numbers.csv"} {
		// Real files are required for the Windows attributes and delete guard.
		if err := os.WriteFile(filepath.Join(root, name), []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := protectionReasonWithMode(filepath.Join(root, name), []string{root}, nil, true); got != "" {
			t.Errorf("text content blocked: %s: %s", name, got)
		}
	}
	for _, name := range []string{"notes.txt", "plan.md", "numbers.csv"} {
		if got := protectionReasonWithMode(filepath.Join(root, name), []string{root}, nil, false); got == "" {
			t.Errorf("text allowed in strict mode: %s", name)
		}
	}
}

func TestBlockedWideUserRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if _, err := canScanRoot(home); err == nil {
		t.Fatal("home root scan should be blocked")
	}
}

func TestNoUntrustedAPIRequestCanDelete(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(target, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{token: "launch-token", allowedHost: "127.0.0.1:48721", byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{root})
	if len(m.files) != 1 {
		t.Fatal("fixture missing")
	}
	for _, tc := range []struct{ host, origin string }{{"bad-domain.test:48721", ""}, {"127.0.0.1:48721", "https://bad-domain.test"}} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:48721/api/trash", strings.NewReader(`{"ids":["1"]}`))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("X-DeskCare-Token", "launch-token")
		rr := httptest.NewRecorder()
		m.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("unsafe request accepted: %d", rr.Code)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file removed by foreign request: %v", err)
	}
}
