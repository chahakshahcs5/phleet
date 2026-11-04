package httpapi

import "testing"

func TestValidRclonePath(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"", true},
		{"rclone", true},
		{"rclone.exe", true},
		{`C:\Program Files\rclone\rclone.exe`, true},
		{"/usr/local/bin/rclone", true},
		{`C:\Windows\System32\calc.exe`, false},
		{"cmd.exe", false},
		{"rclone\n--config=/tmp/evil", false},
	} {
		if got := validRclonePath(tc.path); got != tc.ok {
			t.Fatalf("validRclonePath(%q) = %v, want %v", tc.path, got, tc.ok)
		}
	}
}
