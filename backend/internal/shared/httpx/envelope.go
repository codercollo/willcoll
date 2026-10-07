// Package httpx holds the product-agnostic HTTP layer every product's API
// binary builds on: the JSON envelope, request/response helpers, the
// standard error responders, and the middleware that has no knowledge of
// any product's domain (panic recovery, request logging, metrics, CORS,
// rate limiting). Anything that needs a manager, a tenant, or a session
// principal stays in the product's own cmd/<product>-api package.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Envelope wraps every JSON response body, e.g. Envelope{"property": p}.
type Envelope map[string]any

// MaxBytesLimit caps request body size read by ReadJSON.
const MaxBytesLimit = 1_048_576 // 1MB

// WriteJSON marshals data as indented JSON, applies any extra headers, and
// writes it to the response with the given status code. If flash is
// non-nil, its return value is delivered once as a top-level "flash" key —
// products with no flash-message mechanism pass nil.
func WriteJSON(c *gin.Context, status int, data Envelope, headers http.Header, flash func(*gin.Context) string) error {
	if flash != nil {
		if msg := flash(c); msg != "" {
			if _, taken := data["flash"]; !taken {
				data["flash"] = msg
			}
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

// ReadJSON decodes a single JSON value from the request body into dst,
// rejecting unknown fields and bodies larger than MaxBytesLimit, and
// translating decode failures into human-readable errors.
func ReadJSON(c *gin.Context, dst any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBytesLimit)

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

// ReadIDParam parses the ":id" route parameter as a UUID.
func ReadIDParam(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, errors.New("invalid id parameter")
	}
	return id, nil
}

// ReadString returns the string value for key in qs, or defaultValue if
// key isn't present.
func ReadString(qs url.Values, key string, defaultValue string) string {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}
	return s
}

// ReadUUID reads an optional UUID query-string value. It returns nil when
// the key is absent, and records a validation error on v when the value
// isn't a UUID.
func ReadUUID(qs url.Values, key string, v *validator.Validator) *uuid.UUID {
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

// ReadInt parses the value for key in qs as an int, or returns
// defaultValue if key isn't present, recording a validation error on v if
// the value is present but not a valid integer.
func ReadInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
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
