package moneyfmt

import "time"

// Nairobi is Willcoll's one business calendar (build-phases.txt 3f-5):
// every date comparison in either API or in platform data — trial and
// subscription expiry, billing-run periods, due dates, reminder
// schedules, "current month" defaults — must be anchored to this
// timezone, never the server's own local time or a bare UTC instant. A
// database server in UTC and an office in Nairobi must always agree on
// what day it is; the server's own timezone must never matter.
var Nairobi = mustLoadLocation("Africa/Nairobi")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Africa/Nairobi is a stable, long-standing IANA zone; this can
		// only fail if the binary's tzdata is missing entirely, and must
		// never silently fall back to a different calendar.
		panic("moneyfmt: loading " + name + ": " + err.Error())
	}
	return loc
}

// NowFunc returns the current instant. Production code must never call
// this directly — use Now()/Today() — but a test may reassign it to pin
// "the current moment" for the duration of the test, restoring it
// afterward (e.g. via t.Cleanup):
//
//	moneyfmt.NowFunc = func() time.Time { return fixed }
//	defer func() { moneyfmt.NowFunc = time.Now }()
var NowFunc = time.Now

// Now returns the current moment, expressed in Africa/Nairobi. Equal in
// absolute instant to time.Now() (reassigning NowFunc aside) — only the
// Location differs — so it is always safe to use in place of a bare
// time.Now() wherever an instant is recorded, and is what any calendar
// extraction (Year/Month/Day, "is it a new month yet") must be done
// through instead.
func Now() time.Time {
	return NowFunc().In(Nairobi)
}

// Today returns midnight at the start of today, in Africa/Nairobi — the
// one definition of "today" every date comparison (trial expiry,
// subscription periods, billing-run periods, "current month" defaults)
// must use.
func Today() time.Time {
	t := Now()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Nairobi)
}

// AtNairobiMidnight reinterprets t's calendar date (Year/Month/Day) as
// midnight in Africa/Nairobi, discarding whatever Location or
// time-of-day t itself carried.
//
// This matters specifically for a Postgres DATE column: database/sql
// drivers scan one into a time.Time at midnight UTC, which is not the
// same instant as midnight in Nairobi. Comparing that raw scanned value
// against Now()/Today() directly silently shifts the real-world cutoff
// by Nairobi's UTC offset (3 hours) — exactly the bug this function
// exists to close. Every comparison against a DATE column (a
// subscription's current_period_end, a billing run's period, a reading's
// date) must reinterpret it through this function first.
func AtNairobiMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Nairobi)
}
