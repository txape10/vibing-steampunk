package adt

import "testing"

func TestResolveRowLimit(t *testing.T) {
	cases := []struct {
		name      string
		unlimited bool
		requested int
		want      int
	}{
		{"default", false, 0, 100},
		{"negative falls back to default", false, -1, 100},
		{"explicit", false, 5000, 5000},
		{"all rows wins over a number", true, 7, UnlimitedRows},
		{"all rows without a number", true, 0, UnlimitedRows},
	}
	for _, tc := range cases {
		if got := ResolveRowLimit(tc.unlimited, tc.requested); got != tc.want {
			t.Errorf("%s: ResolveRowLimit(%v, %d) = %d, want %d", tc.name, tc.unlimited, tc.requested, got, tc.want)
		}
	}
}
