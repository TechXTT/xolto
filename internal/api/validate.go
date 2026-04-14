package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var requestValidator = newRequestValidator()

func newRequestValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return field.Name
		}
		return name
	})
	return v
}

// Decode parses a request JSON body into dst, rejects unknown fields,
// and validates struct tags on the decoded payload.
func Decode[T any](r *http.Request, dst *T) error {
	if r == nil || dst == nil {
		return errors.New("invalid request")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if !errors.Is(err, io.EOF) {
			return normalizeDecodeError(err)
		}
	}
	if err := requestValidator.Struct(dst); err != nil {
		return normalizeValidationError(err)
	}
	return nil
}

func normalizeDecodeError(err error) error {
	if err == nil {
		return nil
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("invalid json syntax")
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if strings.TrimSpace(typeErr.Field) != "" {
			return fmt.Errorf("%s has an invalid type", typeErr.Field)
		}
		return errors.New("invalid json field type")
	}
	message := err.Error()
	if strings.HasPrefix(message, "json: unknown field ") {
		field := strings.Trim(strings.TrimPrefix(message, "json: unknown field "), "\"")
		return fmt.Errorf("unknown field %s", field)
	}
	return errors.New("invalid json body")
}

func normalizeValidationError(err error) error {
	if err == nil {
		return nil
	}
	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) || len(validationErrs) == 0 {
		return errors.New("invalid request body")
	}
	first := validationErrs[0]
	field := first.Field()
	switch first.Tag() {
	case "required":
		return fmt.Errorf("%s is required", field)
	case "email":
		return fmt.Errorf("%s must be a valid email", field)
	case "min":
		return fmt.Errorf("%s must be at least %s", field, first.Param())
	case "max":
		return fmt.Errorf("%s must be at most %s", field, first.Param())
	case "oneof":
		return fmt.Errorf("%s must be one of: %s", field, first.Param())
	case "url":
		return fmt.Errorf("%s must be a valid url", field)
	default:
		return fmt.Errorf("%s is invalid", field)
	}
}
