//go:build !windows

package main

func osFileProtectionReason(path string, roots []string) string { return "" }
func osScanRootProtectionReason(path string) string             { return "" }
func osSkipScanDirectory(path string) bool                      { return false }
