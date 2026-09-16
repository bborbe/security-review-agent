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

// restoreEnv registers a DeferCleanup that puts name back to the value it held
// when restoreEnv was called — unsetting it when it was absent — so every spec
// that mutates the environment stays hermetic and order-independent. Call it
// BEFORE mutating the variable.
func restoreEnv(name string) {
	original, present := os.LookupEnv(name)
	DeferCleanup(func() {
		if present {
			_ = os.Setenv(name, original)
			return
		}
		_ = os.Unsetenv(name)
	})
}

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
		// Unset GCP_KEY_JSON explicitly so an ambient value can never divert
		// this spec off the ADC branch it is asserting on.
		restoreEnv("GCP_KEY_JSON")
		_ = os.Unsetenv("GCP_KEY_JSON")
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json")
		DeferCleanup(func() { _ = os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS") })

		fetcher := pkg.NewCloudBuildLogFetcher()
		log, err := fetcher.FetchLog(ctx, ref)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("application default credentials"))
		Expect(err.Error()).NotTo(ContainSubstring("GCP_KEY_JSON"))
		Expect(log).To(BeEmpty())
	})

	It("fails on a malformed GCP_KEY_JSON before any HTTP call", func() {
		restoreEnv("GCP_KEY_JSON")
		os.Setenv("GCP_KEY_JSON", "not-json")
		DeferCleanup(func() { _ = os.Unsetenv("GCP_KEY_JSON") })

		buildStub := newAPIStub(http.StatusOK, "unused")
		storageStub := newAPIStub(http.StatusOK, "unused")
		DeferCleanup(buildStub.close)
		DeferCleanup(storageStub.close)

		// A nil token source is the documented "resolve from the environment" case.
		fetcher := pkg.NewCloudBuildLogFetcherForTest(
			buildStub.server.URL, storageStub.server.URL, nil,
		)

		log, err := fetcher.FetchLog(ctx, ref)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("GCP_KEY_JSON"))
		Expect(log).To(BeEmpty())
		Expect(buildStub.count()).To(Equal(0))
		Expect(storageStub.count()).To(Equal(0))
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

var _ = Describe("credentialsFromEnv", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	// Every spec sets or unsets BOTH variables explicitly so none of them
	// depends on the ambient environment.
	It("uses the GCP_KEY_JSON key when it is set", func() {
		restoreEnv("GCP_KEY_JSON")
		restoreEnv("GOOGLE_APPLICATION_CREDENTIALS")
		os.Setenv(
			"GCP_KEY_JSON",
			`{"type":"authorized_user","client_id":"test-client","refresh_token":"test-refresh"}`,
		)
		_ = os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")

		tokenSource, err := pkg.CredentialsFromEnvForTest(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenSource).NotTo(BeNil())
	})

	It("fails on a malformed key instead of silently falling back to ADC", func() {
		restoreEnv("GCP_KEY_JSON")
		restoreEnv("GOOGLE_APPLICATION_CREDENTIALS")
		os.Setenv("GCP_KEY_JSON", "not-json")
		// This path makes the ADC branch fail with its own distinctive message,
		// so a silent fallback would be visible in the error text.
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json")

		tokenSource, err := pkg.CredentialsFromEnvForTest(ctx)
		Expect(err).To(HaveOccurred())
		Expect(tokenSource).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("GCP_KEY_JSON"))
		Expect(err.Error()).NotTo(ContainSubstring("application default credentials"))
	})

	It("falls back to Application Default Credentials when the env var is unset", func() {
		restoreEnv("GCP_KEY_JSON")
		restoreEnv("GOOGLE_APPLICATION_CREDENTIALS")
		_ = os.Unsetenv("GCP_KEY_JSON")
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json")

		tokenSource, err := pkg.CredentialsFromEnvForTest(ctx)
		Expect(err).To(HaveOccurred())
		Expect(tokenSource).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("application default credentials"))
	})
})
