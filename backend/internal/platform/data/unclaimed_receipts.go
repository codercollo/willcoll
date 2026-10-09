package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Money received on a channel nobody has claimed (migration 000023, C3.2): the
// payment is not posted, but it is never left to a log line either. Every such
// delivery is recorded here, idempotently, and stays open on the Super Admin
// System page until a Super Admin resolves it with a note.

const (
	UnclaimedOpen     = "open"
	UnclaimedResolved = "resolved"

	// MaxUnclaimedNoteLen bounds a resolution note.
	MaxUnclaimedNoteLen = 1000
)

// UnclaimedReceipt is one row as the System page sees it: the phone is masked
// and the payload is left out (it carries the full phone). The stored payload
// stays in the table for recovery.
type UnclaimedReceipt struct {
	ID             uuid.UUID      `json:"id"`
	MpesaReceipt   string         `json:"mpesa_receipt"`
	ChannelID      string         `json:"channel_id"`
	Product        string         `json:"product"`
	Amount         moneyfmt.Money `json:"amount"`
	Phone          string         `json:"phone"`
	ReceivedAt     time.Time      `json:"received_at"`
	Status         string         `json:"status"`
	ResolvedBy     *uuid.UUID     `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	ResolutionNote *string        `json:"resolution_note,omitempty"`
}

// UnclaimedReceiptStats summarises the open rows for the System page.
type UnclaimedReceiptStats struct {
	Open             int            `json:"open"`
	OpenAmount       moneyfmt.Money `json:"open_amount"`
	OldestAgeSeconds int            `json:"oldest_age_seconds"`
}

func unclaimedFromRow(r sqlc.UnclaimedReceipt) UnclaimedReceipt {
	return UnclaimedReceipt{
		ID: r.ID, MpesaReceipt: r.MpesaReceipt, ChannelID: r.ChannelID, Product: r.Product, Amount: r.Amount,
		Phone: mailer.MaskPhone(r.Msisdn), ReceivedAt: r.ReceivedAt, Status: r.Status,
		ResolvedBy: r.ResolvedBy, ResolvedAt: r.ResolvedAt, ResolutionNote: r.ResolutionNote,
	}
}

// UnclaimedReceiptModel is unclaimed_receipts access. Record runs on Store (a
// webhook has no tenant, and the table has no RLS); listing and resolving are
// Super Admin work on AdminStore, the only role allowed to UPDATE.
type UnclaimedReceiptModel struct {
	Store      db.Store
	AdminStore db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Record stores one unclaimed payment. Idempotent per M-Pesa receipt: a
// redelivery inserts nothing and reports recorded=false. A payment with no
// receipt number still gets a stable key derived from its payload, so it cannot
// be recorded twice either.
func (m UnclaimedReceiptModel) Record(ctx context.Context, mpesaReceipt, channelID, product string, amount moneyfmt.Money, msisdn string, raw json.RawMessage) (recorded bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if strings.TrimSpace(mpesaReceipt) == "" {
		mpesaReceipt = "NO-RECEIPT-" + uuid.NewSHA1(uuid.NameSpaceOID, raw).String()
	}
	n, err := m.Store.RecordUnclaimedReceipt(ctx, sqlc.RecordUnclaimedReceiptParams{
		MpesaReceipt: mpesaReceipt, ChannelID: channelID, Product: product, Amount: amount, Msisdn: msisdn, RawPayload: raw,
	})
	return n > 0, err
}

// ListOpen returns up to limit open receipts, oldest first. Never nil.
func (m UnclaimedReceiptModel) ListOpen(ctx context.Context, limit int) ([]UnclaimedReceipt, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.AdminStore.ListOpenUnclaimedReceipts(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]UnclaimedReceipt, len(rows))
	for i, r := range rows {
		out[i] = unclaimedFromRow(r)
	}
	return out, nil
}

// Stats counts the open receipts, their total and the age of the oldest.
func (m UnclaimedReceiptModel) Stats(ctx context.Context) (*UnclaimedReceiptStats, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.AdminStore.UnclaimedReceiptStats(ctx)
	if err != nil {
		return nil, err
	}
	return &UnclaimedReceiptStats{Open: int(row.OpenCount), OpenAmount: row.OpenAmount, OldestAgeSeconds: int(row.OldestAgeSeconds)}, nil
}

// Get returns one receipt (open or resolved), or ErrRecordNotFound.
func (m UnclaimedReceiptModel) Get(ctx context.Context, id uuid.UUID) (*UnclaimedReceipt, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.AdminStore.GetUnclaimedReceipt(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	r := unclaimedFromRow(row)
	return &r, nil
}

// ClaimantFirm reports the firm that now claims channelID, if any: a receipt is
// usually resolved because its channel got claimed, and that firm's audit log
// is where the resolution belongs. ok=false means the channel is still unclaimed.
func (m UnclaimedReceiptModel) ClaimantFirm(ctx context.Context, channelID string) (firm uuid.UUID, ok bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.AdminStore.GetPaymentChannelByID(ctx, channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return row.ManagerID, true, nil
}

// Resolve closes an open receipt, recording who did it and why. Resolving a
// receipt that is already resolved (or absent) is ErrRecordNotFound: the first
// resolution is never overwritten.
func (m UnclaimedReceiptModel) Resolve(ctx context.Context, id, adminID uuid.UUID, note string) (*UnclaimedReceipt, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	note = strings.TrimSpace(note)
	row, err := m.AdminStore.ResolveUnclaimedReceipt(ctx, sqlc.ResolveUnclaimedReceiptParams{ID: id, ResolvedBy: &adminID, ResolutionNote: &note})
	if err != nil {
		return nil, notFound(err)
	}
	r := unclaimedFromRow(row)
	return &r, nil
}
