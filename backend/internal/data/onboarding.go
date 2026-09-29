package data

import (
	"context"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/google/uuid"
)

// The setup checklist a new firm follows to go live. It is derived from what
// the firm has actually done, never stored, so it cannot drift from the data.

// OnboardingCounts is what the checklist is computed from.
type OnboardingCounts struct {
	Landlords, Properties, PropertiesWithChannel, Units, ActiveLeases int
	OpeningImports, WaterReadings, RentCharges, LiveQRCodes           int
}

// OnboardingStep is one line of the checklist.
type OnboardingStep struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Done   bool   `json:"done"`
	// Required steps must be done to be "live"; optional ones are offered.
	Required bool `json:"required"`
}

// OnboardingStatus is the whole checklist.
type OnboardingStatus struct {
	Steps []OnboardingStep `json:"steps"`
	// Complete is true when every required step is done.
	Complete bool `json:"complete"`
	// Next is the key of the first required step still to do ("" when complete).
	Next string `json:"next"`
	// Done and Total count the required steps, for a progress bar.
	Done  int `json:"done"`
	Total int `json:"total"`
}

// BuildOnboardingStatus turns counts into the checklist.
func BuildOnboardingStatus(c OnboardingCounts) OnboardingStatus {
	steps := []OnboardingStep{
		{Key: "landlord", Title: "Add your landlord", Required: true, Done: c.Landlords > 0,
			Detail: "The person or company that owns the property. Their bank details print on water and garbage bills."},
		{Key: "property", Title: "Add a property", Required: true, Done: c.Properties > 0,
			Detail: "Its name, water rate, management fee and whether you charge for garbage."},
		{Key: "tenants", Title: "Bring in your units and tenants", Required: true, Done: c.ActiveLeases > 0,
			Detail: fmt.Sprintf("%d units and %d tenants so far. Upload one spreadsheet, or add them one by one.", c.Units, c.ActiveLeases)},
		{Key: "balances", Title: "Check opening balances", Required: false, Done: c.OpeningImports > 0,
			Detail: "Arrears tenants owe today and deposits already held. Included when you upload the spreadsheet."},
		{Key: "water", Title: "Enter the starting water meter readings", Required: false, Done: c.WaterReadings > 0,
			Detail: "Type each meter's last reading as its Previous reading, so the first bill is correct."},
		{Key: "mpesa", Title: "Connect M-Pesa", Required: true, Done: c.Properties > 0 && c.PropertiesWithChannel >= c.Properties,
			Detail: "Give each property its PayHero channel so tenants' payments reach the right books."},
		{Key: "billing", Title: "Bill the first month", Required: true, Done: c.RentCharges > 0,
			Detail: "Generate rent invoices, then water and garbage at month end."},
		{Key: "stickers", Title: "Put up the door stickers", Required: false, Done: c.LiveQRCodes > 0,
			Detail: "Print each unit's QR code so tenants scan and pay."},
	}
	out := OnboardingStatus{Steps: steps, Complete: true}
	for _, s := range steps {
		if !s.Required {
			continue
		}
		out.Total++
		if s.Done {
			out.Done++
		} else {
			out.Complete = false
			if out.Next == "" {
				out.Next = s.Key
			}
		}
	}
	return out
}

// OnboardingModel reads a firm's setup progress.
type OnboardingModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Status returns the firm's checklist.
func (m OnboardingModel) Status(ctx context.Context, tenantID uuid.UUID) (OnboardingStatus, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var counts OnboardingCounts
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.OnboardingCounts(ctx, tenantID)
		if err != nil {
			return err
		}
		counts = OnboardingCounts{
			Landlords: int(row.Landlords), Properties: int(row.Properties), PropertiesWithChannel: int(row.PropertiesWithChannel),
			Units: int(row.Units), ActiveLeases: int(row.ActiveLeases), OpeningImports: int(row.OpeningImports),
			WaterReadings: int(row.WaterReadings), RentCharges: int(row.RentCharges), LiveQRCodes: int(row.LiveQrCodes),
		}
		return nil
	})
	if err != nil {
		return OnboardingStatus{}, err
	}
	return BuildOnboardingStatus(counts), nil
}
