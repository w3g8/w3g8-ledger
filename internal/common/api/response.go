package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"
)

// Response is the standard API response envelope
type Response[T any] struct {
	Data  T      `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// Error represents an API error
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// PaginatedResponse is the standard paginated response envelope
type PaginatedResponse[T any] struct {
	Data       []T         `json:"data"`
	Pagination *Pagination `json:"pagination"`
	Error      *Error      `json:"error,omitempty"`
}

// Pagination holds pagination info
type Pagination struct {
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	Total      int64  `json:"total"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Common error codes
const (
	ErrCodeBadRequest       = "BAD_REQUEST"
	ErrCodeUnauthorized     = "UNAUTHORIZED"
	ErrCodeForbidden        = "FORBIDDEN"
	ErrCodeNotFound         = "NOT_FOUND"
	ErrCodeConflict         = "CONFLICT"
	ErrCodeValidation       = "VALIDATION_ERROR"
	ErrCodeInternalError    = "INTERNAL_ERROR"
	ErrCodeServiceUnavail   = "SERVICE_UNAVAILABLE"
	ErrCodeRateLimited      = "RATE_LIMITED"
	ErrCodeInsufficientFunds = "INSUFFICIENT_FUNDS"
)

// WriteJSON writes a JSON response
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// WriteData writes a successful data response
func WriteData[T any](w http.ResponseWriter, status int, data T) {
	WriteJSON(w, status, Response[T]{Data: data})
}

// WriteError writes an error response
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Response[any]{
		Error: &Error{
			Code:    code,
			Message: message,
		},
	})
}

// WriteErrorWithDetails writes an error response with details
func WriteErrorWithDetails(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	WriteJSON(w, status, Response[any]{
		Error: &Error{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// WritePaginated writes a paginated response
func WritePaginated[T any](w http.ResponseWriter, data []T, pagination *Pagination) {
	WriteJSON(w, http.StatusOK, PaginatedResponse[T]{
		Data:       data,
		Pagination: pagination,
	})
}

// BadRequest writes a 400 response
func BadRequest(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, ErrCodeBadRequest, message)
}

// Unauthorized writes a 401 response
func Unauthorized(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, message)
}

// Forbidden writes a 403 response
func Forbidden(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusForbidden, ErrCodeForbidden, message)
}

// NotFound writes a 404 response
func NotFound(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusNotFound, ErrCodeNotFound, message)
}

// Conflict writes a 409 response
func Conflict(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusConflict, ErrCodeConflict, message)
}

// InternalError writes a 500 response
func InternalError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusInternalServerError, ErrCodeInternalError, message)
}

// ValidationError writes a 422 response with validation details
func ValidationError(w http.ResponseWriter, err error) {
	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		details := make(map[string]string)
		for _, e := range validationErrors {
			details[e.Field()] = formatValidationError(e)
		}
		WriteErrorWithDetails(w, http.StatusUnprocessableEntity, ErrCodeValidation, "Validation failed", details)
		return
	}
	WriteError(w, http.StatusUnprocessableEntity, ErrCodeValidation, err.Error())
}

func formatValidationError(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return "This field is required"
	case "email":
		return "Must be a valid email address"
	case "min":
		return "Must be at least " + e.Param()
	case "max":
		return "Must be at most " + e.Param()
	case "len":
		return "Must be exactly " + e.Param() + " characters"
	case "uuid":
		return "Must be a valid UUID"
	case "ulid":
		return "Must be a valid ULID"
	case "oneof":
		return "Must be one of: " + e.Param()
	case "gte":
		return "Must be greater than or equal to " + e.Param()
	case "lte":
		return "Must be less than or equal to " + e.Param()
	case "gt":
		return "Must be greater than " + e.Param()
	case "lt":
		return "Must be less than " + e.Param()
	default:
		return "Invalid value"
	}
}

// Validate is a shared validator instance
var Validate = validator.New()

// DecodeAndValidate decodes JSON and validates the result
func DecodeAndValidate(r *http.Request, v interface{}) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return err
	}
	return Validate.Struct(v)
}

// PaginationParams extracts pagination parameters from query string
type PaginationParams struct {
	Limit  int
	Offset int
	Cursor string
}

// GetPaginationParams extracts pagination from request
func GetPaginationParams(r *http.Request, defaultLimit, maxLimit int) PaginationParams {
	params := PaginationParams{
		Limit:  defaultLimit,
		Offset: 0,
		Cursor: r.URL.Query().Get("cursor"),
	}

	if limit := r.URL.Query().Get("limit"); limit != "" {
		var l int
		if _, err := json.Number(limit).Int64(); err == nil {
			l, _ = r.URL.Query().Get("limit"), 0
		}
		if l > 0 && l <= maxLimit {
			params.Limit = l
		}
	}

	if offset := r.URL.Query().Get("offset"); offset != "" {
		var o int64
		if n, err := json.Number(offset).Int64(); err == nil {
			o = n
		}
		if o >= 0 {
			params.Offset = int(o)
		}
	}

	return params
}
