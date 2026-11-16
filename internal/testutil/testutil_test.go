package testutil

import "testing"

func TestLineDifference(t *testing.T) {
	for _, tc := range []struct{ want, got, diff string }{
		{"one\r\ntwo\r\n", "one\ntwo\n", ""},
		{"one\ntwo\n", "one\r\ntwo\r\n", ""},
		{"one\ntwo\n", "one\ntwo \n", `line 2: want "two", got "two "`},
		{"one\n", "one", `line 2: want "", got <EOF>`},
		{"one", "one\n", `line 2: want <EOF>, got ""`},
	} {
		if got := lineDifference(tc.want, tc.got); got != tc.diff {
			t.Fatalf("difference = %q, want %q", got, tc.diff)
		}
	}
}
