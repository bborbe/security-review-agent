// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"

	pkg "github.com/bborbe/security-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"
)

// apiStub is a minimal httptest-backed API double: it records every request
// URI and Authorization header it receives and serves one canned response.
type apiStub struct {
	server     *httptest.Server
	status     int
	body       string
	requests   []string
	authHeader []string
}

func newAPIStub(status int, body string) *apiStub {
	stub := &apiStub{status: status, body: body}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.requests = append(stub.requests, r.URL.RequestURI())
		stub.authHeader = append(stub.authHeader, r.Header.Get("Authorization"))
		if stub.status != http.StatusOK {
			w.WriteHeader(stub.status)
		}
		_, _ = w.Write([]byte(stub.body))
	}))
	return stub
}

func (s *apiStub) close() { s.server.Close() }

func (s *apiStub) count() int { return len(s.requests) }

var _ = Describe("cloudBuildLogFetcher", func() {
	const (
		buildID = "88027472-8599-454f-8112-569739235dad"
		project = "smedia-brogrammers"
		region  = "europe-west3"
	)

	var (
		ctx     context.Context
		build   *apiStub
		storage *apiStub
	)

	// staticToken mimics the injected-credential path; production resolves ADC.
	staticToken := func() oauth2.TokenSource {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	}

	ref := pkg.BuildRef{BuildID: buildID, Project: project, Region: region}

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("constructs the production fetcher without I/O", func() {
		Expect(pkg.NewCloudBuildLogFetcher()).NotTo(BeNil())
	})

	It("surfaces a credential problem as a runtime error, not a boot failure", func() {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json")
		DeferCleanup(func() { _ = os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS") })

		fetcher := pkg.NewCloudBuildLogFetcher()
		log, err := fetcher.FetchLog(ctx, ref)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("application default credentials"))
		Expect(log).To(BeEmpty())
	})

	Context("when the build is bucket-addressable", func() {
		const logBody = "Step #0 - \"check-golang\": building\n" +
			"ERROR: build step 0 \"golang:1.27\" failed: step exited with non-zero status: 2\n"

		BeforeEach(func() {
			build = newAPIStub(
				http.StatusOK,
				`{"status":"FAILURE","logsBucket":"gs://test-logs","options":{"logging":"LEGACY"}}`,
			)
			storage = newAPIStub(http.StatusOK, logBody)
			DeferCleanup(build.close)
			DeferCleanup(storage.close)
		})

		It("reads the build then the log object and returns the log text", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(log).To(Equal(logBody))
			Expect(build.requests).To(Equal([]string{
				"/v1/projects/" + project + "/locations/" + region + "/builds/" + buildID,
			}))
			Expect(storage.requests).To(Equal([]string{
				"/storage/v1/b/test-logs/o/log-" + buildID + ".txt?alt=media",
			}))
		})

		It("sends the bearer token on both calls", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(build.authHeader).To(Equal([]string{"Bearer test-token"}))
			Expect(storage.authHeader).To(Equal([]string{"Bearer test-token"}))
		})
	})

	Context("when the build logs are not bucket-addressable", func() {
		BeforeEach(func() {
			build = newAPIStub(
				http.StatusOK,
				`{"status":"FAILURE","logsBucket":"gs://test-logs","options":{"logging":"CLOUD_LOGGING_ONLY"}}`,
			)
			storage = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(storage.close)
		})

		It("errors naming the observed logging value and never calls storage", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(log).To(BeEmpty())
			Expect(err.Error()).To(ContainSubstring("CLOUD_LOGGING_ONLY"))
			Expect(err.Error()).To(ContainSubstring(buildID))
			Expect(storage.count()).To(Equal(0))
		})
	})

	Context("when the build has not finished", func() {
		BeforeEach(func() {
			build = newAPIStub(
				http.StatusOK,
				`{"status":"WORKING","logsBucket":"gs://test-logs","options":{"logging":"LEGACY"}}`,
			)
			storage = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(storage.close)
		})

		It("errors naming the non-terminal status", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("WORKING"))
			Expect(storage.count()).To(Equal(0))
		})
	})

	Context("when the build API fails", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusNotFound, `{"error":{"message":"build not found"}}`)
			storage = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(storage.close)
		})

		It("errors naming the HTTP status", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
			Expect(storage.count()).To(Equal(0))
		})
	})

	Context("when the storage API fails", func() {
		BeforeEach(func() {
			build = newAPIStub(
				http.StatusOK,
				`{"status":"FAILURE","logsBucket":"gs://test-logs","options":{"logging":"LEGACY"}}`,
			)
			storage = newAPIStub(http.StatusInternalServerError, "backend error")
			DeferCleanup(build.close)
			DeferCleanup(storage.close)
		})

		It("errors naming the HTTP status", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("500"))
			Expect(storage.count()).To(Equal(1))
		})
	})

	Context("when the build id is not a UUID", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, "unused")
			storage = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(storage.close)
		})

		It("errors before any HTTP call", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, storage.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, pkg.BuildRef{
				BuildID: "../../secrets", Project: project, Region: region,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid cloud build id"))
			Expect(build.count()).To(Equal(0))
			Expect(storage.count()).To(Equal(0))
		})
	})
})
