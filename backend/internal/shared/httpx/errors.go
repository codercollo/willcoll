package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Responder centralizes the standard JSON error responses every product's
// API returns. Logger is required; Flash is optional (nil for a product
// with no flash-message mechanism) and is threaded through to WriteJSON.
type Responder struct {
	Logger *slog.Logger
	Flash  func(*gin.Context) string
}

// LogError logs err along with the request method and URL for context.
func (r Responder) LogError(c *gin.Context, err error) {
	r.Logger.Error(err.Error(), "method", c.Request.Method, "uri", c.Request.URL.RequestURI())
}

// WriteJSON is Responder-bound sugar over the package-level WriteJSON,
// threading Flash through automatically.
func (r Responder) WriteJSON(c *gin.Context, status int, data Envelope, headers http.Header) error {
	return WriteJSON(c, status, data, headers, r.Flash)
}

// ErrorResponse writes a JSON error envelope: {"error": message}.
func (r Responder) ErrorResponse(c *gin.Context, status int, message any) {
	if err := r.WriteJSON(c, status, Envelope{"error": message}, nil); err != nil {
		r.LogError(c, err)
		c.Writer.WriteHeader(http.StatusInternalServerError)
	}
}

// CodedErrorResponse writes {"error": message, "code": code}, plus any
// extra envelope fields, so a client can branch on `code` instead of
// matching the human-readable message text.
func (r Responder) CodedErrorResponse(c *gin.Context, status int, message any, code string, extra Envelope) {
	env := Envelope{"error": message, "code": code}
	for k, v := range extra {
		env[k] = v
	}
	if err := r.WriteJSON(c, status, env, nil); err != nil {
		r.LogError(c, err)
		c.Writer.WriteHeader(http.StatusInternalServerError)
	}
}

// ServerErrorResponse logs err and responds 500.
func (r Responder) ServerErrorResponse(c *gin.Context, err error) {
	r.LogError(c, err)
	message := "the server encountered a problem and could not process your request"
	r.ErrorResponse(c, http.StatusInternalServerError, message)
}

// NotFoundResponse responds 404.
func (r Responder) NotFoundResponse(c *gin.Context) {
	message := "the requested resource could not be found"
	r.ErrorResponse(c, http.StatusNotFound, message)
}

// MethodNotAllowedResponse responds 405.
func (r Responder) MethodNotAllowedResponse(c *gin.Context) {
	message := "the " + c.Request.Method + " method is not supported for this resource"
	r.ErrorResponse(c, http.StatusMethodNotAllowed, message)
}

// BadRequestResponse responds 400 with err's message.
func (r Responder) BadRequestResponse(c *gin.Context, err error) {
	r.ErrorResponse(c, http.StatusBadRequest, err.Error())
}

// FailedValidationResponse responds 422 with a map of field -> message.
func (r Responder) FailedValidationResponse(c *gin.Context, errs map[string]string) {
	r.ErrorResponse(c, http.StatusUnprocessableEntity, errs)
}

// EditConflictResponse responds 409, for optimistic-locking / concurrent-edit conflicts.
func (r Responder) EditConflictResponse(c *gin.Context) {
	message := "unable to update the record due to an edit conflict, please try again"
	r.ErrorResponse(c, http.StatusConflict, message)
}

// RateLimitExceededResponse responds 429.
func (r Responder) RateLimitExceededResponse(c *gin.Context) {
	message := "rate limit exceeded"
	r.ErrorResponse(c, http.StatusTooManyRequests, message)
}

// InvalidCredentialsResponse responds 401 for a failed login attempt.
func (r Responder) InvalidCredentialsResponse(c *gin.Context) {
	message := "invalid authentication credentials"
	r.ErrorResponse(c, http.StatusUnauthorized, message)
}

// AuthenticationRequiredResponse responds 401 when no session/credentials are present.
func (r Responder) AuthenticationRequiredResponse(c *gin.Context) {
	message := "you must be authenticated to access this resource"
	r.ErrorResponse(c, http.StatusUnauthorized, message)
}

// NotPermittedResponse responds 403 for an authenticated principal lacking permission.
func (r Responder) NotPermittedResponse(c *gin.Context) {
	message := "your account doesn't have permission to access this resource"
	r.ErrorResponse(c, http.StatusForbidden, message)
}

// InactiveAccountResponse responds 403 for a manager account that hasn't been activated.
func (r Responder) InactiveAccountResponse(c *gin.Context) {
	message := "your account must be activated to access this resource"
	r.ErrorResponse(c, http.StatusForbidden, message)
}

// AccountSuspendedResponse responds 403 for a manager whose account the
// Super Admin has suspended (system-design.txt 1.1).
func (r Responder) AccountSuspendedResponse(c *gin.Context) {
	message := "your account has been suspended"
	r.ErrorResponse(c, http.StatusForbidden, message)
}
