package subscriptions

// Tier is one of the four subscription bands from spec §12.1.
type Tier string

const (
	TierStarter      Tier = "starter"
	TierGrowth       Tier = "growth"
	TierProfessional Tier = "professional"
	TierEnterprise   Tier = "enterprise"
)

// ComputeTier maps a unit count to its subscription tier.
func ComputeTier(unitCount int) Tier {
	switch {
	case unitCount <= 15:
		return TierStarter
	case unitCount <= 50:
		return TierGrowth
	case unitCount <= 150:
		return TierProfessional
	default:
		return TierEnterprise
	}
}

// MonthlyFeeKES returns the fixed monthly fee in whole KES.
func (t Tier) MonthlyFeeKES() int {
	switch t {
	case TierStarter:
		return 1500
	case TierGrowth:
		return 3500
	case TierProfessional:
		return 7500
	case TierEnterprise:
		return 12000
	default:
		return 0
	}
}
