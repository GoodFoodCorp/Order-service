package domain

import "fmt"

// Typed business errors. The HTTP adapter maps them to status codes;
// the domain and application layers never deal with HTTP.
type ErrorCode string

const (
	ErrCodeValidation        ErrorCode = "VALIDATION_ERROR"
	ErrCodeNotFound          ErrorCode = "NOT_FOUND"
	ErrCodeForbidden         ErrorCode = "FORBIDDEN"
	ErrCodeInvalidTransition ErrorCode = "INVALID_STATUS_TRANSITION"
	ErrCodePayment           ErrorCode = "PAYMENT_ERROR"
	ErrCodeConflict          ErrorCode = "CONFLICT"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func NewValidationError(msg string) *Error {
	return &Error{Code: ErrCodeValidation, Message: msg}
}

func NewNotFoundError(msg string) *Error {
	return &Error{Code: ErrCodeNotFound, Message: msg}
}

func NewForbiddenError(msg string) *Error {
	return &Error{Code: ErrCodeForbidden, Message: msg}
}

func NewInvalidTransitionError(from, to OrderStatus) *Error {
	return &Error{
		Code:    ErrCodeInvalidTransition,
		Message: fmt.Sprintf("cannot transition order from %s to %s", from, to),
	}
}

func NewPaymentError(msg string) *Error {
	return &Error{Code: ErrCodePayment, Message: msg}
}

func NewConflictError(msg string) *Error {
	return &Error{Code: ErrCodeConflict, Message: msg}
}
