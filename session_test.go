package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionClosingAndMultipleTabs(t *testing.T) {
	now := time.Now()
	m := &Manager{}
	started := now.Add(-time.Minute)
	if m.shouldExit(now, now) {
		t.Fatal("must not exit before a browser has connected")
	}
	m.markSession("tab11111111111111111", now)
	m.markSession("tab22222222222222222", now)
	m.forgetSession("tab11111111111111111", now.Add(time.Second))
	if m.shouldExit(now.Add(10*time.Second), started) {
		t.Fatal("closing one of two tabs stopped the app")
	}
	m.forgetSession("tab22222222222222222", now.Add(11*time.Second))
	if m.shouldExit(now.Add(12*time.Second), started) {
		t.Fatal("must allow short refresh/close grace")
	}
	if !m.shouldExit(now.Add(14*time.Second), started) {
		t.Fatal("last tab gone should allow clean exit")
	}
}

func TestRefreshReopensSessionBeforeExit(t *testing.T) {
	now := time.Now()
	m := &Manager{}
	m.markSession("tab11111111111111111", now)
	m.forgetSession("tab11111111111111111", now.Add(time.Second))
	m.markSession("tab33333333333333333", now.Add(1500*time.Millisecond))
	if m.shouldExit(now.Add(4*time.Second), now) {
		t.Fatal("browser refresh incorrectly exits")
	}
}

func TestSessionCrashAndStartupTimeout(t *testing.T) {
	now := time.Now()
	m := &Manager{}
	if !m.shouldExit(now.Add(startupGrace+time.Second), now) {
		t.Fatal("unopened app should exit on startup timeout")
	}
	m.markSession("tab11111111111111111", now)
	if m.shouldExit(now.Add(sessionExpiry-time.Second), now) {
		t.Fatal("live session expired too early")
	}
	if m.shouldExit(now.Add(sessionExpiry+time.Second), now) {
		t.Fatal("allow grace once a crashed session has expired")
	}
	if !m.shouldExit(now.Add(sessionExpiry+closeGrace+2*time.Second), now) {
		t.Fatal("browser crash should eventually release the executable")
	}
}

func TestSessionAPIAuthentication(t *testing.T) {
	m := &Manager{token: strings.Repeat("f", 64)}
	srv := httptest.NewServer(m.routes())
	defer srv.Close()
	id := "tab11111111111111111"
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/session/open", strings.NewReader(`{"id":"`+id+`"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing token allowed: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/session/open", strings.NewReader(`{"id":"`+id+`"}`))
	req.Header.Set("X-DeskCare-Token", m.token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session open failed: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/session/beacon", strings.NewReader(`{"id":"`+id+`","token":"WRONG"}`))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong beacon token allowed: %d", resp.StatusCode)
	}
	m.sessionMu.Lock()
	if len(m.sessions) != 1 {
		t.Fatal("unauthorized beacon removed session")
	}
	m.sessionMu.Unlock()
	data, _ := json.Marshal(map[string]string{"id": id, "token": m.token})
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/session/beacon", strings.NewReader(string(data)))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid beacon failed: %d", resp.StatusCode)
	}
	if !m.shouldExit(time.Now().Add(closeGrace+time.Second), time.Now()) {
		t.Fatal("beacon did not disconnect session")
	}
}

func TestNativeWindowKeepsAppAliveDespiteThrottledJavaScript(t *testing.T) {
	now := time.Now()
	m := &Manager{}
	m.markSession("tab11111111111111111", now)
	m.windowSeen.Store(true)
	if m.shouldExit(now.Add(time.Hour), now) {
		t.Fatal("OS-confirmed visible app window must not auto-close due to throttled JS timers")
	}
}
