package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

const upstreamErrorRetryBodyLimit = 64 << 10

// DoWithConfiguredUpstreamRetry retries explicit HTTP error responses before
// the caller can write them downstream. Transport errors are deliberately not
// replayed: without a response we cannot know whether the operation succeeded.
// The same account, proxy, TLS profile and concurrency reservation are retained.
func DoWithConfiguredUpstreamRetry(req *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if req == nil {
		return send(req)
	}
	state := upstreamErrorRetryFromContext(req.Context())
	// Only replay POST inference requests with a reproducible body. Do not buffer
	// uploaded bodies or replay mutation endpoints using PATCH/DELETE.
	if state == nil || req.Method != http.MethodPost || (req.Body != nil && req.Body != http.NoBody && req.GetBody == nil) {
		return send(req)
	}
	var original *http.Request
	resp, err := send(req)
	for {
		if err != nil || resp == nil || resp.StatusCode < 400 || resp.StatusCode > 599 || resp.StatusCode == http.StatusTooManyRequests {
			return resp, err
		}
		policy := state.getPolicy()
		if !policy.settings.Enabled || (!policy.statuses[resp.StatusCode] && len(policy.keywords) == 0) {
			return resp, nil
		}
		if req.Context().Err() != nil || state.clientCtx.Err() != nil {
			return resp, nil
		}
		var body []byte
		var readErr error
		if resp.Body != nil {
			errorBody := resp.Body
			stopClose := context.AfterFunc(state.clientCtx, func() { _ = errorBody.Close() })
			body, readErr = io.ReadAll(io.LimitReader(resp.Body, upstreamErrorRetryBodyLimit+1))
			stopClose()
			// Return every consumed byte (and any read error) if no retry is made.
			resp.Body = &upstreamRetryPrefixBody{
				ReadCloser: resp.Body,
				reader:     io.MultiReader(bytes.NewReader(body), &upstreamRetryReadError{err: readErr}, resp.Body),
			}
		}
		if readErr != nil || len(body) > upstreamErrorRetryBodyLimit {
			return resp, nil
		}
		delay, claimed := state.claim(resp.StatusCode, body)
		if !claimed {
			return resp, nil
		}
		if original == nil {
			original = req.Clone(req.Context())
		}
		next := original.Clone(original.Context())
		if original.GetBody != nil {
			next.Body, err = original.GetBody()
			if err != nil {
				return resp, nil
			}
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err := state.wait(req.Context(), delay); err != nil {
			if next.Body != nil {
				_ = next.Body.Close()
			}
			return nil, err
		}
		// A detached streaming/billing context must not start a new upstream
		// attempt after the original client has gone away.
		retryCtx, cancel := context.WithCancel(next.Context())
		stop := context.AfterFunc(state.clientCtx, cancel)
		next = next.WithContext(retryCtx)
		resp, err = send(next)
		stop()
		if err != nil || resp == nil {
			cancel()
			return resp, err
		}
		// Keep retryCtx alive while this response streams; cancel on body close.
		if resp.Body != nil {
			resp.Body = &upstreamRetryCancelBody{ReadCloser: resp.Body, cancel: cancel}
		} else {
			cancel()
		}
	}
}

type upstreamRetryPrefixBody struct {
	io.ReadCloser
	reader io.Reader
}

func (b *upstreamRetryPrefixBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

type upstreamRetryReadError struct{ err error }

func (r *upstreamRetryReadError) Read([]byte) (int, error) {
	if r.err != nil {
		err := r.err
		r.err = nil
		return 0, err
	}
	return 0, io.EOF
}

type upstreamRetryCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *upstreamRetryCancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
