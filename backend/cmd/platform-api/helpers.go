package main

import (
	"net/http"
	"net/url"

	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// envelope wraps every JSON response body, e.g. envelope{"manager": m}.
type envelope = httpx.Envelope

// writeJSON marshals data as indented JSON, applies any extra headers, and
// writes it to the response with the given status code. A pending flash
// message in the session is delivered once, as a top-level "flash" key.
func (app *application) writeJSON(c *gin.Context, status int, data envelope, headers http.Header) error {
	return app.responder().WriteJSON(c, status, data, headers)
}

// readJSON decodes a single JSON value from the request body into dst,
// rejecting unknown fields and bodies larger than httpx.MaxBytesLimit, and
// translating decode failures into human-readable errors.
func (app *application) readJSON(c *gin.Context, dst any) error {
	return httpx.ReadJSON(c, dst)
}

// readIDParam parses the ":id" route parameter as a UUID.
func (app *application) readIDParam(c *gin.Context) (uuid.UUID, error) {
	return httpx.ReadIDParam(c)
}

// readString returns the string value for key in qs, or defaultValue if
// key isn't present.
func (app *application) readString(qs url.Values, key string, defaultValue string) string {
	return httpx.ReadString(qs, key, defaultValue)
}

// readInt parses the value for key in qs as an int, or returns
// defaultValue if key isn't present, recording a validation error on v if
// the value is present but not a valid integer.
func (app *application) readInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
	return httpx.ReadInt(qs, key, defaultValue, v)
}

// background runs fn in a goroutine tracked by application.wg, recovering
// any panic so a failure in a background job (email) never crashes the
// process.
func (app *application) background(fn func()) {
	app.wg.Run(app.logger, fn)
}

// sessionLoaded reports whether loadAndSaveSession ran for this request.
// scs panics when asked for session data that was never loaded, so every
// session helper checks this first.
func (app *application) sessionLoaded(c *gin.Context) bool {
	return app.sessionManager != nil && c.GetBool(sessionLoadedContextKey)
}

// putFlash stores a one-time message in the session. It's returned with
// the next JSON response (see writeJSON) and then discarded, e.g. "Your
// account has been activated" after the activation redirect.
func (app *application) putFlash(c *gin.Context, message string) {
	if app.sessionLoaded(c) {
		app.sessionManager.Put(c.Request.Context(), sessionKeyFlash, message)
		c.Set(flashPutContextKey, true)
	}
}

// popFlash returns and removes the session's pending flash message, or "".
// A request that has just set a flash doesn't consume it: it's for the
// next response.
func (app *application) popFlash(c *gin.Context) string {
	if !app.sessionLoaded(c) || c.GetBool(flashPutContextKey) {
		return ""
	}
	return app.sessionManager.PopString(c.Request.Context(), sessionKeyFlash)
}

// loginPrincipal signs a manager or admin in. The session token is renewed
// first, so a token planted before login (session fixation) is worthless.
func (app *application) loginPrincipal(c *gin.Context, principalType string, id uuid.UUID) error {
	ctx := c.Request.Context()
	if err := app.sessionManager.RenewToken(ctx); err != nil {
		return err
	}
	app.sessionManager.Put(ctx, sessionKeyPrincipalType, principalType)
	app.sessionManager.Put(ctx, sessionKeyPrincipalID, id.String())
	// created_at anchors the absolute cap; renewals never move it.
	app.sessionPolicy.Stamp(app.sessionManager, ctx)
	return nil
}

// logoutPrincipal destroys the session outright: the row is deleted from
// the session store and the cookie cleared, so it can't be replayed —
// this is what signs a manager out of EVERY product at once
// (system-design.txt A.4, ADR-0007), since the session lives in
// willcoll_platform and every product API reads it from there.
func (app *application) logoutPrincipal(c *gin.Context) error {
	return app.sessionManager.Destroy(c.Request.Context())
}

// clearPrincipal forgets a session principal that no longer resolves (e.g.
// the manager was deleted) while keeping the rest of the session.
func (app *application) clearPrincipal(c *gin.Context) {
	ctx := c.Request.Context()
	app.sessionManager.Remove(ctx, sessionKeyPrincipalType)
	app.sessionManager.Remove(ctx, sessionKeyPrincipalID)
}
