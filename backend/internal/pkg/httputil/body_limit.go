package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
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
	raw, err := readRequestBodyChunks(reader, capHint, req.ContentLength)
	if err != nil {
		return nil, err
	}
	if identity {
		if int64(len(raw)) > maxDecodedBytes {
			return nil, &http.MaxBytesError{Limit: maxDecodedBytes}
		}
		return raw, nil
	}

	decoded, err := decompressRequestBodyLimit(enc, raw, maxDecodedBytes)
	if err != nil {
		return nil, fmt.Errorf("decode Content-Encoding %q: %w", enc, err)
	}
	req.Header.Del("Content-Encoding")
	req.Header.Del("Content-Length")
	req.ContentLength = int64(len(decoded))
	return decoded, nil
}

func decompressRequestBodyLimit(encoding string, raw []byte, maxDecodedBytes int64) ([]byte, error) {
	var reader io.Reader
	switch encoding {
	case "zstd":
		dec, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		reader = dec
	case "gzip", "x-gzip":
		dec, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = dec.Close() }()
		reader = dec
	case "deflate":
		dec, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = dec.Close() }()
		reader = dec
	default:
		return nil, errors.New("unsupported Content-Encoding")
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, maxDecodedBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > maxDecodedBytes {
		return nil, &http.MaxBytesError{Limit: maxDecodedBytes}
	}
	return decoded, nil
}
