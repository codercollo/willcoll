package auth

// Action identifies one RBAC-gated capability from the role → base
// capability table (spec §2.1). PBAC narrows an Agent's grant of the six
// "if granted" actions per property; that narrowing lives in
// internal/api/middleware_permissions.go against agent_property_grants, not
// here — Authorize only answers the RBAC question of what a role gets by
// default.
type Action string

const (
	ActionViewPortfolio         Action = "view_portfolio"
	ActionRecordPayment         Action = "record_payment"
	ActionRecordMeterReading    Action = "record_meter_reading"
	ActionIssueInvoice          Action = "issue_invoice"
	ActionEditUnitPricing       Action = "edit_unit_pricing"
	ActionEditLease             Action = "edit_lease"
	ActionManageProperty        Action = "manage_property"
	ActionInviteAgent           Action = "invite_agent"
	ActionSetAgentGrant         Action = "set_agent_grant"
	ActionViewLedger            Action = "view_ledger"
	ActionVoidPayment           Action = "void_payment"
	ActionViewFinancialReports  Action = "view_financial_reports"
	ActionConfigureSMSTemplates Action = "configure_sms_templates"
	ActionSystemAdministration  Action = "system_administration"
)

// rolePermissions is the RBAC role -> default capability bundle transcribed
// from the spec §2.1 table. It intentionally omits the Agent rows marked
// "if granted" (edit_unit_pricing, edit_lease, void_payment) and the
// Landlord/Manager-only "(own)" qualifiers — those are per-resource
// narrowings that Authorize, answering only the base-role question, cannot
// express; callers needing them apply PBAC or an ownership check on top.
var rolePermissions = map[string]map[Action]bool{
	"landlord": {
		ActionViewPortfolio:         true,
		ActionManageProperty:        true, // own properties only
		ActionViewLedger:            true, // own properties only
		ActionViewFinancialReports:  true, // own properties only
		ActionConfigureSMSTemplates: true, // own prefs only
	},
	"manager": {
		ActionViewPortfolio:         true,
		ActionRecordPayment:         true,
		ActionRecordMeterReading:    true,
		ActionIssueInvoice:          true,
		ActionEditUnitPricing:       true,
		ActionEditLease:             true,
		ActionManageProperty:        true,
		ActionInviteAgent:           true,
		ActionSetAgentGrant:         true,
		ActionViewLedger:            true,
		ActionVoidPayment:           true,
		ActionViewFinancialReports:  true,
		ActionConfigureSMSTemplates: true,
		ActionSystemAdministration:  true, // super-manager flag gates this further, checked at the handler
	},
	"agent": {
		ActionViewPortfolio:      true, // assigned properties only
		ActionRecordPayment:      true,
		ActionRecordMeterReading: true,
		ActionIssueInvoice:       true,
		ActionViewLedger:         true, // view-only unless granted
	},
}

// Authorize reports whether role's default RBAC bundle includes action (spec
// §2.1, ch.17 pattern: a role -> permission table held as unexported service
// state, not scattered literals). It is the base-role answer only; per-
// property PBAC narrowing for an Agent is a separate, later check.
func (s *Service) Authorize(role string, action Action) bool {
	return s.permissions[role][action]
}
