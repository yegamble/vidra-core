package observability

import "context"

// Correlation carries safe request identifiers through HTTP handlers and service
// calls. It intentionally contains no user input beyond the already-sanitized,
// bounded correlation id.
type Correlation struct {
	RequestID     string
	CorrelationID string
}

type correlationContextKey struct{}

// ContextWithCorrelation attaches request/correlation identifiers so audit and
// job instrumentation below the HTTP layer can inherit them automatically.
func ContextWithCorrelation(ctx context.Context, ids Correlation) context.Context {
	return context.WithValue(ctx, correlationContextKey{}, ids)
}

// CorrelationFromContext returns safe request identifiers, or their zero value.
func CorrelationFromContext(ctx context.Context) Correlation {
	ids, _ := ctx.Value(correlationContextKey{}).(Correlation)
	return ids
}
