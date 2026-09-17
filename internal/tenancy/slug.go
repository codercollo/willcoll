package tenancy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// slugNonAlnum matches any run of characters that isn't a lowercase letter
// or digit, collapsed to a single hyphen — same shape as the slug
// organizations.slug is validated against on update (handlers_branding.go's
// slugPattern, ^[a-z0-9]+(?:-[a-z0-9]+)*$), so a name-derived slug never
// needs correcting later.
var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// maxGeneratedSlugBase leaves room under the 100-character slug column limit
// (handlers_branding.go's validateSlug) for a "-NNNN" collision suffix.
const maxGeneratedSlugBase = 90

const slugSuffixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// slugify lowercases name, replaces every run of non [a-z0-9] characters
// with a single hyphen, and trims leading/trailing hyphens.
func slugify(name string) string {
	slug := slugNonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > maxGeneratedSlugBase {
		slug = strings.Trim(slug[:maxGeneratedSlugBase], "-")
	}
	return slug
}

func randomSlugSuffix(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = slugSuffixAlphabet[rand.Intn(len(slugSuffixAlphabet))]
	}
	return string(b)
}

// generateUniqueSlug derives a URL-safe slug from name and returns the first
// candidate not already used by another organization: the bare slug first,
// then "-2".."-5", then a random 4-character suffix. It only reads (a plain
// SELECT never aborts the caller's transaction the way a failed INSERT/
// UPDATE against the slug UNIQUE constraint would), so the caller does the
// actual insert with whatever candidate comes back.
func generateUniqueSlug(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	base := slugify(name)
	if base == "" {
		base = "org"
	}

	candidates := make([]string, 0, 6)
	candidates = append(candidates, base)
	for i := 2; i <= 5; i++ {
		candidates = append(candidates, fmt.Sprintf("%s-%d", base, i))
	}
	candidates = append(candidates, base+"-"+randomSlugSuffix(4))

	for _, candidate := range candidates {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE slug = $1)`, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("check slug availability: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}

	return "", errors.New("could not generate a unique organization slug")
}
