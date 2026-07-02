package tools

import "context"

type authorizedDirsKey struct{}

// WithAuthorizedDirs returns a derived context carrying the authorized
// external directories for the current tool call. An empty/nil slice
// returns ctx unchanged so workspace-only calls pay no allocation.
// The slice is copied so later mutation by the caller cannot affect the
// value stored in the context.
func WithAuthorizedDirs(ctx context.Context, dirs []string) context.Context {
	if len(dirs) == 0 {
		return ctx
	}
	cp := make([]string, len(dirs))
	copy(cp, dirs)
	return context.WithValue(ctx, authorizedDirsKey{}, cp)
}

// AuthorizedDirs returns the authorized external directories attached to
// ctx, or nil if none.
func AuthorizedDirs(ctx context.Context) []string {
	v, _ := ctx.Value(authorizedDirsKey{}).([]string)
	return v
}
