package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/api"
	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
)

type portfolioResponse struct {
	Data []struct {
		PropertyID uuid.UUID `json:"property_id"`
		Name       string    `json:"name"`
		Units      int       `json:"units"`
	} `json:"data"`
}

func TestLandlordPortfolioDoesNotLeakOtherLandlordProperty(t *testing.T) {
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Report Org", "Report Org", "report-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert org: %v", err)
	}

	seedUser := func(role string) uuid.UUID {
		id := uuid.New()
		if _, err := testPool.Exec(ctx, `
			INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
			VALUES ($1, $2, $3, $4, $5, $6, true, true, 'active')`,
			id, orgID, role, uuid.NewString(), role+"-"+uuid.NewString()+"@example.com", role,
		); err != nil {
			t.Fatalf("insert %s user: %v", role, err)
		}
		return id
	}

	managerID := seedUser("manager")
	landlordA := seedUser("landlord")
	landlordB := seedUser("landlord")

	propertyA := uuid.New()
	propertyB := uuid.New()
	for _, p := range []struct {
		id   uuid.UUID
		name string
	}{
		{propertyA, "Property A"},
		{propertyB, "Property B"},
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO properties (id, organization_id, manager_id, name, location)
			VALUES ($1, $2, $3, $4, $5)`,
			p.id, orgID, managerID, p.name, "Test Location",
		); err != nil {
			t.Fatalf("insert property: %v", err)
		}
	}

	if _, err := testPool.Exec(ctx, `
		INSERT INTO property_ownership (property_id, landlord_id)
		VALUES ($1, $2), ($3, $4)`,
		propertyA, landlordA, propertyB, landlordB,
	); err != nil {
		t.Fatalf("insert ownership: %v", err)
	}

	authSvc := auth.NewService([]byte("01234567890123456789012345678901"))
	tokenA, err := authSvc.IssueToken(landlordA, "landlord", orgID)
	if err != nil {
		t.Fatalf("issue landlord A token: %v", err)
	}

	srv := api.NewServer(
		testPool,
		authSvc,
		tenancy.NewService(testPool),
		&recordingMailer{},
		branding.NewService(testPool),
		money.NewService(testPool),
		nil,
		nil,
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doJSON(t, http.MethodGet, ts.URL+"/v1/reports/portfolio", tokenA, nil)
	expectStatus(t, resp, http.StatusOK)

	var out portfolioResponse
	decodeBody(t, resp, &out)

	var sawA, sawB bool
	for _, row := range out.Data {
		if row.PropertyID == propertyA {
			sawA = true
		}
		if row.PropertyID == propertyB {
			sawB = true
		}
	}
	if !sawA {
		t.Fatal("landlord A portfolio did not include their own property")
	}
	if sawB {
		t.Fatal("landlord A portfolio leaked another landlord's property")
	}
}
