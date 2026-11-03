//go:build !windows

package config

// File modes are enforced by the OS on Unix-like platforms.
func hardenDataDir(string) {}
