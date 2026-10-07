package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrChannelAlreadyRegistered is returned when a channel id already belongs
// to a different (product, owner) — the one-owner-per-channel rule
// (system-design.txt A.6, build-phases.txt 3g-1). The caller maps this to
// 409, never silently reassigns ownership.
var ErrChannelAlreadyRegistered = errors.New("this payment channel is already registered to a different property")

// ErrChannelHasPayments is returned by Release when payment_inbox already
// holds receipts for the channel: that history must stay attributable, so the
// claim cannot be deleted. The caller keeps the claim and carries on.
var ErrChannelHasPayments = errors.New("this payment channel has recorded payments and cannot be released")

// PaymentChannel is a manager's claim on one PayHero channel id, for one
// product's one owner (e.g. a Rentals property).
type PaymentChannel struct {
	ChannelID   string
	ManagerID   uuid.UUID
	Product     string
	OwnerID     *uuid.UUID
	CallbackURL string
}

func paymentChannelFromRow(r sqlc.PaymentChannel) *PaymentChannel {
	return &PaymentChannel{
		ChannelID: r.ChannelID, ManagerID: r.ManagerID, Product: r.Product,
		OwnerID: r.OwnerID, CallbackURL: r.CallbackUrl,
	}
}

// ChannelModel is tenant-scoped payment_channels access.
type ChannelModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Register claims channelID for (product, ownerID) under tenantID.
// ErrChannelAlreadyRegistered if the channel id already belongs to someone
// else (the database's own primary key is the actual guard; this just names
// the failure).
func (m ChannelModel) Register(ctx context.Context, tenantID uuid.UUID, product, channelID string, ownerID *uuid.UUID, callbackURL string) (*PaymentChannel, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *PaymentChannel
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.RegisterPaymentChannel(ctx, sqlc.RegisterPaymentChannelParams{
			ChannelID: channelID, ManagerID: tenantID, Product: product, OwnerID: ownerID, CallbackUrl: callbackURL,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
				return ErrChannelAlreadyRegistered
			}
			return err
		}
		out = paymentChannelFromRow(row)
		return nil
	})
	return out, err
}

// Claim is Register made idempotent for the same owner: the one claim path
// for a channel typed onto a property (rentals-api's create/update, build-
// phases.txt 3i-d) and for the dev seed. claimed reports whether this call
// created the claim (so a caller can release it if a later save fails); a
// claim this same (product, owner) already holds is success with
// claimed=false. ErrChannelAlreadyRegistered when the channel belongs to
// another owner, another product or another manager.
func (m ChannelModel) Claim(ctx context.Context, tenantID uuid.UUID, product, channelID string, ownerID uuid.UUID, callbackURL string) (claimed bool, err error) {
	_, err = m.Register(ctx, tenantID, product, channelID, &ownerID, callbackURL)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrChannelAlreadyRegistered):
		existing, getErr := m.Get(ctx, tenantID, channelID)
		switch {
		case getErr == nil && existing.Product == product && existing.OwnerID != nil && *existing.OwnerID == ownerID:
			return false, nil
		case getErr != nil && !errors.Is(getErr, ErrRecordNotFound):
			return false, getErr
		}
		return false, ErrChannelAlreadyRegistered
	default:
		return false, err
	}
}

// GetByOwner returns tenantID's channel for (product, ownerID), or
// ErrRecordNotFound if it has not registered one.
func (m ChannelModel) GetByOwner(ctx context.Context, tenantID uuid.UUID, product string, ownerID uuid.UUID) (*PaymentChannel, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *PaymentChannel
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetPaymentChannelByOwner(ctx, sqlc.GetPaymentChannelByOwnerParams{
			ManagerID: tenantID, Product: product, OwnerID: &ownerID,
		})
		if err != nil {
			return notFound(err)
		}
		out = paymentChannelFromRow(row)
		return nil
	})
	return out, err
}

// Get resolves channelID to its owner under tenantID, or ErrRecordNotFound
// if tenantID holds no such channel.
func (m ChannelModel) Get(ctx context.Context, tenantID uuid.UUID, channelID string) (*PaymentChannel, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *PaymentChannel
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetPaymentChannel(ctx, sqlc.GetPaymentChannelParams{ManagerID: tenantID, ChannelID: channelID})
		if err != nil {
			return notFound(err)
		}
		out = paymentChannelFromRow(row)
		return nil
	})
	return out, err
}

// Release drops (product, ownerID)'s claim on channelID under tenantID, so the
// channel can be claimed elsewhere. Releasing a claim that is not held is a
// no-op (idempotent). ErrChannelHasPayments if payment_inbox already holds
// receipts for the channel — the claim is then left in place.
func (m ChannelModel) Release(ctx context.Context, tenantID uuid.UUID, product, channelID string, ownerID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		_, err := q.ReleasePaymentChannel(ctx, sqlc.ReleasePaymentChannelParams{
			ManagerID: tenantID, ChannelID: channelID, Product: product, OwnerID: &ownerID,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
			return ErrChannelHasPayments
		}
		return err
	})
}
