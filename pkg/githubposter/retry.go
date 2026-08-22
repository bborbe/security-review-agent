// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	"context"
	stderrors "errors"
	"math/rand"
	"net"
	"time"

	errors "github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
)

// errPhantomPOST is the sentinel returned by verify-GET closures when POST returned 200
// but the review is absent in the subsequent listing. Classified transient so retryCall retries.
var errPhantomPOST = stderrors.New("phantom-POST sentinel")

// retryBaseDelay is the wait before a retry attempt. Overridden to 0 in tests via export_test.go.
var retryBaseDelay = 5 * time.Second

// retryJitterMs is the maximum random jitter in milliseconds added to the base delay.
var retryJitterMs = 1000

// CallResult carries the outcome of a single retryCall invocation.
type CallResult[T any] struct {
	Value        T
	HTTPStatus   int
	ResponseBody string
	Err          error
	Attempts     int
	Class        prpkg.ErrorClass
}

// classifyError maps (httpStatus, err) to an ErrorClass.
// The phantom-POST sentinel is treated as transient so retryCall retries the verify-GET.
func classifyError(httpStatus int, err error) prpkg.ErrorClass {
	if errors.Is(err, errPhantomPOST) {
		return prpkg.ErrorClassTransient
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return prpkg.ErrorClassTransient
	}
	var ne net.Error
	if err != nil && stderrors.As(err, &ne) {
		return prpkg.ErrorClassTransient
	}
	if httpStatus == 0 {
		return prpkg.ErrorClassTransient
	}
	if httpStatus >= 500 || httpStatus == 429 {
		return prpkg.ErrorClassTransient
	}
	if httpStatus == 401 || httpStatus == 403 || httpStatus == 404 || httpStatus == 422 {
		return prpkg.ErrorClassPermanent
	}
	if err != nil {
		return prpkg.ErrorClassUnknown
	}
	return prpkg.ErrorClassNotAFailure
}

// retryCall executes call at most twice: once immediately, and once after a short
// backoff if the first result is transient. label is kept for documentation purposes.
func retryCall[T any](
	ctx context.Context,
	label string,
	call func(ctx context.Context) (T, int, string, error),
) CallResult[T] {
	_ = label // kept in signature for documentation; errors come from the call closure
	value, status, body, err := call(ctx)
	class := classifyError(status, err)
	if err == nil {
		return CallResult[T]{
			Value:        value,
			HTTPStatus:   status,
			ResponseBody: body,
			Attempts:     1,
			Class:        class,
		}
	}
	if class != prpkg.ErrorClassTransient {
		return CallResult[T]{
			HTTPStatus:   status,
			ResponseBody: body,
			Err:          err,
			Attempts:     1,
			Class:        class,
		}
	}

	// #nosec G404 -- jitter for retry backoff, not security-sensitive
	n := rand.Intn(retryJitterMs + 1) //nolint:gosec
	jitter := time.Duration(n) * time.Millisecond
	select {
	case <-ctx.Done():
		return CallResult[T]{
			HTTPStatus:   status,
			ResponseBody: body,
			Err:          ctx.Err(),
			Attempts:     1,
			Class:        prpkg.ErrorClassTransient,
		}
	case <-time.After(retryBaseDelay + jitter):
	}

	value2, status2, body2, err2 := call(ctx)
	class2 := classifyError(status2, err2)
	return CallResult[T]{
		Value:        value2,
		HTTPStatus:   status2,
		ResponseBody: body2,
		Err:          err2,
		Attempts:     2,
		Class:        class2,
	}
}
