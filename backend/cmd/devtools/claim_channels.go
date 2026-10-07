package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/google/uuid"
)

// runClaimChannels is `devtools claim-channels` (make dev/setup runs it after
// seeding): it makes sure every rentals property that has a payhero_channel_id
// also has its claim in willcoll_platform.payment_channels, then VERIFIES it.
//
// The repair uses platformdata.ChannelModel.Claim, the same idempotent claim
// path rentals-api runs when a channel is typed onto a property (3i-d), so a
// dev database seeded before the seeder claimed channels (PH-CH-RUNDA-DEMO)
// is fixed in place, with no reset and no data lost. The verification is an
// independent read of payment_channels, so a claim that could not be made
// (another owner holds the channel, say) cannot pass unnoticed: it exits
// non-zero naming each property and channel.
//
// Reads properties on DB_ADMIN_DSN (willcoll_admin, BYPASSRLS, read-only use)
// and writes through PLATFORM_DSN (the willcoll_platform owner or admin DSN
// that dev/setup passes the seeder). PAYHERO_CALLBACK_BASE_URL is optional.
func runClaimChannels() {
	adminDSN := os.Getenv("DB_ADMIN_DSN")
	if adminDSN == "" {
		log.Fatal("DB_ADMIN_DSN is not set: the rentals database's willcoll_admin DSN (to list every property with a channel)")
	}
	rentals := mustOpen(adminDSN)
	defer func() { _ = rentals.Close() }()
	models, closeDB := openPlatform()
	defer closeDB()
	platformDSN := os.Getenv("PLATFORM_DSN")
	platform := mustOpen(platformDSN)
	defer func() { _ = platform.Close() }()

	ctx := context.Background()
	problems, total, err := claimAndVerifyChannels(ctx, rentals, platform, models.Channels, os.Getenv("PAYHERO_CALLBACK_BASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "claim-channels: %d of %d property channel(s) have NO matching claim in platform.payment_channels:\n", len(problems), total)
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  - %s\n", p)
		}
		fmt.Fprintln(os.Stderr, "Without a claim, every collections webhook for that channel falls back to local-only posting.")
		os.Exit(1)
	}
	fmt.Printf("claim-channels: all %d property channel(s) have a matching platform claim\n", total)
}

type channelProperty struct {
	propertyID, tenantID uuid.UUID
	name, channelID      string
}

// claimAndVerifyChannels claims every property channel (best effort: a
// conflict is left for the verification to report) and returns one message
// per property whose channel has no matching claim afterwards.
func claimAndVerifyChannels(ctx context.Context, rentals, platform *sql.DB, channels platformdata.ChannelModel, callbackBase string) (problems []string, total int, err error) {
	rows, err := rentals.QueryContext(ctx, `
		SELECT id, tenant_id, name, payhero_channel_id FROM properties
		WHERE payhero_channel_id IS NOT NULL AND payhero_channel_id != '' AND deleted_at IS NULL
		ORDER BY name`)
	if err != nil {
		return nil, 0, fmt.Errorf("listing properties with a channel: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var props []channelProperty
	for rows.Next() {
		var p channelProperty
		if err := rows.Scan(&p.propertyID, &p.tenantID, &p.name, &p.channelID); err != nil {
			return nil, 0, err
		}
		props = append(props, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	callback := ""
	if base := strings.TrimRight(strings.TrimSpace(callbackBase), "/"); base != "" {
		callback = base + "/v1/webhooks/payhero/collections"
	}
	for _, p := range props {
		claimed, cerr := channels.Claim(ctx, p.tenantID, platformdata.ProductRentals, p.channelID, p.propertyID, callback)
		switch {
		case cerr != nil:
			log.Printf("claim-channels: %s (%s): %v", p.name, p.channelID, cerr)
		case claimed:
			fmt.Printf("claim-channels: claimed %s for %s\n", p.channelID, p.name)
		}
	}

	// Independent verification: read the claims back.
	for _, p := range props {
		var product string
		var owner, manager uuid.NullUUID
		verr := platform.QueryRowContext(ctx, `SELECT product, owner_id, manager_id FROM payment_channels WHERE channel_id = $1`, p.channelID).
			Scan(&product, &owner, &manager)
		switch {
		case verr == sql.ErrNoRows:
			problems = append(problems, fmt.Sprintf("property %q (%s): channel %s is not claimed", p.name, p.propertyID, p.channelID))
		case verr != nil:
			return nil, 0, fmt.Errorf("verifying channel %s: %w", p.channelID, verr)
		case product != platformdata.ProductRentals || !owner.Valid || owner.UUID != p.propertyID || !manager.Valid || manager.UUID != p.tenantID:
			problems = append(problems, fmt.Sprintf("property %q (%s): channel %s is claimed by someone else (product %s, owner %s)", p.name, p.propertyID, p.channelID, product, owner.UUID))
		}
	}
	return problems, len(props), nil
}
