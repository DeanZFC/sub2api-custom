package httputil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReadRequestBodyWithPreallocLimit applies a caller-specific decoded body cap.
// Oversized bodies are rejected rather than truncated. Callers accepting
// compressed input must separately limit the encoded request body.
func ReadRequestBodyWithPreallocLimit(req *http.Request, maxDecodedBytes int64) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	if maxDecodedBytes <= 0 {
		maxDecodedBytes = maxDecompressedBodySize
	}
	if preread, ok := req.Body.(*PrereadBody); ok {
		body := preread.Bytes()
		if int64(len(body)) > maxDecodedBytes {
			return nil, &http.MaxBytesError{Limit: maxDecodedBytes}
		}
		return body, nil
	}

	enc := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding")))
	identity := enc == "" || enc == "identity"
	if identity && req.ContentLength > maxDecodedBytes {
		return nil, &http.MaxBytesError{Limit: maxDecodedBytes}
	}
	capHint := requestBodyReadInitCap
	if req.ContentLength > int64(capHint) {
		if req.ContentLength > requestBodyReadMaxInitCap {
			capHint = requestBodyReadMaxInitCap
		} else {
			capHint = int(req.ContentLength)
		}
	}
	var reader io.Reader = req.Body
	if identity {
		reader = io.LimitReader(reader, maxDecodedBytes+1)
		if int64(capHint) > maxDecodedBytes+1 {
			capHint = int(maxDecodedBytes + 1)
		}
	}
	raw, err := readRequestBodyChunksWithBudget(req.Context(), reader, capHint, req.ContentLength)
	if err != nil {
		return nil, err
	}
	if identity {
		if int64(len(raw.data)) > maxDecodedBytes {
			raw.release(req.Context())
			return nil, &http.MaxBytesError{Limit: maxDecodedBytes}
		}
		return raw.data, nil
	}

	defer raw.release(req.Context())
	decoded, err := decompressRequestBodyLimitWithBudget(req.Context(), enc, raw.data, maxDecodedBytes)
	if err != nil {
		return nil, fmt.Errorf("decode Content-Encoding %q: %w", enc, err)
	}
	req.Header.Del("Content-Encoding")
	req.Header.Del("Content-Length")
	req.ContentLength = int64(len(decoded.data))
	return decoded.data, nil
}

func decompressRequestBodyLimit(encoding string, raw []byte, maxDecodedBytes int64) ([]byte, error) {
	decoded, err := decompressRequestBodyLimitWithBudget(context.Background(), encoding, raw, maxDecodedBytes)
	return decoded.data, err
}
