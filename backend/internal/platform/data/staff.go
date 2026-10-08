package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Staff roles, ranked least to most privileged (system-design.txt A.1).
// The manager's own login is always the implicit owner — never a staff
// row — so it never needs to appear here.
const (
	RoleViewer     = "viewer"
	RoleOfficer    = "officer"
	RoleAccountant = "accountant"
	RoleOwner      = "owner"
)

// Staff statuses.
const (
	StaffInvited  = "invited"
	StaffActive   = "active"
	StaffDisabled = "disabled"
)

var staffRoles = []string{RoleOwner, RoleAccountant, RoleOfficer, RoleViewer}

// roleRank orders roles for RoleAtLeast's minimum-role check.
var roleRank = map[string]int{RoleViewer: 0, RoleOfficer: 1, RoleAccountant: 2, RoleOwner: 3}

// RoleAtLeast reports whether role meets minimum, by rank. An unknown role
// never meets any minimum (fails closed) — build-phases.txt 3f-2's
// permission gate depends on this never silently allowing a typo'd role.
func RoleAtLeast(role, minimum string) bool {
	r, ok1 := roleRank[role]
	m, ok2 := roleRank[minimum]
	return ok1 && ok2 && r >= m
}

// ErrDuplicateStaffEmail is returned when a manager already has a staff
// member with that email (staff.email is unique per manager, not global).
var ErrDuplicateStaffEmail = errors.New("duplicate email")

