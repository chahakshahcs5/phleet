//go:build !windows

package httpapi

import "os/exec"

func hideProcessWindow(_ *exec.Cmd) {}
