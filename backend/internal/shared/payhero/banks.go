package payhero

// Banks maps a bank name to the M-Pesa paybill PayHero settles a "bank"
// channel through (docs.payhero.co.ke: Payment Channels → Get PH Bank
// Paybills). That endpoint exists in PayHero's sidebar navigation, but the
// docs site is a JS-rendered SPA that did not yield its response shape to
// automated fetching, so this is the documented static fallback the task
// explicitly allows. Confirm against a live account and swap in a call to
// the real endpoint before go-live if its shape turns out to differ.
//
// Paybill numbers below are the banks' own well-known, publicly listed
// M-Pesa paybills (the same numbers printed on each bank's own website and
// statements), not anything PayHero-specific.
var Banks = []Bank{
	{Name: "KCB Bank", Paybill: 522522},
	{Name: "Equity Bank", Paybill: 247247},
	{Name: "Co-operative Bank", Paybill: 400200},
	{Name: "Absa Bank Kenya", Paybill: 303030},
	{Name: "Standard Chartered Bank", Paybill: 329329},
	{Name: "NCBA Bank", Paybill: 880100},
	{Name: "Diamond Trust Bank (DTB)", Paybill: 516600},
	{Name: "Family Bank", Paybill: 222111},
	{Name: "I&M Bank", Paybill: 542542},
	{Name: "Stanbic Bank", Paybill: 600100},
}

// Bank is one entry in the bank picker on the "Get paid" card: choosing a
// bank fills in its paybill automatically, so the manager never has to know it.
type Bank struct {
	Name    string `json:"name"`
	Paybill int64  `json:"paybill"`
}

// BankPaybill returns the paybill for a bank name (case-sensitive, matching
// Banks' Name field exactly, as the frontend dropdown sends it back), and
// whether it was found.
func BankPaybill(name string) (int64, bool) {
	for _, b := range Banks {
		if b.Name == name {
			return b.Paybill, true
		}
	}
	return 0, false
}
