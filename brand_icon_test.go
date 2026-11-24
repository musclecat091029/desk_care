package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeskCare18BrandAssets(t *testing.T) {
	mgr := newManager()
	handler := mgr.routes()
	t.Run("sidebar", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 200 {
			t.Fatalf("index status: %d", w.Code)
		}
		b := w.Body.String()
		if !strings.Contains(b, `class="brand-symbol" src="/ui/deskcare-icon.png"`) || !strings.Contains(b, "DeskCare SAFE 1.9") {
			t.Fatal("1.9 cat logo or version missing")
		}
	})
	t.Run("png", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/ui/deskcare-icon.png", nil))
		if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatal("icon not served as PNG")
		}
	})
	t.Run("favicon", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/ui/favicon.ico", nil))
		if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte{0, 0, 1, 0}) {
			t.Fatal("favicon missing")
		}
	})
}
