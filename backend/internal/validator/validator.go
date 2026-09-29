// Package validator provides a hand-rolled validation helper (the
// Greenlight pattern) for business-rule checks that go-playground/validator
// struct tags can't express — e.g. "current_reading must be >= previous_reading"
// (system-design.txt section 4.3). Mechanical checks (required, numeric,
// e164 phone) stay on struct tags; business rules live here.
package validator

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	playground "github.com/go-playground/validator/v10"
)

var (
	EmailRX = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+\/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

	// PhoneRX matches E.164 format, e.g. +254712345678.
	PhoneRX = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)

	// SlugRX matches a URL-safe slug, e.g. "runda-arcade" — lowercase
	// letters and digits in hyphen-separated groups. Property slugs appear
	// in the public /pay/{property_slug}/{unit_code} URL.
	SlugRX = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Validator collects named validation failures for a single request.
type Validator struct {
	Errors map[string]string
}

// New returns a usable Validator with an empty Errors map.
func New() *Validator {
	return &Validator{Errors: make(map[string]string)}
}

// Valid reports whether no errors have been recorded.
func (v *Validator) Valid() bool {
	return len(v.Errors) == 0
}

// AddError records an error for key if one isn't already present.
func (v *Validator) AddError(key, message string) {
	if _, exists := v.Errors[key]; !exists {
		v.Errors[key] = message
	}
}

// Check adds an error for key if ok is false.
func (v *Validator) Check(ok bool, key, message string) {
	if !ok {
		v.AddError(key, message)
	}
}

// Struct runs the go-playground/validator struct tags on s (a request DTO)
// and records each failure under the field's JSON name. Struct tags cover
// the mechanical checks (required, length, phone/email/slug format, enum
// membership); domain rules stay as explicit Check calls. Registered
// custom tags:
//
//	phone — E.164, via PhoneRX (one definition shared with Check calls)
//	slug  — URL-safe slug, via SlugRX
//
// For optional PATCH fields use pointers with `omitnil`, so the remaining
// tags only run when the client actually sent the field.
func (v *Validator) Struct(s any) {
	err := structValidator.Struct(s)
	if err == nil {
		return
	}

	var fieldErrors playground.ValidationErrors
	if !errors.As(err, &fieldErrors) {
		// InvalidValidationError: s isn't a struct. That's a programming
		// error in the handler, not bad client input.
		panic(err)
	}

	for _, fe := range fieldErrors {
		v.AddError(fe.Field(), fieldErrorMessage(fe))
	}
}

var structValidator = newStructValidator()

func newStructValidator() *playground.Validate {
	sv := playground.New(playground.WithRequiredStructEnabled())

	// Report fields by their JSON name so error keys match the request body.
	sv.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return f.Name
		}
		return name
	})

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(sv.RegisterValidation("phone", func(fl playground.FieldLevel) bool {
		return PhoneRX.MatchString(fl.Field().String())
	}))
	must(sv.RegisterValidation("slug", func(fl playground.FieldLevel) bool {
		return SlugRX.MatchString(fl.Field().String())
	}))

	return sv
}

// fieldErrorMessage renders a tag failure in the same wording the
// hand-written Check calls use, so a field reports the same message
// whichever layer catches it.
func fieldErrorMessage(fe playground.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "must be provided"
	case "phone":
		return "must be a valid E.164 phone number"
	case "email":
		return "must be a valid email address"
	case "slug":
		return "must contain only lowercase letters, digits and single hyphens"
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "min":
		if fe.Kind() == reflect.String {
			if fe.Param() == "1" {
				return "must not be empty"
			}
			return fmt.Sprintf("must be at least %s characters long", fe.Param())
		}
		return "must be at least " + fe.Param()
	case "max":
		if fe.Kind() == reflect.String {
			return fmt.Sprintf("must not be more than %s characters long", fe.Param())
		}
		return "must be at most " + fe.Param()
	default:
		return fmt.Sprintf("failed %q validation", fe.Tag())
	}
}

// PermittedValue reports whether value is one of the permitted values.
func PermittedValue[T comparable](value T, permittedValues ...T) bool {
	return slices.Contains(permittedValues, value)
}

// Matches reports whether value satisfies rx.
func Matches(value string, rx *regexp.Regexp) bool {
	return rx.MatchString(value)
}

// Unique reports whether all values in the slice are distinct.
func Unique[T comparable](values []T) bool {
	uniqueValues := make(map[T]bool)

	for _, value := range values {
		uniqueValues[value] = true
	}

	return len(values) == len(uniqueValues)
}
