package service

import "context"

type usageHTTPStatusKey struct{}

func WithUsageHTTPStatus(ctx context.Context, status int) context.Context {
	return context.WithValue(ctx, usageHTTPStatusKey{}, status)
}

func UsageHTTPStatus(ctx context.Context) *int {
	if ctx != nil {
		if status, ok := ctx.Value(usageHTTPStatusKey{}).(int); ok && status >= 100 && status <= 599 {
			return &status
		}
	}
	return nil
}
