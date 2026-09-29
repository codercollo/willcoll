package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// logError logs err along with the request method and URL for context.
func (app *application) logError(c *gin.Context, err error) {
	app.logger.Error(err.Error(), "method", c.Request.Method, "uri", c.Request.URL.RequestURI())
}

// errorResponse writes a JSON error envelope: {"error": message}.
func (app *application) errorResponse(c *gin.Context, status int, message any) {
	err := app.writeJSON(c, status, envelope{"error": message}, nil)
	if err != nil {
		app.logError(c, err)
		c.Writer.WriteHeader(http.StatusInternalServerError)
	}
}

// codedErrorResponse writes {"error": message, "code": code}, plus any
// extra envelope fields, so a client can branch on `code` instead of
// matching the human-readable message text.
func (app *application) codedErrorResponse(c *gin.Context, status int, message any, code string, extra envelope) {
	env := envelope{"error": message, "code": code}
	for k, v := range extra {
		env[k] = v
	}
	if err := app.writeJSON(c, status, env, nil); err != nil {
		app.logError(c, err)
		c.Writer.WriteHeader(http.StatusInternalServerError)
	}
}

// serverErrorResponse logs err and responds 500.
func (app *application) serverErrorResponse(c *gin.Context, err error) {
	app.logError(c, err)
	message := "the server encountered a problem and could not process your request"
	app.errorResponse(c, http.StatusInternalServerError, message)
}

// notFoundResponse responds 404.
func (app *application) notFoundResponse(c *gin.Context) {
	message := "the requested resource could not be found"
	app.errorResponse(c, http.StatusNotFound, message)
}

// methodNotAllowedResponse responds 405.
func (app *application) methodNotAllowedResponse(c *gin.Context) {
	message := "the " + c.Request.Method + " method is not supported for this resource"
	app.errorResponse(c, http.StatusMethodNotAllowed, message)
}

// badRequestResponse responds 400 with err's message.
func (app *application) badRequestResponse(c *gin.Context, err error) {
	app.errorResponse(c, http.StatusBadRequest, err.Error())
}

// failedValidationResponse responds 422 with a map of field -> message.
func (app *application) failedValidationResponse(c *gin.Context, errs map[string]string) {
	app.errorResponse(c, http.StatusUnprocessableEntity, errs)
}

// editConflictResponse responds 409, for optimistic-locking / concurrent-edit conflicts.
func (app *application) editConflictResponse(c *gin.Context) {
	message := "unable to update the record due to an edit conflict, please try again"
	app.errorResponse(c, http.StatusConflict, message)
}

// rateLimitExceededResponse responds 429.
func (app *application) rateLimitExceededResponse(c *gin.Context) {
	message := "rate limit exceeded"
	app.errorResponse(c, http.StatusTooManyRequests, message)
}

// invalidCredentialsResponse responds 401 for a failed login attempt.
func (app *application) invalidCredentialsResponse(c *gin.Context) {
	message := "invalid authentication credentials"
	app.errorResponse(c, http.StatusUnauthorized, message)
}

// authenticationRequiredResponse responds 401 when no session/credentials are present.
func (app *application) authenticationRequiredResponse(c *gin.Context) {
	message := "you must be authenticated to access this resource"
	app.errorResponse(c, http.StatusUnauthorized, message)
}

// notPermittedResponse responds 403 for an authenticated principal lacking permission.
func (app *application) notPermittedResponse(c *gin.Context) {
	message := "your account doesn't have permission to access this resource"
	app.errorResponse(c, http.StatusForbidden, message)
}

// inactiveAccountResponse responds 403 for a manager account that hasn't been activated.
func (app *application) inactiveAccountResponse(c *gin.Context) {
	message := "your account must be activated to access this resource"
	app.errorResponse(c, http.StatusForbidden, message)
}

// accountSuspendedResponse responds 403 for a manager whose account the
// Super Admin has suspended (system-design.txt 1.1).
func (app *application) accountSuspendedResponse(c *gin.Context) {
	message := "your account has been suspended"
	app.errorResponse(c, http.StatusForbidden, message)
}
