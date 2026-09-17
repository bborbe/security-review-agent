// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	pkg "github.com/bborbe/security-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"
)

// apiStub is a minimal httptest-backed API double. It records the method, the
// request URI, the request body and the Authorization / Content-Type /
// X-Goog-User-Project headers of every request it receives, and serves a
// sequence of canned responses — one per request, the last one repeating once
// the sequence is exhausted — so a multi-page walk can be driven.
type apiStub struct {
	server      *httptest.Server
	statuses    []int
	bodies      []string
	requests    []string
	methods     []string
	requestBody []string
	authHeader  []string
	contentType []string
	quotaHeader []string
}

func newAPIStub(status int, body string) *apiStub {
	return newAPIStubSequence([]int{status}, []string{body})
}

func newAPIStubSequence(statuses []int, bodies []string) *apiStub {
	stub := &apiStub{statuses: statuses, bodies: bodies}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		stub.requests = append(stub.requests, r.URL.RequestURI())
		stub.methods = append(stub.methods, r.Method)
		stub.requestBody = append(stub.requestBody, string(raw))
		stub.authHeader = append(stub.authHeader, r.Header.Get("Authorization"))
		stub.contentType = append(stub.contentType, r.Header.Get("Content-Type"))
		stub.quotaHeader = append(stub.quotaHeader, r.Header.Get("X-Goog-User-Project"))
		status, body := stub.response(len(stub.requests) - 1)
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	return stub
}

// response returns the canned response for the n-th call, repeating the last
// entry once the sequence is exhausted.
func (s *apiStub) response(n int) (int, string) {
	if n >= len(s.statuses) {
		n = len(s.statuses) - 1
	}
	var body string
	if n < len(s.bodies) {
		body = s.bodies[n]
	}
	return s.statuses[n], body
}

func (s *apiStub) close() { s.server.Close() }

func (s *apiStub) count() int { return len(s.requests) }

// query decodes the n-th recorded request body as a Cloud Logging
// entries:list query.
func (s *apiStub) query(n int) logEntriesQuery {
	var query logEntriesQuery
	Expect(json.Unmarshal([]byte(s.requestBody[n]), &query)).NotTo(HaveOccurred())
	return query
}

