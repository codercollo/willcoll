package moneyfmt

import (
	"testing"
	"time"
)

// pin reassigns NowFunc to return fixed for the duration of the test,
// restoring the real clock afterward.
func pin(t *testing.T, fixed time.Time) {
	t.Helper()
	NowFunc = func() time.Time { return fixed }
	t.Cleanup(func() { NowFunc = time.Now })
}

// TestNowTodayIgnoreServerTimezone pins the clock to two of the hours this
// bug class actually breaks at — just after midnight and just before it, in
// Nairobi — and checks Today()/Now() land on the Nairobi calendar day
// regardless of what Location the instant was expressed in when NowFunc
// produced it (mirroring a server whose local time, or whose time.Now(), is
// UTC rather than Nairobi). The real regression test is running this file
// twice from the shell under TZ=UTC and TZ=Africa/Nairobi: Now()/Today()
// must return the identical calendar day either way, because both explicitly
// convert to Nairobi rather than trusting time.Local.
func TestNowTodayIgnoreServerTimezone(t *testing.T) {
	// 00:30 EAT on 2026-10-01, expressed as the equivalent UTC instant
	// (2026-09-30 21:30 UTC) — exactly what a server running in UTC would
	// hand back from a bare time.Now() at that real-world moment.
	fixed := time.Date(2026, 9, 30, 21, 30, 0, 0, time.UTC)
	pin(t, fixed)

	today := Today()
	if y, m, d := today.Date(); y != 2026 || m != time.October || d != 1 {
		t.Fatalf("Today() at 00:30 EAT on the 1st = %v, want 2026-10-01", today)
	}
	if today.Location() != Nairobi {
		t.Fatalf("Today() location = %v, want Nairobi", today.Location())
	}
	if now := Now(); now.Location() != Nairobi {
		t.Fatalf("Now() location = %v, want Nairobi", now.Location())
	}

	// 23:30 EAT on 2026-09-30 (the last day of the month), as the
	// equivalent UTC instant (2026-09-30 20:30 UTC) — a server in UTC at
	// this real-world moment still thinks it's the 30th at 20:30, not yet
	// into October.
	fixed = time.Date(2026, 9, 30, 20, 30, 0, 0, time.UTC)
	pin(t, fixed)

	today = Today()
	if y, m, d := today.Date(); y != 2026 || m != time.September || d != 30 {
		t.Fatalf("Today() at 23:30 EAT on the last day = %v, want 2026-09-30", today)
	}
	if NewPeriod(Now()) != (Period{Year: 2026, Month: time.September}) {
		t.Fatalf("NewPeriod(Now()) at 23:30 EAT on the 30th = %v, want September — not yet the new month", NewPeriod(Now()))
	}
}

// TestAtNairobiMidnightReanchorsUTCScannedDate is the exact shape of a
// Postgres DATE column round-tripped through database/sql: the driver hands
// back a time.Time at midnight UTC for the calendar date stored, which is
// not the same instant as midnight in Nairobi. AtNairobiMidnight must
// re-anchor it to the same Y/M/D at Nairobi midnight, not shift the day.
func TestAtNairobiMidnightReanchorsUTCScannedDate(t *testing.T) {
	scanned := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) // a DATE column holding 2026-10-01
	got := AtNairobiMidnight(scanned)
	if y, m, d := got.Date(); y != 2026 || m != time.October || d != 1 {
		t.Fatalf("AtNairobiMidnight(%v) = %v, want the same calendar date", scanned, got)
	}
	if got.Location() != Nairobi {
		t.Fatalf("AtNairobiMidnight(%v) location = %v, want Nairobi", scanned, got.Location())
	}
	// The re-anchored instant is 3 hours earlier than the UTC-scanned one
	// (Nairobi midnight happens before UTC midnight) — this is precisely
	// the skew that caused the original bug when compared against a
	// moneyfmt.Now() taken in that 3-hour window.
	if !got.Before(scanned) {
		t.Fatalf("AtNairobiMidnight(%v) = %v, want an instant before the UTC-midnight scan", scanned, got)
	}
}
