package reconciliation

import (
	"strings"
	"unicode"
)

// Candidate is one unit's payer identities: the phones and names on its
// active lease (the tenant and any lease_payers).
type Candidate struct {
	UnitID string
	Phones []string
	Names  []string
}

// Method says how a unit was identified.
type Method string

const (
	ByPhone Method = "phone"
	ByName  Method = "name"
)

// UnitMatch is the outcome of identifying a payer.
type UnitMatch struct {
	UnitID string
	Method Method
	// Ambiguous is true when more than one unit fits equally well. UnitID is
	// then empty and the payment needs a manager.
	Ambiguous bool
}

// Found reports whether exactly one unit was identified.
func (m UnitMatch) Found() bool { return m.UnitID != "" && !m.Ambiguous }

// NormalizePhone canonicalises a Kenyan mobile number to "+254XXXXXXXXX", so
// 0722 000 000, 722000000, 254722000000 and +254 722 000 000 compare equal.
// Anything that doesn't look like a Kenyan number is returned as its digits
// only, so it can still be compared.
func NormalizePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "254") && len(d) == 12:
		return "+" + d
	case strings.HasPrefix(d, "0") && len(d) == 10:
		return "+254" + d[1:]
	case len(d) == 9 && (d[0] == '7' || d[0] == '1'):
		return "+254" + d
	default:
		return d
	}
}

// MatchUnit identifies the unit a payment belongs to: the payer phone first,
// then the payer name. A result that fits several units is Ambiguous rather
// than a guess.
func MatchUnit(msisdn, payerName string, candidates []Candidate) UnitMatch {
	if m := MatchByPhone(msisdn, candidates); m.Found() || m.Ambiguous {
		return m
	}
	return MatchByName(payerName, candidates)
}

// MatchByPhone finds the unit whose lease (tenant or a co-payer) has exactly
// this MSISDN. Numbers are compared after normalisation, so 0722 000 000 and
// +254722000000 are the same. Two units sharing the number is Ambiguous.
func MatchByPhone(msisdn string, candidates []Candidate) UnitMatch {
	phone := NormalizePhone(msisdn)
	if phone == "" {
		return UnitMatch{}
	}
	units := map[string]bool{}
	for _, c := range candidates {
		for _, p := range c.Phones {
			if NormalizePhone(p) == phone {
				units[c.UnitID] = true
			}
		}
	}
	switch len(units) {
	case 0:
		return UnitMatch{}
	case 1:
		for u := range units {
			return UnitMatch{UnitID: u, Method: ByPhone}
		}
	}
	return UnitMatch{Ambiguous: true, Method: ByPhone}
}

// MatchByName finds the unit whose lease names best fit the payer name, after
// case and whitespace normalisation, tolerating extra middle names and word
// order. The best score must be unique; a tie is Ambiguous.
func MatchByName(payerName string, candidates []Candidate) UnitMatch {
	payer := tokens(payerName)
	if len(payer) == 0 {
		return UnitMatch{}
	}
	best, bestUnits := 0.0, map[string]bool{}
	for _, c := range candidates {
		for _, n := range c.Names {
			score, ok := nameScore(payer, tokens(n))
			if !ok {
				continue
			}
			switch {
			case score > best:
				best, bestUnits = score, map[string]bool{c.UnitID: true}
			case score == best:
				bestUnits[c.UnitID] = true
			}
		}
	}
	switch len(bestUnits) {
	case 0:
		return UnitMatch{}
	case 1:
		for u := range bestUnits {
			return UnitMatch{UnitID: u, Method: ByName}
		}
	}
	return UnitMatch{Ambiguous: true, Method: ByName}
}

// tokens lower-cases a name and splits it into its letter and digit runs.
func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// nameScore compares two token lists. M-Pesa names often carry an extra
// middle name ("JOHN KAMAU MWANGI" vs "John Kamau"), so the score is the
// share of the shorter name found in the longer one. It requires at least
// two shared tokens and two-thirds of the shorter name, so a common first
// name alone never matches: with money involved, no match beats a wrong one.
func nameScore(a, b []string) (float64, bool) {
	if len(a) == 0 || len(b) == 0 {
		return 0, false
	}
	set := make(map[string]bool, len(b))
	for _, t := range b {
		set[t] = true
	}
	shared := 0
	seen := map[string]bool{}
	for _, t := range a {
		if set[t] && !seen[t] {
			shared++
			seen[t] = true
		}
	}
	shorter := min(len(a), len(b))
	if shared < 2 {
		return 0, false
	}
	score := float64(shared) / float64(shorter)
	return score, score >= 2.0/3.0
}
