// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter_test

import (
	"errors"
	"net"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/githubposter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("classifyError", func() {
	DescribeTable(
		"classifies errors",
		func(httpStatus int, err error, want pkg.ErrorClass) {
			Expect(githubposter.ClassifyErrorForTest(httpStatus, err)).To(Equal(want))
		},
		Entry("HTTP 200 → NotAFailure", 200, nil, pkg.ErrorClassNotAFailure),
		Entry("HTTP 429 → Transient", 429, nil, pkg.ErrorClassTransient),
		Entry("HTTP 500 → Transient", 500, nil, pkg.ErrorClassTransient),
		Entry("HTTP 503 → Transient", 503, nil, pkg.ErrorClassTransient),
		Entry("HTTP 401 → NotTransient", 401, nil, pkg.ErrorClassPermanent),
		Entry("HTTP 403 → NotTransient", 403, nil, pkg.ErrorClassPermanent),
		Entry("HTTP 404 → NotTransient", 404, nil, pkg.ErrorClassPermanent),
		Entry("HTTP 422 → NotTransient", 422, nil, pkg.ErrorClassPermanent),
		Entry(
			"status 0 with timeout error → Transient",
			0,
			&net.DNSError{IsTimeout: true},
			pkg.ErrorClassTransient,
		),
		Entry(
			"status 0 with generic error → Transient",
			0,
			errors.New("some error"),
			pkg.ErrorClassTransient,
		),
		Entry(
			"generic error with status 200 → Unknown",
			200,
			errors.New("some error"),
			pkg.ErrorClassUnknown,
		),
		Entry(
			"generic error with status 503 → Transient",
			503,
			errors.New("some error"),
			pkg.ErrorClassTransient,
		),
	)
})
