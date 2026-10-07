package data

import "testing"

func TestBuildOnboardingStatus(t *testing.T) {
	done := func(s OnboardingStatus, key string) bool {
		for _, st := range s.Steps {
			if st.Key == key {
				return st.Done
			}
		}
		t.Fatalf("no step %q", key)
		return false
	}

	t.Run("a brand new firm starts at the landlord", func(t *testing.T) {
		s := BuildOnboardingStatus(OnboardingCounts{})
		if s.Complete || s.Next != "landlord" || s.Done != 0 || s.Total != 5 {
			t.Errorf("status = %+v", s)
		}
	})
	t.Run("steps are checked off from real data, in order", func(t *testing.T) {
		s := BuildOnboardingStatus(OnboardingCounts{Landlords: 1, Properties: 1, Units: 10, ActiveLeases: 8})
		if !done(s, "landlord") || !done(s, "property") || !done(s, "tenants") || done(s, "mpesa") || s.Next != "mpesa" || s.Done != 3 {
			t.Errorf("status = %+v", s)
		}
	})
	t.Run("m-pesa is done only when every property has a channel", func(t *testing.T) {
		if done(BuildOnboardingStatus(OnboardingCounts{Properties: 2, PropertiesWithChannel: 1}), "mpesa") {
			t.Error("one of two properties connected counts as done")
		}
		if !done(BuildOnboardingStatus(OnboardingCounts{Properties: 2, PropertiesWithChannel: 2}), "mpesa") {
			t.Error("all properties connected is not done")
		}
		if done(BuildOnboardingStatus(OnboardingCounts{}), "mpesa") {
			t.Error("no properties cannot be connected")
		}
	})
	t.Run("optional steps never block being live", func(t *testing.T) {
		s := BuildOnboardingStatus(OnboardingCounts{Landlords: 1, Properties: 1, PropertiesWithChannel: 1, Units: 3, ActiveLeases: 3, RentCharges: 3})
		if !s.Complete || s.Next != "" || s.Done != 5 {
			t.Errorf("status = %+v", s)
		}
		if done(s, "balances") || done(s, "water") || done(s, "stickers") {
			t.Error("optional steps were marked done without data")
		}
	})
	t.Run("opening balances, readings and stickers each tick their own step", func(t *testing.T) {
		s := BuildOnboardingStatus(OnboardingCounts{OpeningImports: 1, WaterReadings: 4, LiveQRCodes: 2})
		if !done(s, "balances") || !done(s, "water") || !done(s, "stickers") {
			t.Errorf("status = %+v", s)
		}
	})
}
