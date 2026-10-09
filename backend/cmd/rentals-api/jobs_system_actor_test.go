package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformmock "github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/mock"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// C3.4: a background job acts as an explicit system actor, and sets tenant
// context per firm. The tenant handed to every tenant transaction is recorded
// next to the row it wrote, so firm A's work can be told apart from firm B's.

type tenantWrite struct {
	tenant uuid.UUID // the tenant the transaction was opened for
	what   string
	row    any
}

func TestUsageRecountJobIsASystemActorScopedPerFirm(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	pStore := platformmock.NewMockStore(ctrl)

	firmA, firmB := uuid.New(), uuid.New()
	counts := map[uuid.UUID]int32{firmA: 7, firmB: 31}

	var mu sync.Mutex
	var writes []tenantWrite
	record := func(tenant uuid.UUID, what string, row any) {
		mu.Lock()
		defer mu.Unlock()
		writes = append(writes, tenantWrite{tenant, what, row})
	}

	store.EXPECT().AdminListTenantIDs(gomock.Any()).Return([]uuid.UUID{firmA, firmB}, nil)
	// Each transaction hands its fn a querier bound to the tenant it was
	// opened for (as SET LOCAL app.tenant_id does), so a query can only ever
	// see, and a write only ever land in, that firm.
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tenant uuid.UUID, fn func(sqlc.Querier) error) error {
			q := mock.NewMockQuerier(ctrl)
			q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, asked uuid.UUID) (int32, error) {
					if asked != tenant {
						t.Errorf("counted units for %s inside the transaction opened for %s", asked, tenant)
					}
					return counts[asked], nil
				}).AnyTimes()
			return fn(q)
		}).AnyTimes()

	pStore.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tenant uuid.UUID, fn func(platformsqlc.Querier) error) error {
			pq := platformmock.NewMockQuerier(ctrl)
			pq.EXPECT().UpsertModuleUsage(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, a platformsqlc.UpsertModuleUsageParams) (platformsqlc.ModuleUsage, error) {
					record(tenant, "usage", a)
					return platformsqlc.ModuleUsage{ManagerID: a.ManagerID, Product: a.Product, BillableCount: a.BillableCount}, nil
				}).AnyTimes()
			pq.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, a platformsqlc.CreateAuditLogParams) error {
					record(tenant, "audit", a)
					return nil
				}).AnyTimes()
			return fn(pq)
		}).AnyTimes()

	platformModels := platformdata.NewModelsFromStore(pStore, 0)
	app := &application{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:   data.NewModelsFromStore(store, 0),
		platform: &platformModels,
	}

	app.recountAllManagers(context.Background())
	app.wg.Wait()

	usageByFirm := map[uuid.UUID]int32{}
	auditByFirm := map[uuid.UUID]int{}
	for _, w := range writes {
		switch row := w.row.(type) {
		case platformsqlc.UpsertModuleUsageParams:
			if row.ManagerID != w.tenant {
				t.Errorf("usage for %s was written inside the transaction opened for %s", row.ManagerID, w.tenant)
			}
			usageByFirm[row.ManagerID] = row.BillableCount
		case platformsqlc.CreateAuditLogParams:
			if row.ManagerID != w.tenant {
				t.Errorf("audit row for %s was written inside the transaction opened for %s", row.ManagerID, w.tenant)
			}
			if row.ActorType != platformdata.ActorSystem || row.ActorName == nil || *row.ActorName != jobUsageRecount {
				t.Errorf("audit actor = %q/%v, want system/%q", row.ActorType, row.ActorName, jobUsageRecount)
			}
			if row.ActorID != nil || row.StaffID != nil {
				t.Errorf("a system row carries a principal id: actor %v staff %v", row.ActorID, row.StaffID)
			}
			if row.Ip != nil {
				t.Errorf("a system row carries an IP: %v", *row.Ip)
			}
			auditByFirm[row.ManagerID]++
		}
	}
	for firm, want := range counts {
		if usageByFirm[firm] != want {
			t.Errorf("firm %s usage = %d, want %d (no carry-over from the other firm)", firm, usageByFirm[firm], want)
		}
		if auditByFirm[firm] != 1 {
			t.Errorf("firm %s audit rows = %d, want 1", firm, auditByFirm[firm])
		}
	}
}

// A system audit entry without a job name is refused by the model: nobody can
// write an anonymous system row.
func TestSystemAuditEntryMustNameItsJob(t *testing.T) {
	ctrl := gomock.NewController(t)
	pStore := platformmock.NewMockStore(ctrl)
	models := platformdata.NewModelsFromStore(pStore, 0)
	err := models.Audit.Log(context.Background(), uuid.New(), platformdata.AuditEntry{ActorType: platformdata.ActorSystem, Action: "x", EntityType: "y"})
	if err == nil {
		t.Fatal("expected an error for a system actor with no job name")
	}
}
