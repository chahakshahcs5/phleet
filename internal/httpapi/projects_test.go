package httpapi

import "testing"

func TestNormalizeDeployRef(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"branch", "main", "main", false},
		{"tag", "v1.2.3", "v1.2.3", false},
		{"branch with slash", "release/2026.11", "release/2026.11", false},
		{"full sha", "0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567", false},
		{"short sha", "0123456", "", true},
		{"short hex branch name", "deadbeef", "", true},
		{"parent escape", "../main", "", true},
		{"trailing slash", "main/", "", true},
		{"lock file", "main.lock", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeDeployRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeDeployRef(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeDeployRef(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeDeployRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
