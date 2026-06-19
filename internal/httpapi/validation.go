package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// FieldError is a single field-level validation problem, surfaced to the client
// in the error envelope so a form can highlight the offending input.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Validatable is implemented by request DTOs that can validate themselves. Each
// handler's input type owns its rules — explicit, dependency-free, and testable
// without HTTP. Return nil/empty when the value is valid.
type Validatable interface {
	Validate() []FieldError
}

// ValidationError carries field-level failures. The central error handler renders
// it as a 422 unprocessable_entity envelope with the fields attached.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string { return "validation failed" }

// bindAndValidate decodes the request body into dst and, when dst implements
// Validatable, runs its rules. A decode failure becomes a 400 bad_request; a
// validation failure becomes a 422 with field errors. dst must be a pointer.
//
// Handlers should call this as their first step:
//
//	var in createThingRequest
//	if err := bindAndValidate(c, &in); err != nil {
//	    return err
//	}
func bindAndValidate(c echo.Context, dst any) error {
	if err := c.Bind(dst); err != nil {
		// Echo's bind errors already carry a 4xx; normalise to our envelope with
		// a generic, non-leaky message (the raw decoder error can echo input).
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if v, ok := dst.(Validatable); ok {
		if fields := v.Validate(); len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
	}
	return nil
}