// logEntriesQuery is the entries:list request body as the specs read it.
type logEntriesQuery struct {
	ResourceNames []string `json:"resourceNames"`
	Filter        string   `json:"filter"`
	OrderBy       string   `json:"orderBy"`
	PageSize      int      `json:"pageSize"`
	PageToken     string   `json:"pageToken"`
}

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
		buildID    = "88027472-8599-454f-8112-569739235dad"
		project    = "smedia-brogrammers"
		region     = "europe-west3"
		startTime  = "2026-09-17T10:00:00Z"
		createTime = "2026-09-17T09:59:00Z"
	)

	// The Cloud Build metadata responses the build hop can serve: a finished
	// build with a start timestamp (the normal case), one that only carries a
	// createTime, and one that carries neither.
	const (
		failedBuild             = `{"status":"FAILURE","startTime":"` + startTime + `"}`
		failedBuildWithoutStart = `{"status":"FAILURE","createTime":"` + createTime + `"}`
		failedBuildNoTimestamps = `{"status":"FAILURE"}`
	)

	var (
		ctx     context.Context
		build   *apiStub
		logging *apiStub
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
		Expect(
			os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json"),
		).NotTo(HaveOccurred())
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
		Expect(os.Setenv("GCP_KEY_JSON", "not-json")).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.Unsetenv("GCP_KEY_JSON") })

		buildStub := newAPIStub(http.StatusOK, "unused")
		loggingStub := newAPIStub(http.StatusOK, "unused")
		DeferCleanup(buildStub.close)
		DeferCleanup(loggingStub.close)

		// A nil token source is the documented "resolve from the environment" case.
		fetcher := pkg.NewCloudBuildLogFetcherForTest(
			buildStub.server.URL, loggingStub.server.URL, nil,
		)

		log, err := fetcher.FetchLog(ctx, ref)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("GCP_KEY_JSON"))
		Expect(log).To(BeEmpty())
		Expect(buildStub.count()).To(Equal(0))
		Expect(loggingStub.count()).To(Equal(0))
	})

	Context("when the build has finished", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			logging = newAPIStub(
				http.StatusOK,
				`{"entries":[{"textPayload":"Step #0 - \"check-golang\": building"}]}`,
			)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("reads the build then the log entries and returns the log text", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(log).To(Equal(`Step #0 - "check-golang": building`))
			Expect(build.requests).To(Equal([]string{
				"/v1/projects/" + project + "/locations/" + region + "/builds/" + buildID,
			}))
			Expect(logging.requests).To(Equal([]string{"/v2/entries:list"}))
		})

		// The httptest double is the only place the external contract can be
		// checked without live GCP access, so this asserts the whole request,
		// not just the returned string.
		It("sends the full Cloud Logging request shape", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())

			Expect(logging.methods).To(Equal([]string{http.MethodPost}))
			Expect(logging.authHeader).To(Equal([]string{"Bearer test-token"}))
			Expect(logging.contentType).To(Equal([]string{"application/json"}))
			Expect(logging.quotaHeader).To(Equal([]string{project}))

			// The Cloud Build hop is unchanged: GET, bearer token, and no
			// quota-project header.
			Expect(build.methods).To(Equal([]string{http.MethodGet}))
			Expect(build.authHeader).To(Equal([]string{"Bearer test-token"}))
			Expect(build.quotaHeader).To(Equal([]string{""}))

			query := logging.query(0)
			Expect(query.ResourceNames).To(Equal([]string{"projects/" + project}))
			Expect(query.OrderBy).To(Equal("timestamp asc"))
			Expect(query.PageSize).To(Equal(1000))
			Expect(query.Filter).To(ContainSubstring(`timestamp>="` + startTime + `"`))
			Expect(query.Filter).To(ContainSubstring(`resource.type="build"`))
			Expect(query.Filter).To(ContainSubstring(
				`resource.labels.build_id="` + buildID + `"`,
			))
			// pageToken is omitted while empty.
			Expect(logging.requestBody[0]).NotTo(ContainSubstring("pageToken"))
		})

		It("uses the build's startTime as the filter's lower bound", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(logging.query(0).Filter).To(ContainSubstring(`timestamp>="` + startTime + `"`))
		})
	})

	Context("when the build carries no startTime", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuildWithoutStart)
			logging = newAPIStub(http.StatusOK, `{"entries":[{"textPayload":"line"}]}`)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("falls back to createTime for the filter's lower bound", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(logging.query(0).Filter).To(ContainSubstring(
				`timestamp>="` + createTime + `"`,
			))
		})
	})

	Context("when the build carries neither timestamp", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuildNoTimestamps)
			logging = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		// An unbounded filter is the one shape that silently yields an empty
		// log, so this must fail rather than fall through to one.
		It("fails naming the build and issues no logging request", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(log).To(BeEmpty())
			Expect(err.Error()).To(ContainSubstring(buildID))
			Expect(err.Error()).To(ContainSubstring("startTime"))
			Expect(err.Error()).To(ContainSubstring("createTime"))
			Expect(logging.count()).To(Equal(0))
		})
	})

	Context("when the log spans multiple pages", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			logging = newAPIStubSequence(
				[]int{http.StatusOK, http.StatusOK},
				[]string{
					`{"entries":[{"textPayload":"page one a"},` +
						`{"textPayload":"page one b"}],"nextPageToken":"token-1"}`,
					`{"entries":[{"textPayload":"page two a"}]}`,
				},
			)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("joins both pages in order with no separator at the boundary", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(log).To(Equal("page one a\npage one b\npage two a"))
			Expect(logging.count()).To(Equal(2))
			Expect(logging.query(0).PageToken).To(BeEmpty())
			Expect(logging.query(1).PageToken).To(Equal("token-1"))
		})
	})

	// The API legitimately returns an empty entries array alongside a valid
	// nextPageToken while it is still searching. A loop that stops on an empty
	// page returns an empty log here.
	Context("when a page is empty but carries a continuation token", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			logging = newAPIStubSequence(
				[]int{http.StatusOK, http.StatusOK},
				[]string{
					`{"entries":[],"nextPageToken":"token-1"}`,
					`{"entries":[{"textPayload":"found on the retry"}]}`,
				},
			)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("retries with the token instead of treating the empty page as the end", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(log).To(Equal("found on the retry"))
			Expect(logging.count()).To(Equal(2))
			Expect(logging.query(1).PageToken).To(Equal("token-1"))
		})
	})

	Context("when entries carry structured payloads", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			logging = newAPIStub(http.StatusOK, `{"entries":[
				{"textPayload":"plain line"},
				{"jsonPayload":{"message":"structured line"}},
				{"jsonPayload":{"other":"no message"}},
				{"jsonPayload":"not an object"},
				{"jsonPayload":{"message":42}}]}`)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("falls back to jsonPayload.message and skips entries with no text", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(log).To(Equal("plain line\nstructured line"))
		})
	})

	Context("when the logging API fails", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			logging = newAPIStub(
				http.StatusForbidden, `{"error":{"message":"permission denied"}}`,
			)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("errors naming the status and the URL without re-reading the build", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(log).To(BeEmpty())
			Expect(err.Error()).To(ContainSubstring("403"))
			Expect(err.Error()).To(ContainSubstring("/v2/entries:list"))
			Expect(build.count()).To(Equal(1))
		})
	})

	Context("when the page walk exceeds the page cap", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			logging = newAPIStub(
				http.StatusOK,
				`{"entries":[{"textPayload":"line"}],"nextPageToken":"token"}`,
			)
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("stops at the cap and returns the accumulated log with an error", func() {
			fetcher := pkg.NewCloudBuildLogFetcherWithCapsForTest(
				build.server.URL, logging.server.URL, staticToken(), 1<<20, 2,
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exceeded 2 pages"))
			Expect(log).To(Equal("line\nline"))
			Expect(logging.count()).To(Equal(2))
		})
	})

	Context("when the logging response exceeds the body cap", func() {
		// The cap applies to every response body, so it must clear the build
		// metadata JSON (~54 bytes) or the first hop fails before logging is
		// ever reached — the boundary under test is the logging response's.
		const bodyCap = 256

		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, failedBuild)
			DeferCleanup(build.close)
		})

		It("errors naming the cap rather than reading the whole response into memory", func() {
			logging = newAPIStub(http.StatusOK, strings.Repeat("x", bodyCap*2))
			DeferCleanup(logging.close)

			fetcher := pkg.NewCloudBuildLogFetcherWithCapsForTest(
				build.server.URL, logging.server.URL, staticToken(), bodyCap, 100,
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(log).To(BeEmpty())
			Expect(err.Error()).To(ContainSubstring("exceeds 256 bytes"))
		})

		It("accepts a response exactly at the cap", func() {
			prefix := `{"entries":[{"textPayload":"`
			suffix := `"}]}`
			payload := strings.Repeat("x", bodyCap-len(prefix)-len(suffix))
			body := prefix + payload + suffix
			Expect(body).To(HaveLen(bodyCap))

			logging = newAPIStub(http.StatusOK, body)
			DeferCleanup(logging.close)

			fetcher := pkg.NewCloudBuildLogFetcherWithCapsForTest(
				build.server.URL, logging.server.URL, staticToken(), bodyCap, 100,
			)

			log, err := fetcher.FetchLog(ctx, ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(log).To(Equal(payload))
		})
	})

	Context("when the build has not finished", func() {
		BeforeEach(func() {
			build = newAPIStub(
				http.StatusOK, `{"status":"WORKING","startTime":"`+startTime+`"}`,
			)
			logging = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("errors naming the non-terminal status and never queries the log", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("WORKING"))
			Expect(logging.count()).To(Equal(0))
		})
	})

	Context("when the build API fails", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusNotFound, `{"error":{"message":"build not found"}}`)
			logging = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("errors naming the HTTP status", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, ref)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
			Expect(logging.count()).To(Equal(0))
		})
	})

	Context("when the build id is not a UUID", func() {
		BeforeEach(func() {
			build = newAPIStub(http.StatusOK, "unused")
			logging = newAPIStub(http.StatusOK, "unused")
			DeferCleanup(build.close)
			DeferCleanup(logging.close)
		})

		It("errors before any HTTP call", func() {
			fetcher := pkg.NewCloudBuildLogFetcherForTest(
				build.server.URL, logging.server.URL, staticToken(),
			)

			_, err := fetcher.FetchLog(ctx, pkg.BuildRef{
				BuildID: "../../secrets", Project: project, Region: region,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid cloud build id"))
			Expect(build.count()).To(Equal(0))
			Expect(logging.count()).To(Equal(0))
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
		Expect(os.Setenv(
			"GCP_KEY_JSON",
			`{"type":"authorized_user","client_id":"test-client","refresh_token":"test-refresh"}`,
		)).NotTo(HaveOccurred())
		_ = os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")

		tokenSource, err := pkg.CredentialsFromEnvForTest(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenSource).NotTo(BeNil())
	})

	It("fails on a malformed key instead of silently falling back to ADC", func() {
		restoreEnv("GCP_KEY_JSON")
		restoreEnv("GOOGLE_APPLICATION_CREDENTIALS")
		Expect(os.Setenv("GCP_KEY_JSON", "not-json")).NotTo(HaveOccurred())
		// This path makes the ADC branch fail with its own distinctive message,
		// so a silent fallback would be visible in the error text.
		Expect(
			os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json"),
		).NotTo(HaveOccurred())

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
		Expect(
			os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/adc.json"),
		).NotTo(HaveOccurred())

		tokenSource, err := pkg.CredentialsFromEnvForTest(ctx)
		Expect(err).To(HaveOccurred())
		Expect(tokenSource).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("application default credentials"))
	})
})
