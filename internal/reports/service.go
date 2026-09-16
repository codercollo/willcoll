package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Service owns landlord-scoped financial report reads.
type Service struct {
	pool *pgxpool.Pool
}

// NewService returns a reports service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Page struct {
	Number int `json:"page"`
	Size   int `json:"page_size"`
}

type Metadata struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	FirstPage  int `json:"first_page"`
	LastPage   int `json:"last_page"`
	TotalCount int `json:"total_count"`
}

type ListResult[T any] struct {
	Data     []T      `json:"data"`
	Metadata Metadata `json:"metadata"`
}

type ArrearsFilters struct {
	PropertyID *uuid.UUID
	Role       string
	UserID     uuid.UUID
	Sort       string
	Page       Page
}

type ArrearsRow struct {
	UnitID     uuid.UUID       `json:"unit_id"`
	PropertyID uuid.UUID       `json:"property_id"`
	UnitLabel  string          `json:"unit_label"`
	Arrears    decimal.Decimal `json:"arrears"`
}

type CollectionsFilters struct {
	PropertyID *uuid.UUID
	Month      time.Time
	Role       string
	UserID     uuid.UUID
	Sort       string
	Page       Page
}

type CollectionsRow struct {
	PropertyID uuid.UUID       `json:"property_id"`
	Collected  decimal.Decimal `json:"collected"`
}

type PortfolioFilters struct {
	Search string
	Role   string
	UserID uuid.UUID
	Sort   string
	Page   Page
}

type PortfolioRow struct {
	PropertyID uuid.UUID `json:"property_id"`
	Name       string    `json:"name"`
	Units      int       `json:"units"`
}

type UnitStatementFilters struct {
	UnitID uuid.UUID
	Role   string
	UserID uuid.UUID
	Page   Page
}

type UnitStatementRow struct {
	UnitID      uuid.UUID       `json:"unit_id"`
	PeriodMonth time.Time       `json:"period_month"`
	RentDue     decimal.Decimal `json:"rent_due"`
	RentPaid    decimal.Decimal `json:"rent_paid"`
	WaterDue    decimal.Decimal `json:"water_due"`
	WaterPaid   decimal.Decimal `json:"water_paid"`
}

func (s *Service) ListArrears(ctx context.Context, tx pgx.Tx, filters ArrearsFilters) (ListResult[ArrearsRow], error) {
	params := db.ListArrearsReportParams{
		Column1: uuidToPgtype(filters.PropertyID),
		Column2: filters.PropertyID != nil,
		Column3: filters.Role,
		Column4: uuidToPgtype(&filters.UserID),
		Column5: filters.Sort,
		Limit:   int32(filters.Page.Size),
		Offset:  int32(offset(filters.Page)),
	}

	rows, err := db.New(tx).ListArrearsReport(ctx, params)
	if err != nil {
		return ListResult[ArrearsRow]{}, fmt.Errorf("list arrears report: %w", err)
	}

	out := make([]ArrearsRow, 0, len(rows))
	total := int64(0)
	for _, row := range rows {
		total = row.TotalCount
		out = append(out, ArrearsRow{
			UnitID:     pgtypeToUUID(row.UnitID),
			PropertyID: pgtypeToUUID(row.PropertyID),
			UnitLabel:  row.UnitLabel,
			Arrears:    numericToDecimal(row.Arrears),
		})
	}

	return ListResult[ArrearsRow]{Data: out, Metadata: calculateMetadata(int(total), filters.Page)}, nil
}

