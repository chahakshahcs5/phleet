package httpapi

import "testing"

func TestFSReadMaxClampsInlineReads(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", maxEditBytes},
		{"1024", 1024},
		{"999999999", maxEditBytes},
		{"nope", maxEditBytes},
		{"-1", maxEditBytes},
	}
	for _, tc := range cases {
		if got := fsReadMax(tc.in); got != tc.want {
			t.Fatalf("fsReadMax(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
