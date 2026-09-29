package data

import "testing"

func TestPrefixTSQuery(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"Jane", "jane:*"},
		{"jane  WANJ", "jane:* & wanj:*"},
		{"Wanjikũ", "wanjikũ:*"},
		{"SHOP NO.1", "shop:* & no:* & 1:*"},
		// tsquery operators and quotes are dropped, never passed through.
		{"jane & !peter | (x) <-> 'y':*", "jane:* & peter:* & x:* & y:*"},
		{"!!!", ""},
		// At most maxSearchTerms words.
		{"a b c d e f g h i j", "a:* & b:* & c:* & d:* & e:* & f:* & g:* & h:*"},
	}

	for _, tt := range tests {
		if got := prefixTSQuery(tt.in); got != tt.want {
			t.Errorf("prefixTSQuery(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
