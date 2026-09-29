package data

import (
	"context"
	"crypto/rand"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Lease-bound unit QR codes. The sticker encodes only a URL carrying the token.
// The token identifies the unit; it never authenticates the tenant, who still
// verifies their phone by SMS on the pay page.

// qrTokenAlphabet is 31 unambiguous characters: no 0/O/1/I/L. It is upper case
// only, so a code typed by hand is case-insensitive.
const qrTokenAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// QRTokenLength is 12 characters, about 59 bits: not guessable, and short
// enough to type from a sticker if the QR is damaged. This is the legacy
// length: every token already issued (and still scanning fine) is this
// long. New and rotated codes use NewQRTokenLength instead.
const QRTokenLength = 12

// NewQRTokenLength (8 characters, about 39 bits) is the random part of the
// current scheme: the unit label carries the rest of the entropy budget's
// job (telling codes apart at a glance), so the random part can be shorter
// and the whole printed code stays short — "A1-K7QM-2XH9" rather than a
// bare 12-character string with no idea which unit it is.
const NewQRTokenLength = 8

var (
	// ErrQRLeaseNotActive is returned when a code is asked for on a lease that
	// has ended: a terminated lease never gets a live code.
	ErrQRLeaseNotActive = errors.New("the lease is not active")

	// ErrQRInactive is what a scan of an unknown, revoked or ended code gets.
	// It deliberately does not say which, so tokens cannot be probed.
	ErrQRInactive = errors.New("this code is not active")
)

// UnitQR is one code as the manager sees it.
type UnitQR struct {
	ID uuid.UUID `json:"id"`
	// Token is the bare random part (12 legacy chars, or 8 current chars);
	// never sent to the client — ScanCode (the URL path) and ShortCode (the
	// printed hand-typing form) are what leave the server.
	Token         string     `json:"-"`
	ScanCode      string     `json:"-"`
	ShortCode     string     `json:"short_code"`
	LeaseID       uuid.UUID  `json:"lease_id"`
	UnitID        uuid.UUID  `json:"unit_id"`
	PropertyID    uuid.UUID  `json:"property_id"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	ScanCount     int32      `json:"scan_count"`
	LastScannedAt *time.Time `json:"last_scanned_at,omitempty"`
}

// QRTarget is what a public scan resolves to: enough to build the pay-page
// redirect and to count the scan, and nothing about the tenant.
type QRTarget struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	PropertySlug string
	UnitCode     string
}

// GenerateQRToken returns a fresh random legacy-length (12-character) token
// from crypto/rand. Kept for the tests exercising the alphabet/rejection
// sampling; createQR itself now draws NewQRTokenLength tokens via
// randomQRToken for every new or rotated code.
func GenerateQRToken() (string, error) { return randomQRToken(QRTokenLength) }

// randomQRToken draws a fresh random token of the given length from
// crypto/rand, using rejection sampling so no character is more likely than
// another.
func randomQRToken(length int) (string, error) {
	const n = len(qrTokenAlphabet)
	const limit = 256 - (256 % n) // bytes at or above this would bias the pick
	out := make([]byte, 0, length)
	buf := make([]byte, length*2)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, qrTokenAlphabet[int(b)%n])
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}

// NormalizeQRCode turns whatever a person typed into the stored form: upper
// case, dashes and spaces ignored. "k7qm-2xh9 ptrb" and "K7QM2XH9PTRB" match.
func NormalizeQRCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "", "_", "").Replace(strings.TrimSpace(s)))
}

// ValidQRCode reports whether s (already normalised) could be a bare token
// at all — legacy (12 characters) or current (8) — so obviously malformed
// input never reaches the database.
func ValidQRCode(s string) bool {
	if len(s) != QRTokenLength && len(s) != NewQRTokenLength {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(qrTokenAlphabet, r) {
			return false
		}
	}
	return true
}

// FormatQRShortCode groups a bare token for printing: K7QM2XH9PTRB ->
// K7QM-2XH9-PTRB. A current-scheme (8-character) token still gets the unit
// prefix from FormatUnitQRCodes; this is the token-only grouping used for
// both schemes' trailing part.
func FormatQRShortCode(token string) string {
	var parts []string
	for len(token) > 4 {
		parts = append(parts, token[:4])
		token = token[4:]
	}
	return strings.Join(append(parts, token), "-")
}

// unitLabelNo drops the "NO"/"NO." abbreviation ("Number") entirely, not
// just its punctuation: "SHOP NO.1" reads as "shop number 1", and the label
// printed on a sticker compacts that all the way to "SHOP1", not "SHOPNO1".
var unitLabelNo = regexp.MustCompile(`\bNO\.?\s*`)

// NormalizeUnitLabel turns a unit_code into the form printed on its QR code
// and matched against a scanned prefix: upper case, no spaces, dots, or "NO"
// abbreviation. "SHOP NO.1" -> "SHOP1", "1A" -> "1A".
func NormalizeUnitLabel(unitCode string) string {
	s := unitLabelNo.ReplaceAllString(strings.ToUpper(unitCode), "")
	return strings.NewReplacer(" ", "", ".", "").Replace(s)
}

// FormatUnitQRCodes builds the scan code (what goes in the URL, /q/<this>)
// and the short code (what's printed for hand-typing) for one unit's QR
// token. A legacy (12-character) token has no unit prefix — it was issued
// before this scheme and keeps working exactly as scanned; a current
// (8-character) token is always prefixed, e.g. "A1-K7QM2XH9" /
// "A1-K7QM-2XH9".
func FormatUnitQRCodes(token, unitCode string) (scanCode, shortCode string) {
	if len(token) != NewQRTokenLength {
		return token, FormatQRShortCode(token)
	}
	prefix := NormalizeUnitLabel(unitCode)
	return prefix + "-" + token, prefix + "-" + FormatQRShortCode(token)
}

// unitQRFromRow builds the manager-facing view of a code row. unitCode is
// needed to build the current scheme's unit-prefixed scan/short codes
// (FormatUnitQRCodes is a no-op prefix-wise for a legacy token).
func unitQRFromRow(row sqlc.UnitQrCode, unitCode string) UnitQR {
	scanCode, shortCode := FormatUnitQRCodes(row.Token, unitCode)
	return UnitQR{
		ID: row.ID, Token: row.Token, ScanCode: scanCode, ShortCode: shortCode, LeaseID: row.LeaseID, UnitID: row.UnitID,
		PropertyID: row.PropertyID, CreatedAt: row.CreatedAt, RevokedAt: row.RevokedAt, ScanCount: row.ScanCount, LastScannedAt: row.LastScannedAt,
	}
}

// UnitQRModel manages the codes. Store is the app pool (tenant transactions);
// Resolver is the admin pool used only for the public token lookup.
type UnitQRModel struct {
	Store    db.Store
	Resolver db.Store
	Timeout  time.Duration
}

// QRSticker is everything the sticker PNG/PDF needs: the code itself, the
// unit's normalised label, the property's name and slug, and the rent
// figure off the active lease. Nothing about the tenant.
type QRSticker struct {
	QR           UnitQR
	UnitLabel    string
	PropertyName string
	PropertySlug string
	RentAmount   moneyfmt.Money
}

// StickerData is GetOrCreate plus the property/unit/rent context the
// sticker prints. One request, one transaction.
func (m UnitQRModel) StickerData(ctx context.Context, tenantID, createdBy, leaseID uuid.UUID) (*QRSticker, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out QRSticker
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		lease, err := activeLease(ctx, q, tenantID, leaseID)
		if err != nil {
			return err
		}
		unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: lease.UnitID})
		if err != nil {
			return notFound(err)
		}
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: unit.PropertyID})
		if err != nil {
			return notFound(err)
		}

		var qr UnitQR
		if row, err := q.GetActiveUnitQRCodeByLease(ctx, sqlc.GetActiveUnitQRCodeByLeaseParams{TenantID: tenantID, LeaseID: leaseID}); err == nil {
			qr = unitQRFromRow(row, unit.UnitCode)
		} else if errors.Is(notFound(err), ErrRecordNotFound) {
			row, unitCode, err := createQR(ctx, q, tenantID, createdBy, lease)
			if err != nil {
				return err
			}
			qr = unitQRFromRow(row, unitCode)
		} else {
			return err
		}

		out = QRSticker{
			QR: qr, UnitLabel: NormalizeUnitLabel(unit.UnitCode),
			PropertyName: property.Name, PropertySlug: property.Slug, RentAmount: lease.RentAmount,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// BulkQRSkip is one lease the bulk run didn't issue a sticker for, and why.
type BulkQRSkip struct {
	LeaseID uuid.UUID `json:"lease_id"`
	Reason  string    `json:"reason"`
}

// BulkQRResult is a bulk sticker run's outcome: the sheet's stickers
// (naturally sorted by unit — A1, A2, ..., A10, B1), and a manager-facing
// summary of what happened to every lease id asked for.
type BulkQRResult struct {
	Stickers []QRSticker  `json:"-"`
	Created  int          `json:"created"`
	Reused   int          `json:"reused"`
	Skipped  []BulkQRSkip `json:"skipped"`
}

// BulkGetOrCreate get-or-creates one active code per lease on propertyID —
// idempotent, never rotating an existing code — and returns them naturally
// sorted by unit code for the sticker sheet. A lease that doesn't exist
// (for this tenant), isn't active (a vacant unit has no lease to bind a
// code to), or isn't on propertyID is skipped, not an error: the run
// finishes and reports it.
func (m UnitQRModel) BulkGetOrCreate(ctx context.Context, tenantID, createdBy, propertyID uuid.UUID, leaseIDs []uuid.UUID) (*BulkQRResult, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	result := &BulkQRResult{}
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		for _, leaseID := range leaseIDs {
			lease, err := activeLease(ctx, q, tenantID, leaseID)
			if err != nil {
				reason := "lease not found"
				if errors.Is(err, ErrQRLeaseNotActive) {
					reason = "the lease has ended"
				}
				result.Skipped = append(result.Skipped, BulkQRSkip{LeaseID: leaseID, Reason: reason})
				continue
			}
			unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: lease.UnitID})
			if err != nil {
				return notFound(err)
			}
			if unit.PropertyID != propertyID {
				result.Skipped = append(result.Skipped, BulkQRSkip{LeaseID: leaseID, Reason: "not a unit on this property"})
				continue
			}
			property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: unit.PropertyID})
			if err != nil {
				return notFound(err)
			}

			var qr UnitQR
			if row, err := q.GetActiveUnitQRCodeByLease(ctx, sqlc.GetActiveUnitQRCodeByLeaseParams{TenantID: tenantID, LeaseID: leaseID}); err == nil {
				qr = unitQRFromRow(row, unit.UnitCode)
				result.Reused++
			} else if errors.Is(notFound(err), ErrRecordNotFound) {
				row, unitCode, err := createQR(ctx, q, tenantID, createdBy, lease)
				if err != nil {
					return err
				}
				qr = unitQRFromRow(row, unitCode)
				result.Created++
			} else {
				return err
			}

			result.Stickers = append(result.Stickers, QRSticker{
				QR: qr, UnitLabel: NormalizeUnitLabel(unit.UnitCode),
				PropertyName: property.Name, PropertySlug: property.Slug, RentAmount: lease.RentAmount,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result.Stickers, func(i, j int) bool {
		return naturalLess(result.Stickers[i].UnitLabel, result.Stickers[j].UnitLabel)
	})
	return result, nil
}

// naturalLess orders unit labels the way a person would: "A1" < "A2" <
// "A10" < "B1", not the ASCII "A1" < "A10" < "A2" a plain string compare
// gives. It splits each label into alternating letter/digit runs and
// compares digit runs numerically.
func naturalLess(a, b string) bool {
	ar, br := splitNatural(a), splitNatural(b)
	for i := 0; i < len(ar) && i < len(br); i++ {
		if ar[i] == br[i] {
			continue
		}
		an, aErr := strconv.Atoi(ar[i])
		bn, bErr := strconv.Atoi(br[i])
		if aErr == nil && bErr == nil {
			return an < bn
		}
		return ar[i] < br[i]
	}
	return len(ar) < len(br)
}

// splitNatural breaks a string into runs of consecutive digits and
// consecutive non-digits: "SHOP12" -> ["SHOP", "12"].
func splitNatural(s string) []string {
	var runs []string
	var cur strings.Builder
	var curIsDigit bool
	for i, r := range s {
		isDigit := r >= '0' && r <= '9'
		if i > 0 && isDigit != curIsDigit {
			runs = append(runs, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
		curIsDigit = isDigit
	}
	if cur.Len() > 0 {
		runs = append(runs, cur.String())
	}
	return runs
}

// GetOrCreate returns the lease's live code, creating one if it has none. The
// lease must be active.
func (m UnitQRModel) GetOrCreate(ctx context.Context, tenantID, createdBy, leaseID uuid.UUID) (*UnitQR, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var qr UnitQR
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		lease, err := activeLease(ctx, q, tenantID, leaseID)
		if err != nil {
			return err
		}
		if row, err := q.GetActiveUnitQRCodeByLease(ctx, sqlc.GetActiveUnitQRCodeByLeaseParams{TenantID: tenantID, LeaseID: leaseID}); err == nil {
			unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: row.UnitID})
			if err != nil {
				return err
			}
			qr = unitQRFromRow(row, unit.UnitCode)
			return nil
		} else if !errors.Is(notFound(err), ErrRecordNotFound) {
			return err
		}
		row, unitCode, err := createQR(ctx, q, tenantID, createdBy, lease)
		if err != nil {
			return err
		}
		qr = unitQRFromRow(row, unitCode)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &qr, nil
}

// Rotate retires the lease's live code and issues a new one, in one
// transaction, so the lease is never left with two live codes or none.
func (m UnitQRModel) Rotate(ctx context.Context, tenantID, createdBy, leaseID uuid.UUID) (*UnitQR, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var qr UnitQR
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		lease, err := activeLease(ctx, q, tenantID, leaseID)
		if err != nil {
			return err
		}
		if _, err := q.RevokeUnitQRCodesForLease(ctx, sqlc.RevokeUnitQRCodesForLeaseParams{TenantID: tenantID, LeaseID: leaseID}); err != nil {
			return err
		}
		row, unitCode, err := createQR(ctx, q, tenantID, createdBy, lease)
		if err != nil {
			return err
		}
		qr = unitQRFromRow(row, unitCode)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &qr, nil
}

// Resolve is the public scan lookup. It returns ErrQRInactive for an unknown,
// revoked or ended code alike.
func (m UnitQRModel) Resolve(ctx context.Context, code string) (*QRTarget, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	// A current-scheme code is "<UNIT>-<TOKEN>": the unit prefix is never
	// part of the token itself, so it must be split off BEFORE
	// NormalizeQRCode, which otherwise strips every dash indiscriminately
	// (right, for a legacy bare token; wrong here, it would merge the
	// prefix into the token). A legacy code has no prefix at all.
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	var unitPrefix, token string
	if i := strings.IndexByte(trimmed, '-'); i >= 0 {
		unitPrefix = NormalizeUnitLabel(trimmed[:i])
		token = NormalizeQRCode(trimmed[i+1:])
	} else {
		token = NormalizeQRCode(trimmed)
	}
	if !ValidQRCode(token) {
		return nil, ErrQRInactive
	}
	row, err := m.Resolver.ResolveUnitQRToken(ctx, token)
	if err != nil {
		if errors.Is(notFound(err), ErrRecordNotFound) {
			return nil, ErrQRInactive
		}
		return nil, err
	}
	if !row.Active.Valid || !row.Active.Bool {
		return nil, ErrQRInactive
	}
	// The unit prefix isn't a security boundary (the token alone already
	// identifies the unit) — it's a tamper/mixup guard: a code for A1
	// scanned as if it were B2 must not resolve, so a sticker swapped onto
	// the wrong door is caught rather than silently accepted.
	if unitPrefix != "" && unitPrefix != NormalizeUnitLabel(row.UnitCode) {
		return nil, ErrQRInactive
	}
	return &QRTarget{ID: row.ID, TenantID: row.TenantID, PropertySlug: row.PropertySlug, UnitCode: row.UnitCode}, nil
}

// RecordScan counts one scan of a live code. A code revoked in the meantime is
// ErrQRInactive.
func (m UnitQRModel) RecordScan(ctx context.Context, t *QRTarget) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, t.TenantID, func(q sqlc.Querier) error {
		n, err := q.IncrementUnitQRScan(ctx, sqlc.IncrementUnitQRScanParams{TenantID: t.TenantID, ID: t.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrQRInactive
		}
		return nil
	})
}

// revokeQRCodesForLease retires a lease's code inside the caller's transaction.
// The lease-termination path calls it, so a sticker stops working at the same
// instant the lease ends, atomically with it.
func revokeQRCodesForLease(ctx context.Context, q sqlc.Querier, tenantID, leaseID uuid.UUID) error {
	_, err := q.RevokeUnitQRCodesForLease(ctx, sqlc.RevokeUnitQRCodesForLeaseParams{TenantID: tenantID, LeaseID: leaseID})
	return err
}

func activeLease(ctx context.Context, q sqlc.Querier, tenantID, leaseID uuid.UUID) (sqlc.Lease, error) {
	lease, err := q.GetLease(ctx, sqlc.GetLeaseParams{TenantID: tenantID, ID: leaseID})
	if err != nil {
		if errors.Is(notFound(err), ErrRecordNotFound) {
			return lease, ErrLeaseNotFound // another firm's lease looks exactly like a missing one
		}
		return lease, err
	}
	if lease.Status != LeaseStatusActive {
		return lease, ErrQRLeaseNotActive
	}
	return lease, nil
}

// createQR inserts a fresh code, retrying on the (astronomically unlikely)
// token collision.
func createQR(ctx context.Context, q sqlc.Querier, tenantID, createdBy uuid.UUID, lease sqlc.Lease) (sqlc.UnitQrCode, string, error) {
	unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: lease.UnitID})
	if err != nil {
		return sqlc.UnitQrCode{}, "", notFound(err)
	}
	for range 5 {
		token, err := randomQRToken(NewQRTokenLength)
		if err != nil {
			return sqlc.UnitQrCode{}, "", err
		}
		row, err := q.CreateUnitQRCode(ctx, sqlc.CreateUnitQRCodeParams{
			Token: token, LeaseID: lease.ID, UnitID: lease.UnitID, PropertyID: unit.PropertyID, TenantID: tenantID, CreatedBy: createdBy,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "unit_qr_codes_token_key" {
			continue // collided with an existing token: draw again
		}
		return row, unit.UnitCode, err
	}
	return sqlc.UnitQrCode{}, "", errors.New("could not generate a unique QR token")
}
