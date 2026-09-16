// Kenyan phone-number validation (spec §3.1, §6.2). Numbers are stored in
// canonical E.164 form without the leading "+": "254" followed by 9 digits,
// the first of which is 7 (mobile) or 1 (Airtel mobile) — e.g. "254712345678".
package msisdn

// Valid reports whether s is a valid Kenyan MSISDN in canonical form.
func Valid(s string) bool {
	if len(s) != 12 {
		return false
	}
	if s[0] != '2' || s[1] != '5' || s[2] != '4' {
		return false
	}
	if s[3] != '7' && s[3] != '1' {
		return false
	}
	for i := 4; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
