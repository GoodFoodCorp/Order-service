package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// CancelledBy says who cancelled an order.
type CancelledBy string

const (
	CancelledByCustomer   CancelledBy = "CUSTOMER"
	CancelledByRestaurant CancelledBy = "RESTAURANT"
	CancelledByHeadOffice CancelledBy = "HEAD_OFFICE"
)

// CancellationReason is why an order was cancelled. Each author picks from
// its own list, so the reasons stay meaningful for the restaurant's and the
// network's statistics.
type CancellationReason string

const (
	ReasonOrderedByMistake   CancellationReason = "ORDERED_BY_MISTAKE"
	ReasonChangedMind        CancellationReason = "CHANGED_MIND"
	ReasonWaitTooLong        CancellationReason = "WAIT_TOO_LONG"
	ReasonWrongAddress       CancellationReason = "WRONG_ADDRESS"
	ReasonItemUnavailable    CancellationReason = "ITEM_UNAVAILABLE"
	ReasonRestaurantTooBusy  CancellationReason = "RESTAURANT_TOO_BUSY"
	ReasonRestaurantClosing  CancellationReason = "RESTAURANT_CLOSING"
	ReasonNoCourierAvailable CancellationReason = "NO_COURIER_AVAILABLE"
	ReasonTechnicalIssue     CancellationReason = "TECHNICAL_ISSUE"
	ReasonCustomerRequest    CancellationReason = "CUSTOMER_REQUEST"
	// ReasonOther needs a comment saying what happened.
	ReasonOther CancellationReason = "OTHER"
)

// MaxCancellationCommentLength bounds the free-text explanation (runes).
const MaxCancellationCommentLength = 300

var reasonsBy = map[CancelledBy]map[CancellationReason]bool{
	CancelledByCustomer: {
		ReasonOrderedByMistake: true, ReasonChangedMind: true, ReasonWaitTooLong: true,
		ReasonWrongAddress: true, ReasonOther: true,
	},
	CancelledByRestaurant: {
		ReasonItemUnavailable: true, ReasonRestaurantTooBusy: true, ReasonRestaurantClosing: true,
		ReasonNoCourierAvailable: true, ReasonTechnicalIssue: true, ReasonCustomerRequest: true, ReasonOther: true,
	},
	CancelledByHeadOffice: {
		ReasonItemUnavailable: true, ReasonRestaurantTooBusy: true, ReasonRestaurantClosing: true,
		ReasonNoCourierAvailable: true, ReasonTechnicalIssue: true, ReasonCustomerRequest: true, ReasonOther: true,
	},
}

// Cancellation records who cancelled an order, why and when. The zero value
// means the order was not cancelled (or was, before reasons existed).
type Cancellation struct {
	By      CancelledBy
	Reason  CancellationReason
	Comment string
	At      *time.Time
}

// IsZero reports whether no cancellation was recorded.
func (c Cancellation) IsZero() bool { return c.Reason == "" }

// NewCancellation validates a cancellation request from the given author.
func NewCancellation(by CancelledBy, reason, comment string, at time.Time) (Cancellation, error) {
	allowed, ok := reasonsBy[by]
	if !ok {
		return Cancellation{}, NewForbiddenError("you are not allowed to cancel orders")
	}
	r := CancellationReason(strings.ToUpper(strings.TrimSpace(reason)))
	if r == "" {
		return Cancellation{}, NewValidationError("a cancellation reason is required")
	}
	if !allowed[r] {
		return Cancellation{}, NewValidationError("unknown cancellation reason: " + reason)
	}
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) > MaxCancellationCommentLength {
		return Cancellation{}, NewValidationError("the comment is limited to 300 characters")
	}
	if r == ReasonOther && utf8.RuneCountInString(comment) < 3 {
		return Cancellation{}, NewValidationError("say what happened when the reason is OTHER")
	}
	at = at.UTC()
	return Cancellation{By: by, Reason: r, Comment: comment, At: &at}, nil
}

// Cancel moves the order to CANCELLED and records why.
func (o *Order) Cancel(c Cancellation) error {
	if err := o.TransitionTo(StatusCancelled); err != nil {
		return err
	}
	o.Cancellation = c
	return nil
}
