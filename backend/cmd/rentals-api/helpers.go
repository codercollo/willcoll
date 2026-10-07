package main

import (
	"net/http"
	"net/url"

	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// envelope wraps every JSON response body, e.g. envelope{"property": p}.
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

// readUUID reads an optional UUID query-string value. It returns nil when
// the key is absent, and records a validation error on v when the value
// isn't a UUID.
func (app *application) readUUID(qs url.Values, key string, v *validator.Validator) *uuid.UUID {
	return httpx.ReadUUID(qs, key, v)
}

// readInt parses the value for key in qs as an int, or returns
// defaultValue if key isn't present, recording a validation error on v if
// the value is present but not a valid integer.
func (app *application) readInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
	return httpx.ReadInt(qs, key, defaultValue, v)
}

// background runs fn in a goroutine tracked by application.wg, recovering
// any panic so a failure in a background job (email, SMS, PDF render) never
// crashes the process.
func (app *application) background(fn func()) {
	app.wg.Logger = app.logger
	app.wg.Run(fn)
}
