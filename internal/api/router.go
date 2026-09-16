package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// registerRoutes is the single explicit route table. Routes are added here
// only in the subphase that adds their handler.
func (s *Server) registerRoutes() {
	s.router.Handle(http.MethodGet, "/healthz", s.healthz)

	s.router.Handle(http.MethodPost, "/v1/auth/register-manager", s.registerManager)
	s.router.Handle(http.MethodPost, "/v1/auth/login", s.login)
	s.router.Handle(http.MethodPost, "/v1/auth/refresh", s.refresh)

	s.router.Handle(http.MethodPut, "/v1/users/activate", s.activateUser)
	s.router.Handle(http.MethodPut, "/v1/users/password", s.setPassword)

	s.router.Handler(http.MethodPost, "/v1/agents", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.inviteAgent)))))
	s.router.Handler(http.MethodPost, "/v1/agents/:id/reset-password", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.resetAgentPassword)))))

	s.router.Handle(http.MethodGet, "/v1/public/branding", s.publicBranding)
	s.router.Handle(http.MethodPost, "/v1/webhooks/intasend", s.subscriptions.IntasendWebhook)
	s.router.Handler(http.MethodGet, "/v1/organization", s.authenticate(s.tenantScope(wrapHandle(s.getOrganization))))
	s.router.Handler(http.MethodPatch, "/v1/organization", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.patchOrganization)))))
	s.router.Handler(http.MethodPatch, "/v1/organization/branding", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.patchOrganizationBranding)))))

	s.router.Handler(http.MethodGet, "/v1/properties", s.authenticate(s.tenantScope(wrapHandle(s.listProperties))))
	s.router.Handler(http.MethodPost, "/v1/properties", s.authenticate(s.tenantScope(s.requireRole("manager", "landlord")(wrapHandle(s.createProperty)))))
	s.router.Handler(http.MethodGet, "/v1/properties/:id/units", s.authenticate(s.tenantScope(wrapHandle(s.listUnits))))
	s.router.Handler(http.MethodPost, "/v1/properties/:id/units", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.createUnit)))))
	s.router.Handler(http.MethodPost, "/v1/properties/:id/units/import", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.importUnitsCSV)))))
	s.router.Handler(http.MethodPost, "/v1/properties/:id/tenants/import", s.authenticate(s.tenantScope(s.requirePermission(permissionEditLeases, "manager", "agent")(wrapHandle(s.importTenantsCSV)))))

	s.router.Handler(http.MethodPost, "/v1/agent-grants", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.setAgentGrant)))))

	s.router.Handler(http.MethodPost, "/v1/units/:id/leases", s.authenticate(s.tenantScope(s.requirePermission(permissionEditLeases, "manager", "agent")(wrapHandle(s.createLease)))))
	s.router.Handler(http.MethodGet, "/v1/units/:id/statement", s.authenticate(s.tenantScope(s.requirePermission(permissionViewFinancialReports, "manager", "landlord", "agent")(wrapHandle(s.unitStatement)))))
	s.router.Handler(http.MethodPost, "/v1/leases/:id/terminate", s.authenticate(s.tenantScope(s.requirePermission(permissionEditLeases, "manager", "agent")(wrapHandle(s.terminateLease)))))

	s.router.Handler(http.MethodPost, "/v1/payments", s.authenticate(s.tenantScope(s.requireRole("manager", "agent")(wrapHandle(s.createPayment)))))
	s.router.Handler(http.MethodPost, "/v1/payments/:id/reverse", s.authenticate(s.tenantScope(s.requirePermission(permissionVoidPayments, "manager", "agent")(wrapHandle(s.reversePayment)))))
	s.router.Handler(http.MethodPost, "/v1/remittances", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.createRemittance)))))
	s.router.Handler(http.MethodPost, "/v1/meters/:id/readings", s.authenticate(s.tenantScope(s.requirePermission(permissionManageMeterReadings, "manager", "agent")(wrapHandle(s.createMeterReading)))))
	s.router.Handler(http.MethodPost, "/v1/organization/subscription/charge", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.initiateSubscriptionCharge)))))
	s.router.Handler(http.MethodGet, "/v1/organization/subscription", s.authenticate(s.tenantScope(wrapHandle(s.getSubscription))))

	s.router.Handler(http.MethodGet, "/v1/reports/arrears", s.authenticate(s.tenantScope(s.requireRole("manager", "landlord")(wrapHandle(s.listArrears)))))
	s.router.Handler(http.MethodGet, "/v1/reports/collections", s.authenticate(s.tenantScope(s.requireRole("manager", "landlord")(wrapHandle(s.listCollections)))))
	s.router.Handler(http.MethodGet, "/v1/reports/portfolio", s.authenticate(s.tenantScope(s.requireRole("manager", "landlord")(wrapHandle(s.portfolio)))))
	s.router.Handler(http.MethodGet, "/v1/audit-log", s.authenticate(s.tenantScope(s.requireRole("manager")(wrapHandle(s.listAuditLog)))))
}

// wrapHandle adapts an httprouter.Handle into a standard http.Handler so it can
// be wrapped by the hand-rolled middleware chain.
func wrapHandle(h httprouter.Handle) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, httprouter.ParamsFromContext(r.Context()))
	})
}

// routes wraps the router in the cross-cutting middleware chain.
func (s *Server) routes() http.Handler {
	return s.logRequests(s.recoverPanic(s.cors(s.router)))
}
