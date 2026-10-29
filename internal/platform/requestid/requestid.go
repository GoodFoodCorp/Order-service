// Package requestid carries the correlation id of a request through the
// context, from the inbound middleware to every outbound call.
package requestid

import "context"

// Header is the correlation header shared by every GoodFood service.
const Header = "X-Request-ID"

type key struct{}

// With returns a context carrying id.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key{}, id)
}

// From returns the id carried by ctx, or "".
func From(ctx context.Context) string {
	id, _ := ctx.Value(key{}).(string)
	return id
}
