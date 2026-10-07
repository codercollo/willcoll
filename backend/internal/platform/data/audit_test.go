package data

import (
	"context"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestAuditModel_Log_Staff(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	tenantID := uuid.New()
	staffID := uuid.New()
	entityID := uuid.New()
	store.EXPECT().ExecTenantTx(gomock.Any(), tenantID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) })

	q.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.CreateAuditLogParams) error {
			if a.ManagerID != tenantID {
				t.Errorf("manager_id = %v, want %v", a.ManagerID, tenantID)
			}
			if a.ActorType != ActorStaff || a.ActorID == nil || *a.ActorID != staffID {
				t.Errorf("actor_type/id = %q/%v, want staff/%v", a.ActorType, a.ActorID, staffID)
			}
			if a.StaffID == nil || *a.StaffID != staffID {
				t.Errorf("staff_id = %v, want %v (kept alongside actor_id for a staff actor)", a.StaffID, staffID)
			}
			if a.Product == nil || *a.Product != "rentals" {
				t.Errorf("product = %v, want rentals", a.Product)
			}
			return nil
		})

	m := AuditModel{Store: store}
	err := m.Log(context.Background(), tenantID, AuditEntry{
		ActorType: ActorStaff, ActorID: &staffID, Product: "rentals", Action: "payment.confirmed",
		EntityType: "payment", EntityID: &entityID, Summary: "confirmed",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
}

// TestAuditModel_Log_ManagerActorNeverBlank pins down build-phases.txt
// 3f-4's fix: the manager's own action reads as actor_type "manager" with
// their id, never blank.
func TestAuditModel_Log_ManagerActorNeverBlank(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	tenantID := uuid.New()
	store.EXPECT().ExecTenantTx(gomock.Any(), tenantID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) })

	q.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.CreateAuditLogParams) error {
			if a.ActorType != ActorManager || a.ActorID == nil || *a.ActorID != tenantID {
				t.Errorf("actor_type/id = %q/%v, want manager/%v (never blank)", a.ActorType, a.ActorID, tenantID)
			}
			if a.StaffID != nil {
				t.Errorf("staff_id = %v, want nil for the manager's own login", *a.StaffID)
			}
			if a.Product != nil {
				t.Errorf("product = %v, want nil for a platform-only action", *a.Product)
			}
			return nil
		})

	m := AuditModel{Store: store}
	err := m.Log(context.Background(), tenantID, AuditEntry{
		ActorType: ActorManager, ActorID: &tenantID, Action: "staff.invited", EntityType: "staff", Summary: "x",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
}

func TestAuditModel_Log_AdminActor(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	tenantID := uuid.New()
	adminID := uuid.New()
	store.EXPECT().ExecTenantTx(gomock.Any(), tenantID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) })

	q.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.CreateAuditLogParams) error {
			if a.ActorType != ActorAdmin || a.ActorID == nil || *a.ActorID != adminID {
				t.Errorf("actor_type/id = %q/%v, want admin/%v", a.ActorType, a.ActorID, adminID)
			}
			if a.StaffID != nil {
				t.Errorf("staff_id = %v, want nil for an admin actor", *a.StaffID)
			}
			return nil
		})

	m := AuditModel{Store: store}
	err := m.Log(context.Background(), tenantID, AuditEntry{
		ActorType: ActorAdmin, ActorID: &adminID, Action: "manager.suspended", EntityType: "manager", Summary: "suspended",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
}
