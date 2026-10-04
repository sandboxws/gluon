package inspect

import "testing"

func TestCommas(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1", "1"}, {"123", "123"}, {"1234", "1,234"},
		{"39176797", "39,176,797"}, {"1000000", "1,000,000"},
	} {
		if got := commas(tc.in); got != tc.want {
			t.Errorf("commas(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
