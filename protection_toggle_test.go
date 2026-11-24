package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectionToggleVisibilityAndHardBoundaries(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "family.jpg"), "photo")
	touch(t, filepath.Join(root, "old.txt"), "text data")
	touch(t, filepath.Join(root, "app.exe"), "exe data")
	touch(t, filepath.Join(root, "Windows", "hidden.jpg"), "should not be scanned")
	touch(t, filepath.Join(root, "PortableApp", "package.json"), "{}")
	touch(t, filepath.Join(root, "PortableApp", "cover.jpg"), "no delete")

	m := &Manager{token: "test-token", byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{root})
	if len(m.files) != 3 {
		t.Fatalf("expected 3 indexed files, got %+v", m.files)
	}
	srv := httptest.NewServer(m.routes())
	defer srv.Close()
	request := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-DeskCare-Token", "test-token")
		req.Header.Set("Content-Type", "application/json")
		rsp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer rsp.Body.Close()
		out := map[string]any{}
		if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return rsp.StatusCode, out
	}
	_, all := request("GET", "/api/files?showProtected=true", "")
	if all["total"].(float64) != 3 {
		t.Fatalf("show protected: %v", all)
	}
	_, visible := request("GET", "/api/files?showProtected=false", "")
	if visible["total"].(float64) != 1 {
		t.Fatalf("hide protected: %v", visible)
	}
	_, mode := request("GET", "/api/overview", "")
	if mode["autoProtectionEnabled"] != true {
		t.Fatalf("safe mode not default ON %v", mode)
	}
	code, off := request("POST", "/api/protection", `{"enabled":false}`)
	if code != 200 || off["autoProtectionEnabled"] != false {
		t.Fatalf("switch off failed %d %v", code, off)
	}
	_, visibleOff := request("GET", "/api/files?showProtected=false", "")
	if visibleOff["total"].(float64) != 2 {
		t.Fatalf("only personal + .txt should be allowed, not .exe: %v", visibleOff)
	}
	if protectionReasonWithMode(filepath.Join(root, "app.exe"), []string{root}, nil, true) == "" {
		t.Fatal("execution files must remain blocked even in relaxed mode")
	}
	if protectionReasonWithMode(filepath.Join(root, "Windows", "icon.jpg"), []string{root}, nil, true) == "" {
		t.Fatal("hard protected Windows folder was bypassed")
	}
	if protectionReasonWithMode(filepath.Join(root, "PortableApp", "cover.jpg"), []string{root}, nil, true) == "" {
		t.Fatal("hard project-marker protection was bypassed")
	}
	code, on := request("POST", "/api/protection", `{"enabled":true}`)
	if code != 200 || on["autoProtectionEnabled"] != true {
		t.Fatalf("switch on failed %d %v", code, on)
	}
	_, visibleAgain := request("GET", "/api/files?showProtected=false", "")
	if visibleAgain["total"].(float64) != 1 {
		t.Fatalf("soft protection not restored %v", visibleAgain)
	}
}

func TestDuplicateKeeperCannotBeBypassedByModeSwitch(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "a.jpg"), "same content")
	touch(t, filepath.Join(root, "b.jpg"), "same content")
	touch(t, filepath.Join(root, "archive.zip"), "other file")
	m := &Manager{byID: map[string]int{}, selectedRoots: []string{root}, scan: JobState{State: "running"}}
	m.scanFiles(context.Background(), []string{root})
	m.findDuplicates(context.Background(), append([]FileItem(nil), m.files...))
	if len(m.dupGroups) != 1 {
		t.Fatal("fixture must contain exactly one duplicate group")
	}
	keeperID := m.dupGroups[0].Files[0].ID
	change := func(enabled bool) {
		t.Helper()
		payload := `{"enabled":false}`
		if enabled {
			payload = `{"enabled":true}`
		}
		req := httptest.NewRequest("POST", "/api/protection", strings.NewReader(payload))
		res := httptest.NewRecorder()
		m.setProtectionMode(res, req)
		if res.Code != 200 {
			t.Fatalf("mode change failed %d: %s", res.Code, res.Body.String())
		}
		if f := m.files[m.byID[keeperID]]; f.CanTrash || !f.DuplicateKeeper {
			t.Fatalf("duplicate keeper must remain protected: %+v", f)
		}
	}
	change(false)
	change(true)
	m.findDuplicates(context.Background(), append([]FileItem(nil), m.files...))
	if len(m.dupGroups) != 1 {
		t.Fatal("old keeper must still be checked for duplicates")
	}
}
