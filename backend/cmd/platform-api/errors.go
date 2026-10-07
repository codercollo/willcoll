package main

import (
	"github.com/gin-gonic/gin"
)

// errorResponse writes a JSON error envelope: {"error": message}.
func (app *application) errorResponse(c *gin.Context, status int, message any) {
	app.responder().ErrorResponse(c, status, message)
}

// serverErrorResponse logs err and responds 500.
func (app *application) serverErrorResponse(c *gin.Context, err error) {
	app.responder().ServerErrorResponse(c, err)
}

// notFoundResponse responds 404.
func (app *application) notFoundResponse(c *gin.Context) {
	app.responder().NotFoundResponse(c)
}

// methodNotAllowedResponse responds 405.
func (app *application) methodNotAllowedResponse(c *gin.Context) {
	app.responder().MethodNotAllowedResponse(c)
}

// badRequestResponse responds 400 with err's message.
func (app *application) badRequestResponse(c *gin.Context, err error) {
	app.responder().BadRequestResponse(c, err)
}

// failedValidationResponse responds 422 with a map of field -> message.
func (app *application) failedValidationResponse(c *gin.Context, errs map[string]string) {
	app.responder().FailedValidationResponse(c, errs)
}

// editConflictResponse responds 409, for optimistic-locking / concurrent-edit conflicts.
func (app *application) editConflictResponse(c *gin.Context) {
	app.responder().EditConflictResponse(c)
}

// rateLimitExceededResponse responds 429.
func (app *application) rateLimitExceededResponse(c *gin.Context) {
	app.responder().RateLimitExceededResponse(c)
}

// invalidCredentialsResponse responds 401 for a failed login attempt.
func (app *application) invalidCredentialsResponse(c *gin.Context) {
	app.responder().InvalidCredentialsResponse(c)
}

// authenticationRequiredResponse responds 401 when no session/credentials are present.
func (app *application) authenticationRequiredResponse(c *gin.Context) {
	app.responder().AuthenticationRequiredResponse(c)
}

// notPermittedResponse responds 403 for an authenticated principal lacking permission.
func (app *application) notPermittedResponse(c *gin.Context) {
	app.responder().NotPermittedResponse(c)
}

// inactiveAccountResponse responds 403 for a manager account that hasn't been activated.
func (app *application) inactiveAccountResponse(c *gin.Context) {
	app.responder().InactiveAccountResponse(c)
}

// accountSuspendedResponse responds 403 for a manager whose account the
// Super Admin has suspended (system-design.txt A.1).
func (app *application) accountSuspendedResponse(c *gin.Context) {
	app.responder().AccountSuspendedResponse(c)
}
