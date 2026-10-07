package pdf

import (
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

func TestBuildSubscriptionInvoice(t *testing.T) {
	amount, err := moneyfmt.Parse("4500")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildSubscriptionInvoice(SubscriptionInvoice{
		InvoiceNo: "INV-9", ManagerName: "Acme Ltd", ManagerEmail: "acme@example.com",
		Period: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), IssuedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Amount: amount,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty PDF bytes")
	}
}
