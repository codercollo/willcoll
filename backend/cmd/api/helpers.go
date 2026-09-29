package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// envelope wraps every JSON response body, e.g. envelope{"property": p}.
type envelope map[string]any

// maxBytesLimit caps request body size read by readJSON.
const maxBytesLimit = 1_048_576 // 1MB

// writeJSON marshals data as indented JSON, applies any extra headers, and
// writes it to the response with the given status code. A pending flash
// message in the session is delivered once, as a top-level "flash" key.
func (app *application) writeJSON(c *gin.Context, status int, data envelope, headers http.Header) error {
	if flash := app.popFlash(c); flash != "" {
		if _, taken := data["flash"]; !taken {
			data["flash"] = flash
		}
	}

	js, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return err
	}

	js = append(js, '\n')

	for key, values := range headers {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(status)
	_, err = c.Writer.Write(js)
	return err
}

// readJSON decodes a single JSON value from the request body into dst,
// rejecting unknown fields and bodies larger than maxBytesLimit, and
// translating decode failures into human-readable errors.
func (app *application) readJSON(c *gin.Context, dst any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytesLimit)

	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		var maxBytesError *http.MaxBytesError

		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("body contains badly-formed JSON (at character %d)", syntaxError.Offset)

		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly-formed JSON")

		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)
			}
			return fmt.Errorf("body contains incorrect JSON type (at character %d)", unmarshalTypeError.Offset)

		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return fmt.Errorf("body contains unknown key %s", fieldName)

		case errors.As(err, &maxBytesError):
			return fmt.Errorf("body must not be larger than %d bytes", maxBytesError.Limit)

		case errors.As(err, &invalidUnmarshalError):
			panic(err)

		default:
			return err
		}
	}

	// Call Decode a second time to confirm the body contained only a single
	// JSON value.
	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return errors.New("body must only contain a single JSON value")
	}

	return nil
}

// readIDParam parses the ":id" route parameter as a UUID.
func (app *application) readIDParam(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, errors.New("invalid id parameter")
	}
	return id, nil
}

// readString returns the string value for key in qs, or defaultValue if
// key isn't present.
func (app *application) readString(qs url.Values, key string, defaultValue string) string {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}
	return s
}

// readUUID reads an optional UUID query-string value. It returns nil when
// the key is absent, and records a validation error on v when the value
// isn't a UUID.
func (app *application) readUUID(qs url.Values, key string, v *validator.Validator) *uuid.UUID {
	s := qs.Get(key)
	if s == "" {
		return nil
	}

	id, err := uuid.Parse(s)
	if err != nil {
		v.AddError(key, "must be a valid UUID")
		return nil
	}
	return &id
}

// readInt parses the value for key in qs as an int, or returns
// defaultValue if key isn't present, recording a validation error on v if
// the value is present but not a valid integer.
func (app *application) readInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}

	i, err := strconv.Atoi(s)
	if err != nil {
		v.AddError(key, "must be an integer value")
		return defaultValue
	}

	return i
}

// background runs fn in a goroutine tracked by application.wg, recovering
// any panic so a failure in a background job (email, SMS, PDF render) never
// crashes the process.
func (app *application) background(fn func()) {
	app.wg.Add(1)

	go func() {
		defer app.wg.Done()

		defer func() {
			if err := recover(); err != nil {
				app.logger.Error(fmt.Sprintf("%v", err))
			}
		}()

		fn()
	}()
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
	return nil
}

// logoutPrincipal destroys the session outright: the row is deleted from
// the session store and the cookie cleared, so it can't be replayed.
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
