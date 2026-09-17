package tenancy

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/uuid"
)

var testSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// TestCreateOrganizationWithFirstManagerTxAssignsDistinctSlugsForSameName
// proves two organizations created with the same business name both succeed
// and end up with different, valid slugs, instead of the second one
// violating organizations.slug's NOT NULL/UNIQUE constraints.
func TestCreateOrganizationWithFirstManagerTxAssignsDistinctSlugsForSameName(t *testing.T) {
	ctx := context.Background()
	s := NewService(testPool)
	name := "Willcoll Agencies " + uuid.NewString()[:8]

	orgA, _, err := s.CreateOrganizationWithFirstManagerTx(ctx, CreateOrganizationInput{
		Name:         name,
		FullName:     "Manager A",
		Phone:        uuid.NewString(),
		Email:        "slug-a-" + uuid.NewString() + "@example.com",
		PasswordHash: "irrelevant-for-this-test",
	})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	if orgA.Slug == "" {
		t.Fatal("org A slug is empty")
	}

	orgB, _, err := s.CreateOrganizationWithFirstManagerTx(ctx, CreateOrganizationInput{
		Name:         name, // same name → same base slug candidate
		FullName:     "Manager B",
		Phone:        uuid.NewString(),
		Email:        "slug-b-" + uuid.NewString() + "@example.com",
		PasswordHash: "irrelevant-for-this-test",
	})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}
	if orgB.Slug == "" {
		t.Fatal("org B slug is empty")
	}

	if orgA.Slug == orgB.Slug {
		t.Fatalf("both organizations got the same slug: %q", orgA.Slug)
	}
	if !testSlugPattern.MatchString(orgA.Slug) || !testSlugPattern.MatchString(orgB.Slug) {
		t.Fatalf("slugs not URL-safe: %q, %q", orgA.Slug, orgB.Slug)
	}
}
