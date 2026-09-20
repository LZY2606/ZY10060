package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

type bodyKey struct{}

func withBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ctx := context.WithValue(r.Context(), bodyKey{}, body)
		cloned := r.Clone(ctx)
		cloned.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, cloned)
	})
}

func setRequestBody(context.Context, []byte) {}

func requestBody(ctx context.Context) ([]byte, bool) {
	body, ok := ctx.Value(bodyKey{}).([]byte)
	return body, ok
}