func (s *Service) ListCollections(ctx context.Context, tx pgx.Tx, filters CollectionsFilters) (ListResult[CollectionsRow], error) {
	params := db.ListCollectionsReportParams{
		Column1: pgtype.Date{Time: filters.Month, Valid: true},
		Column2: uuidToPgtype(filters.PropertyID),
		Column3: filters.PropertyID != nil,
		Column4: filters.Role,
		Column5: uuidToPgtype(&filters.UserID),
		Column6: filters.Sort,
		Limit:   int32(filters.Page.Size),
		Offset:  int32(offset(filters.Page)),
	}

	rows, err := db.New(tx).ListCollectionsReport(ctx, params)
	if err != nil {
		return ListResult[CollectionsRow]{}, fmt.Errorf("list collections report: %w", err)
	}

	out := make([]CollectionsRow, 0, len(rows))
	total := int64(0)
	for _, row := range rows {
		total = row.TotalCount
		out = append(out, CollectionsRow{
			PropertyID: pgtypeToUUID(row.PropertyID),
			Collected:  numericToDecimal(row.Collected),
		})
	}

	return ListResult[CollectionsRow]{Data: out, Metadata: calculateMetadata(int(total), filters.Page)}, nil
}

func (s *Service) ListPortfolio(ctx context.Context, tx pgx.Tx, filters PortfolioFilters) (ListResult[PortfolioRow], error) {
	params := db.ListPortfolioReportParams{
		Column1: filters.Role,
		Column2: uuidToPgtype(&filters.UserID),
		Column3: filters.Search,
		Column4: filters.Sort,
		Limit:   int32(filters.Page.Size),
		Offset:  int32(offset(filters.Page)),
	}

	rows, err := db.New(tx).ListPortfolioReport(ctx, params)
	if err != nil {
		return ListResult[PortfolioRow]{}, fmt.Errorf("list portfolio report: %w", err)
	}

	out := make([]PortfolioRow, 0, len(rows))
	total := int64(0)
	for _, row := range rows {
		total = row.TotalCount
		out = append(out, PortfolioRow{
			PropertyID: pgtypeToUUID(row.PropertyID),
			Name:       row.Name,
			Units:      int(row.Units),
		})
	}

	return ListResult[PortfolioRow]{Data: out, Metadata: calculateMetadata(int(total), filters.Page)}, nil
}

func (s *Service) ListUnitStatement(ctx context.Context, tx pgx.Tx, filters UnitStatementFilters) (ListResult[UnitStatementRow], error) {
	params := db.ListUnitStatementParams{
		UnitID:  uuidToPgtype(&filters.UnitID),
		Column2: filters.Role,
		Column3: uuidToPgtype(&filters.UserID),
		Limit:   int32(filters.Page.Size),
		Offset:  int32(offset(filters.Page)),
	}

	rows, err := db.New(tx).ListUnitStatement(ctx, params)
	if err != nil {
		return ListResult[UnitStatementRow]{}, fmt.Errorf("list unit statement: %w", err)
	}

	out := make([]UnitStatementRow, 0, len(rows))
	total := int64(0)
	for _, row := range rows {
		total = row.TotalCount
		out = append(out, UnitStatementRow{
			UnitID:      pgtypeToUUID(row.UnitID),
			PeriodMonth: row.PeriodMonth.Time,
			RentDue:     numericToDecimal(row.RentDue),
			RentPaid:    numericToDecimal(row.RentPaid),
			WaterDue:    numericToDecimal(row.WaterDue),
			WaterPaid:   numericToDecimal(row.WaterPaid),
		})
	}

	return ListResult[UnitStatementRow]{Data: out, Metadata: calculateMetadata(int(total), filters.Page)}, nil
}

func offset(page Page) int {
	return (page.Number - 1) * page.Size
}

func calculateMetadata(total int, page Page) Metadata {
	lastPage := 0
	if total > 0 {
		lastPage = (total + page.Size - 1) / page.Size
	}
	return Metadata{
		Page:       page.Number,
		PageSize:   page.Size,
		FirstPage:  1,
		LastPage:   lastPage,
		TotalCount: total,
	}
}

func uuidToPgtype(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func pgtypeToUUID(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.Nil
	}
	return uuid.UUID(id.Bytes)
}

func numericToDecimal(value pgtype.Numeric) decimal.Decimal {
	dec, err := decimal.NewFromString(value.Int.String())
	if err != nil {
		return decimal.Zero
	}
	if value.Exp < 0 {
		return dec.Shift(int32(value.Exp))
	}
	if value.Exp > 0 {
		return dec.Shift(int32(value.Exp))
	}
	return dec
}
