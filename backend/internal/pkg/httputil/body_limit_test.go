package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestReadRequestBodyWithPreallocLimit_PreservesSharedBudget(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			payload := []byte(strings.Repeat("x", 4096))
			wire := payload
			if encoding == "gzip" {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				if _, err := writer.Write(payload); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				wire = compressed.Bytes()
			}
			budget := NewBodyMemoryBudget(256)
			ctx, release := WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			req := newRequestWithBody(t, wire, encoding).WithContext(ctx)
			if _, err := ReadRequestBodyWithPreallocLimit(req, 8192); !errors.Is(err, ErrRequestBodyMemoryBudget) {
				t.Fatalf("expected shared budget rejection, got %v", err)
			}
			if used := requestBodyBudgetUsed(budget); used != 0 {
				t.Fatalf("failed read retained %d bytes", used)
			}
		})
	}
}

func TestReadRequestBodyWithPreallocLimit_CompressedBody(t *testing.T) {
	body := []byte(samplePayload)
	for _, encoding := range []string{"gzip", "x-gzip", "zstd", "deflate"} {
		t.Run(encoding, func(t *testing.T) {
			var compressed bytes.Buffer
			var encoder io.WriteCloser
			switch encoding {
			case "gzip", "x-gzip":
				encoder = gzip.NewWriter(&compressed)
			case "deflate":
				encoder = zlib.NewWriter(&compressed)
			case "zstd":
				var err error
				encoder, err = zstd.NewWriter(&compressed)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := encoder.Write(body); err != nil {
				t.Fatal(err)
			}
			if err := encoder.Close(); err != nil {
				t.Fatal(err)
			}

			req := newRequestWithBody(t, compressed.Bytes(), encoding)
			got, err := ReadRequestBodyWithPreallocLimit(req, int64(len(body)))
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("exact decoded limit: body = %q, error = %v", got, err)
			}
			if req.Header.Get("Content-Encoding") != "" || req.ContentLength != int64(len(body)) {
				t.Fatalf("decoded request metadata: encoding = %q, length = %d", req.Header.Get("Content-Encoding"), req.ContentLength)
			}

			req = newRequestWithBody(t, compressed.Bytes(), encoding)
			got, err = ReadRequestBodyWithPreallocLimit(req, int64(len(body)-1))
			var maxErr *http.MaxBytesError
			if got != nil || !errors.As(err, &maxErr) || maxErr.Limit != int64(len(body)-1) {
				t.Fatalf("oversized decoded body: body = %q, error = %v", got, err)
			}
			if req.Header.Get("Content-Encoding") != encoding {
				t.Fatal("failed decoding must preserve request metadata")
			}
		})
	}
}

func TestReadRequestBodyWithPreallocLimit_IdentityAndPreread(t *testing.T) {
	for _, kind := range []string{"known_length", "unknown_length", "preread"} {
		t.Run(kind, func(t *testing.T) {
			req := newRequestWithBody(t, []byte(samplePayload), "identity")
			if kind == "unknown_length" {
				req.ContentLength = -1
			}
			if kind == "preread" {
				req.Body = NewPrereadBody([]byte(samplePayload))
			}
			_, err := ReadRequestBodyWithPreallocLimit(req, int64(len(samplePayload)-1))
			var maxErr *http.MaxBytesError
			if !errors.As(err, &maxErr) {
				t.Fatalf("expected decoded body limit error, got %v", err)
			}
		})
	}
}
