package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeTrashRequest(t *testing.T, m *Manager, ids ...string) (int, map[string]any) {
	t.Helper()
	requestData, _ := json.Marshal(map[string]any{"ids": ids})
	req := httptest.NewRequest(http.MethodPost, "/api/trash", bytes.NewReader(requestData))
	rec := httptest.NewRecorder()
	m.trash(rec, req)
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode trash response: %v / %s", err, rec.Body.String())
	}
	return rec.Code, result
}

func requireArray(t *testing.T, response map[string]any, key string, expectedLen int) {
	t.Helper()
	values, ok := response[key].([]any)
	if !ok {
		t.Fatalf("%s must be [] even when empty, got %T (%#v)", key, response[key], response[key])
	}
	if len(values) != expectedLen {
		t.Fatalf("%s length got %d want %d", key, len(values), expectedLen)
	}
}

func TestTrashResponseFailureAlwaysHasArrays(t *testing.T) {
	root := t.TempDir()
	m := &Manager{selectedRoots: []string{root}, byID: map[string]int{}, scan: JobState{State: "done"}}
	status, body := makeTrashRequest(t, m, "not-a-scanned-file")
	if status != 200 {
		t.Fatalf("HTTP %d", status)
	}
	requireArray(t, body, "removed", 0)
	requireArray(t, body, "failed", 1)
}

func TestTrashResponseSuccessAlwaysHasArrays(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.jpg")
	if err := os.WriteFile(path, []byte("test photo"), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if reason := protectionReason(path, []string{root}); reason != "" {
		t.Fatalf("fixture must be allowed: %s", reason)
	}
	f := FileItem{ID: "one", Name: "sample.jpg", Path: path, Size: stat.Size(), Modified: stat.ModTime().Unix(), ModifiedNanos: stat.ModTime().UnixNano(), CanTrash: true, Category: "photos"}
	moved := 0
	m := &Manager{files: []FileItem{f}, byID: map[string]int{"one": 0}, selectedRoots: []string{root}, scan: JobState{State: "done"}, recycleFile: func(path string) error { moved++; return os.Remove(path) }}
	status, body := makeTrashRequest(t, m, "one")
	if status != 200 {
		t.Fatalf("HTTP %d", status)
	}
	requireArray(t, body, "removed", 1)
	requireArray(t, body, "failed", 0)
	if moved != 1 || len(m.files) != 0 {
		t.Fatalf("expected exactly one recycle operation, got %d; indexed files=%d", moved, len(m.files))
	}
}

func TestTrashResponsePartialFailureAlwaysHasArrays(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.jpg")
	if err := os.WriteFile(path, []byte("test photo"), 0600); err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Lstat(path)
	f := FileItem{ID: "ok", Name: "sample.jpg", Path: path, Size: stat.Size(), ModifiedNanos: stat.ModTime().UnixNano(), CanTrash: true, Category: "photos"}
	m := &Manager{files: []FileItem{f}, byID: map[string]int{"ok": 0}, selectedRoots: []string{root}, scan: JobState{State: "done"}, recycleFile: func(path string) error { return os.Remove(path) }}
	status, body := makeTrashRequest(t, m, "ok", "missing")
	if status != 200 {
		t.Fatalf("HTTP %d", status)
	}
	requireArray(t, body, "removed", 1)
	requireArray(t, body, "failed", 1)
}
