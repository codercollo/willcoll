package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PayoutAccount is a Manager's payout onboarding record (spec §2.4, §3.1b).
type PayoutAccount struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	ManagerID            uuid.UUID
	BankName             string
	BankBranchCode       string
	AccountName          string
	AccountNumber        string
	KRAPin               string
	BusinessDocType      string
	BusinessDocReference string
	GatewaySubaccountID  string
	KYCStatus            string
}

// SubmitPayoutAccountInput carries a Manager's bank details + KYC docs.
type SubmitPayoutAccountInput struct {
	OrganizationID       uuid.UUID
	ManagerID            uuid.UUID
	BankName             string
	BankBranchCode       string
	AccountName          string
	AccountNumber        string
	KRAPin               string
	BusinessDocType      string // "certificate_of_incorporation" | "national_id"
	BusinessDocReference string
}

var (
	ErrPayoutFieldsRequired  = errors.New("bank_name, account_name, account_number, kra_pin, business_doc_type, and business_doc_reference are required")
	ErrPayoutDocTypeInvalid  = errors.New("business_doc_type must be certificate_of_incorporation or national_id")
	ErrPayoutAccountNotFound = errors.New("no payout account has been submitted yet")
)

func validBusinessDocType(kind string) bool {
	return kind == "certificate_of_incorporation" || kind == "national_id"
}

// SubmitPayoutAccount creates or replaces the calling Manager's payout
// onboarding submission. Resubmitting resets kyc_status to 'pending' and
// clears any prior gateway_subaccount_id — IntaSend needs to re-clear KYC
// against the new details.
//
// IntaSend sub-account provisioning itself (turning these details into a
// gateway_subaccount_id) is not wired up here: that call, and the KYC
// clearance callback that would set kyc_status='verified', both belong to a
// proper internal/admin-authenticated flow (spec §6.1's "Internal/admin-
// triggered" POST /v1/managers/payout-account/verify) that doesn't exist yet
// in this codebase. Until it does, moving a row to 'verified' is an
// operator-run SQL update, not an HTTP endpoint — exposing that as a
// Manager-callable "verify myself" route would let a Manager bypass KYC
// entirely, which defeats the point of KYC.
func (s *Service) SubmitPayoutAccount(ctx context.Context, input SubmitPayoutAccountInput) (PayoutAccount, error) {
	if strings.TrimSpace(input.BankName) == "" ||
		strings.TrimSpace(input.AccountName) == "" ||
		strings.TrimSpace(input.AccountNumber) == "" ||
		strings.TrimSpace(input.KRAPin) == "" ||
		strings.TrimSpace(input.BusinessDocReference) == "" {
		return PayoutAccount{}, ErrPayoutFieldsRequired
	}
	if !validBusinessDocType(input.BusinessDocType) {
		return PayoutAccount{}, ErrPayoutDocTypeInvalid
	}

	var account PayoutAccount
	err := s.pool.QueryRow(ctx, `
		INSERT INTO manager_payout_accounts (
			organization_id, manager_id, bank_name, bank_branch_code, account_name,
			account_number, kra_pin, business_doc_type, business_doc_reference,
			kyc_status, kyc_submitted_at, gateway_subaccount_id, kyc_verified_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::payout_doc_kind, $9, 'pending', now(), NULL, NULL)
		ON CONFLICT (manager_id) DO UPDATE SET
			bank_name = EXCLUDED.bank_name,
			bank_branch_code = EXCLUDED.bank_branch_code,
			account_name = EXCLUDED.account_name,
			account_number = EXCLUDED.account_number,
			kra_pin = EXCLUDED.kra_pin,
			business_doc_type = EXCLUDED.business_doc_type,
			business_doc_reference = EXCLUDED.business_doc_reference,
			kyc_status = 'pending',
			kyc_submitted_at = now(),
			gateway_subaccount_id = NULL,
			kyc_verified_at = NULL,
			kyc_rejection_reason = NULL,
			updated_at = now()
		RETURNING id, organization_id, manager_id, bank_name,
		          COALESCE(bank_branch_code, ''), account_name, account_number, kra_pin,
		          business_doc_type::text, business_doc_reference,
		          COALESCE(gateway_subaccount_id, ''), kyc_status::text`,
		input.OrganizationID, input.ManagerID, input.BankName, nullIfEmpty(input.BankBranchCode),
		input.AccountName, input.AccountNumber, input.KRAPin, input.BusinessDocType, input.BusinessDocReference,
	).Scan(
		&account.ID, &account.OrganizationID, &account.ManagerID, &account.BankName,
		&account.BankBranchCode, &account.AccountName, &account.AccountNumber, &account.KRAPin,
		&account.BusinessDocType, &account.BusinessDocReference,
		&account.GatewaySubaccountID, &account.KYCStatus,
	)
	if err != nil {
		return PayoutAccount{}, fmt.Errorf("submit payout account: %w", err)
	}

	return account, nil
}

// GetPayoutAccount returns the calling Manager's payout onboarding status.
func (s *Service) GetPayoutAccount(ctx context.Context, managerID uuid.UUID) (PayoutAccount, error) {
	var account PayoutAccount
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, manager_id, bank_name, COALESCE(bank_branch_code, ''),
		       account_name, account_number, kra_pin, business_doc_type::text,
		       business_doc_reference, COALESCE(gateway_subaccount_id, ''), kyc_status::text
		FROM manager_payout_accounts
		WHERE manager_id = $1`,
		managerID,
	).Scan(
		&account.ID, &account.OrganizationID, &account.ManagerID, &account.BankName,
		&account.BankBranchCode, &account.AccountName, &account.AccountNumber, &account.KRAPin,
		&account.BusinessDocType, &account.BusinessDocReference,
		&account.GatewaySubaccountID, &account.KYCStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PayoutAccount{}, ErrPayoutAccountNotFound
	}
	if err != nil {
		return PayoutAccount{}, fmt.Errorf("get payout account: %w", err)
	}
	return account, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
