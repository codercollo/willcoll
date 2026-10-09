package main

import (
	"errors"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
)

// logError logs err along with the request method and URL for context.
func (app *application) logError(c *gin.Context, err error) {
	app.responder().LogError(c, err)
}

// errorResponse writes a JSON error envelope: {"error": message}.
func (app *application) errorResponse(c *gin.Context, status int, message any) {
	app.responder().ErrorResponse(c, status, message)
}

// codedErrorResponse writes {"error": message, "code": code}, plus any
// extra envelope fields, so a client can branch on `code` instead of
// matching the human-readable message text.
func (app *application) codedErrorResponse(c *gin.Context, status int, message any, code string, extra envelope) {
	app.responder().CodedErrorResponse(c, status, message, code, extra)
}

// serverErrorResponse logs err and responds 500.
//
// A write the database refused because the row lies outside the caller's
// property scope (auth.ErrOutOfScope) is answered 404, exactly as a row that
// does not exist, so scope never reveals what exists beyond it.
func (app *application) serverErrorResponse(c *gin.Context, err error) {
	if errors.Is(err, auth.ErrOutOfScope) {
		httpx.MarkScopeDenied(c)
		app.responder().NotFoundResponse(c)
		return
	}
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
// Super Admin has suspended (system-design.txt 1.1).
func (app *application) accountSuspendedResponse(c *gin.Context) {
	app.responder().AccountSuspendedResponse(c)
}
