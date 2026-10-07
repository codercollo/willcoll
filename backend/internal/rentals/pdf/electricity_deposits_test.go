package pdf

import "testing"

func TestBuildElectricityDeposits(t *testing.T) {
	rows := []ElectricityDepositRow{
		{HouseNo: "A1", TenantName: "JOHN KAMAU", Required: money(t, "2000"), Paid: money(t, "2000"), Balance: money(t, "0")},
		{HouseNo: "A2", TenantName: "MARY WANJIKU", Required: money(t, "2000"), Paid: money(t, "500"), Balance: money(t, "1500")},
	}
	b, err := BuildElectricityDeposits("KIWI PLACE", rows, money(t, "4000"), money(t, "2500"), money(t, "1500"))
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, b)
	mustContain(t, text, "KIWI PLACE", "ELECTRICITY DEPOSITS", "A1", "JOHN KAMAU", "2,000.00", "1,500.00", "TOTALS", "4,000.00", "2,500.00")
}
