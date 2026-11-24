//go:build windows

package main

import "testing"

func TestOSWindowCloseStopsBackend(t *testing.T) {
	var o appWindowObserver
	if o.observe(false) {
		t.Fatal("not shown yet")
	}
	if o.observe(true) {
		t.Fatal("open window must stay running")
	}
	if o.observe(false) {
		t.Fatal("one missed enumeration must not close app")
	}
	if o.observe(true) {
		t.Fatal("visible again, must reset")
	}
	if o.observe(false) {
		t.Fatal("one miss must not close")
	}
	if !o.observe(false) {
		t.Fatal("two misses must close backend")
	}
}