// Staff is a sub-account under a manager (system-design.txt A.1): everyday
// use of one product's dashboard, gated by Role and ModuleAccess.
type Staff struct {
	ID           uuid.UUID `json:"id"`
	ManagerID    uuid.UUID `json:"manager_id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        *string   `json:"phone,omitempty"`
	Password     password  `json:"-"`
	Role         string    `json:"role"`
	ModuleAccess []string  `json:"module_access"`
	// PropertyAccess narrows a non-owner's access to specific properties,
	// per product (D2). A missing or empty list means all properties; see
	// PropertyScope, the only way callers should read it.
	PropertyAccess map[string][]uuid.UUID `json:"property_access"`
	Status         string                 `json:"status"`
	CreatedAt      time.Time              `json:"created_at"`
	Version        int32                  `json:"-"`
}

// AnonymousStaff represents "no staff principal": either nobody is signed
// in, or the signed-in principal is the manager's own login (the implicit
// owner), never a staff row. Handlers check IsAnonymous() before treating
// a request as staff-scoped.
var AnonymousStaff = &Staff{}

// IsAnonymous reports whether s is the anonymous sentinel.
func (s *Staff) IsAnonymous() bool { return s == AnonymousStaff }

// maxScopedProperties bounds one product's property list: the list rides in
// every request's context and a Postgres setting, and a firm with more
// properties than this per clerk should be using the unrestricted role.
const maxScopedProperties = 200

// PropertyScope returns the property ids this staff member is limited to in
// product, or nil for "all properties". Owners are never restricted, whatever
// the stored value says, so a stale row cannot lock an owner out of data. A
// non-owner whose product list is empty or missing is unrestricted (D2).
func (s *Staff) PropertyScope(product string) []uuid.UUID {
	if s == nil || s.IsAnonymous() || s.Role == RoleOwner {
		return nil
	}
	if ids := s.PropertyAccess[product]; len(ids) > 0 {
		return ids
	}
	return nil
}

// NormalizePropertyAccess drops empty lists and duplicate ids so the stored
// value is canonical (an empty list and a missing key mean the same thing).
func NormalizePropertyAccess(in map[string][]uuid.UUID) map[string][]uuid.UUID {
	out := make(map[string][]uuid.UUID, len(in))
	for product, ids := range in {
		seen := map[uuid.UUID]bool{}
		var list []uuid.UUID
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				list = append(list, id)
			}
		}
		if len(list) > 0 {
			out[product] = list
		}
	}
	return out
}

// PropertyAccessEqual reports whether two normalised values scope the same
// properties (order-insensitive), so an unchanged value does not revoke
// sessions.
func PropertyAccessEqual(a, b map[string][]uuid.UUID) bool {
	a, b = NormalizePropertyAccess(a), NormalizePropertyAccess(b)
	if len(a) != len(b) {
		return false
	}
	for product, ids := range a {
		other := b[product]
		if len(ids) != len(other) {
			return false
		}
		set := map[uuid.UUID]bool{}
		for _, id := range other {
			set[id] = true
		}
		for _, id := range ids {
			if !set[id] {
				return false
			}
		}
	}
	return true
}

func ValidateStaff(v *validator.Validator, s *Staff) {
	v.Check(s.Name != "", "name", "must be provided")
	v.Check(len(s.Name) <= 200, "name", "must not be more than 200 bytes long")
	ValidateManagerEmail(v, s.Email)
	v.Check(validator.PermittedValue(s.Role, staffRoles...), "role", "must be one of owner, accountant, officer, viewer")
	for _, p := range s.ModuleAccess {
		v.Check(validator.PermittedValue(p, ProductRentals, ProductEstates, ProductWaste), "module_access", "must be one of rentals, estates, waste")
	}
	for product, ids := range s.PropertyAccess {
		v.Check(validator.PermittedValue(product, ProductRentals, ProductEstates, ProductWaste), "property_access", "keys must be one of rentals, estates, waste")
		v.Check(len(ids) <= maxScopedProperties, "property_access", "must not list more than 200 properties per product")
		if len(ids) > 0 {
			v.Check(validator.PermittedValue(product, s.ModuleAccess...), "property_access", "can only restrict a product the staff member has access to")
			v.Check(s.Role != RoleOwner, "property_access", "owners cannot be restricted to specific properties")
		}
	}
}

// StaffModel is the service layer for staff (system-design.txt A.1,
// build-phases.txt 3f-1).
type StaffModel struct {
	// Store is the willcoll_platform_app pool: owner-driven staff
	// management (invite/list/update) runs tenant-scoped via ExecTenantTx
	// — staff carries RLS (migration 000012), unlike managers/tokens.
	Store db.Store

	// AdminStore is willcoll_platform_admin (BYPASSRLS): the cross-tenant
	// reads/writes login and activation need before a tenant context
	// exists (FindActiveByEmail, GetForToken, Activate) — the same reason
	// ManagerModel's own token/email lookups never go through ExecTenantTx.
	AdminStore db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// marshalPropertyAccess encodes the column value ('{}' for none).
func marshalPropertyAccess(m map[string][]uuid.UUID) json.RawMessage {
	b, err := json.Marshal(NormalizePropertyAccess(m))
	if err != nil { // a map of string -> []uuid cannot fail to encode
		return json.RawMessage(`{}`)
	}
	return b
}

func staffFromRow(r sqlc.Staff) *Staff {
	// A value that does not decode restricts nothing it should not: it is
	// treated as no restriction only for the owner; for anyone else a corrupt
	// value must fail closed, so it becomes a scope no real property matches.
	var access map[string][]uuid.UUID
	if len(r.PropertyAccess) > 0 {
		if err := json.Unmarshal(r.PropertyAccess, &access); err != nil {
			access = map[string][]uuid.UUID{ProductRentals: {uuid.Nil}, ProductEstates: {uuid.Nil}, ProductWaste: {uuid.Nil}}
		}
	}
	return &Staff{
		PropertyAccess: NormalizePropertyAccess(access),
		ID:             r.ID, ManagerID: r.ManagerID, Name: r.Name, Email: r.Email, Phone: r.Phone,
		Password: password{hash: r.PasswordHash}, Role: r.Role, ModuleAccess: r.ModuleAccess,
		Status: r.Status, CreatedAt: r.CreatedAt, Version: r.Version,
	}
}

func staffWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "staff_manager_id_email_key" {
		return ErrDuplicateStaffEmail
	}
	return err
}

// Invite creates a staff row under tenantID: status invited, an unusable
// password (dummyPasswordHash — nobody can log in until Activate runs).
// The caller emails an activation link the same way manager signup does.
func (m StaffModel) Invite(ctx context.Context, tenantID uuid.UUID, s *Staff) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.CreateStaff(ctx, sqlc.CreateStaffParams{
			ManagerID: tenantID, Name: s.Name, Email: s.Email, Phone: s.Phone,
			PasswordHash: dummyPasswordHash(), Role: s.Role, ModuleAccess: s.ModuleAccess, Status: StaffInvited,
			PropertyAccess: marshalPropertyAccess(s.PropertyAccess),
		})
		if err != nil {
			return staffWriteError(err)
		}
		*s = *staffFromRow(row)
		return nil
	})
}

// Get returns tenantID's staff member id, or ErrRecordNotFound.
func (m StaffModel) Get(ctx context.Context, tenantID, id uuid.UUID) (*Staff, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *Staff
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetStaff(ctx, id)
		if err != nil {
			return notFound(err)
		}
		out = staffFromRow(row)
		return nil
	})
	return out, err
}

// GetByID returns the staff member id regardless of tenant, or
// ErrRecordNotFound — for resuming a staff session from its principal id
// alone (authenticate middleware), before the tenant (their manager) is
// known. Cross-tenant, so it runs on AdminStore.
func (m StaffModel) GetByID(ctx context.Context, id uuid.UUID) (*Staff, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.AdminStore.GetStaff(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	return staffFromRow(row), nil
}

// List returns every staff row under tenantID, oldest first.
func (m StaffModel) List(ctx context.Context, tenantID uuid.UUID) ([]Staff, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []Staff
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListStaffForManager(ctx, tenantID)
		if err != nil {
			return err
		}
		out = make([]Staff, 0, len(rows))
		for _, r := range rows {
			out = append(out, *staffFromRow(r))
		}
		return nil
	})
	return out, err
}

// Update saves a staff row if its Version still matches (an owner's edits:
// role, module_access, name, phone, status). Returns ErrEditConflict or
// ErrDuplicateStaffEmail.
func (m StaffModel) Update(ctx context.Context, tenantID uuid.UUID, s *Staff) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		version, err := q.UpdateStaff(ctx, sqlc.UpdateStaffParams{
			ID: s.ID, Version: s.Version, Name: s.Name, Email: s.Email, Phone: s.Phone,
			PasswordHash: s.Password.hash, Role: s.Role, ModuleAccess: s.ModuleAccess, Status: s.Status,
			PropertyAccess: marshalPropertyAccess(s.PropertyAccess),
		})
		if err != nil {
			return staffWriteError(editConflict(err))
		}
		s.Version = version
		return nil
	})
}

// FindActiveByEmail resolves a login attempt to exactly one staff row.
// Cross-tenant (no tenant context exists at login), so it runs on
// AdminStore; anything but exactly one match — none, or more than one, a
// person invited under two managers with the same email — is
// ErrRecordNotFound: never guess which account a login belongs to.
func (m StaffModel) FindActiveByEmail(ctx context.Context, email string) (*Staff, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.AdminStore.ListActiveStaffByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrRecordNotFound
	}
	return staffFromRow(rows[0]), nil
}

// GetForToken returns the staff member a still-valid token of tokenScope
// belongs to, or ErrRecordNotFound. Cross-tenant, like ManagerModel's
// equivalent — runs on AdminStore.
func (m StaffModel) GetForToken(ctx context.Context, tokenScope, tokenPlaintext string) (*Staff, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.AdminStore.GetStaffForToken(ctx, sqlc.GetStaffForTokenParams{
		Hash: auth.HashToken(tokenPlaintext), Scope: tokenScope,
	})
	if err != nil {
		return nil, notFound(err)
	}
	return staffFromRow(row), nil
}

// Activate sets a staff member's password and flips them to active, from a
// valid invite/reset token — the same cross-tenant, pre-session flow
// GetForToken feeds into, so it also runs on AdminStore.
func (m StaffModel) Activate(ctx context.Context, s *Staff) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	version, err := m.AdminStore.UpdateStaff(ctx, sqlc.UpdateStaffParams{
		ID: s.ID, Version: s.Version, Name: s.Name, Email: s.Email, Phone: s.Phone,
		PasswordHash: s.Password.hash, Role: s.Role, ModuleAccess: s.ModuleAccess, Status: StaffActive,
		PropertyAccess: marshalPropertyAccess(s.PropertyAccess),
	})
	if err != nil {
		return editConflict(err)
	}
	s.Version = version
	s.Status = StaffActive
	return nil
}
