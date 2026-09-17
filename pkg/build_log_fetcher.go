// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

//counterfeiter:generate -o ../mocks/build-log-fetcher.go --fake-name BuildLogFetcher . BuildLogFetcher

// BuildRef identifies one Cloud Build whose log is to be fetched.
type BuildRef struct {
	BuildID string
	Project string
	Region  string
}

// BuildLogFetcher returns the log text of one Cloud Build.
type BuildLogFetcher interface {
	FetchLog(ctx context.Context, ref BuildRef) (string, error)
}

const (
	// cloudBuildAPIBaseURL is the production Cloud Build v1 REST host. Every test
	// injects its own httptest host, so this literal is the only place the
	// production host is named.
	cloudBuildAPIBaseURL = "https://cloudbuild.googleapis.com"
	// loggingAPIBaseURL is the production Cloud Logging v2 REST host, used to
	// read the build's log entries.
	loggingAPIBaseURL = "https://logging.googleapis.com"
	// cloudBuildHTTPTimeout bounds each Cloud Build / Cloud Logging HTTP call.
	cloudBuildHTTPTimeout = 30 * time.Second
	// errorBodySnippetLen bounds the response-body snippet embedded in an HTTP
	// error message.
	errorBodySnippetLen = 200
	// maxResponseBodyBytes bounds how much of a response body is read into
	// memory. A Cloud Build log is normally far smaller, but the number of log
	// entries behind one build is unbounded from this side, so a pathological
	// response must fail loudly rather than exhaust the pod's memory before the
	// caller truncates it.
	maxResponseBodyBytes = 50 << 20 // 50 MiB
	// loggingPageSize is the entries:list page size. A package constant, not a
	// config knob.
	loggingPageSize = 1000
	// loggingOrderBy keeps the flattened log chronological.
	//
	// The Cloud Logging reference recommends "timestamp desc" for recently
	// ingested entries, but that would reverse the log — and the flattened log
	// must read in build order. The filter already applies both remedies the
	// reference suggests (a resource-type restriction plus a timestamp>=
	// range bound), so "asc" is deliberate: do not "fix" this to desc.
	loggingOrderBy = "timestamp asc"
	// maxLogPages caps the entries:list page walk. A runaway guard: a server
	// that keeps returning a nextPageToken must not spin forever inside a
	// Kubernetes Job. Hitting the cap returns the log accumulated so far
	// together with an error, which the caller treats as a partial result.
	maxLogPages = 100
)

// cloudBuildScope is the ONLY OAuth scope the Cloud Build API accepts. A
// cloud-platform.read-only token is refused with HTTP 403 "insufficient
// authentication scopes". The same scope covers the Cloud Logging API.
// Read-only-ness is enforced by the runtime identity's IAM grants
// (roles/cloudbuild.builds.viewer for the build, logging.logEntries.list on the
// project — carried by roles/logging.viewer — for the log entries, plus
// serviceusage.services.use for the X-Goog-User-Project quota header), never by
// this scope string.
const cloudBuildScope = "https://www.googleapis.com/auth/cloud-platform"

// storageReadScope is the read-only Cloud Storage scope requested alongside
// cloudBuildScope. It stays on the credential path's scope list; the fetcher
// itself reads the build log from the Cloud Logging API.
const storageReadScope = "https://www.googleapis.com/auth/devstorage.read_only"

// gcpKeyJSONEnvVar is the environment variable carrying the GCP
// service-account key JSON. The fleet passes the key as an env var, never as
// a file, so no temp file and no GOOGLE_APPLICATION_CREDENTIALS dance.
const gcpKeyJSONEnvVar = "GCP_KEY_JSON"

// credentialsFromEnv returns the token source for the Cloud Build and Cloud
// Logging calls: built from the service-account key in gcpKeyJSONEnvVar when
// it is set, otherwise resolved through the Application Default Credentials
// chain (the documented local/dev path). Reading the environment variable is
// the only I/O — no file, no temp file, no subprocess, and no network call at
// construction time (CredentialsFromJSON returns a lazily-refreshing source).
func credentialsFromEnv(ctx context.Context) (oauth2.TokenSource, error) {
	if keyJSON := os.Getenv(gcpKeyJSONEnvVar); keyJSON != "" {
		creds, err := google.CredentialsFromJSON(
			ctx, []byte(keyJSON), cloudBuildScope, storageReadScope,
		)
		if err != nil {
			return nil, errors.Wrapf(ctx, err, "parse %s service-account key", gcpKeyJSONEnvVar)
		}
		glog.V(2).Infof("build-fix: credentials resolved from %s", gcpKeyJSONEnvVar)
		return creds.TokenSource, nil
	}

	resolved, err := google.DefaultTokenSource(ctx, cloudBuildScope, storageReadScope)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "resolve google application default credentials")
	}
	glog.V(2).Infof("build-fix: credentials resolved from Application Default Credentials")
	return resolved, nil
}

// cloudBuildLogFetcher reads a Cloud Build's log in two hops: the Cloud Build
// REST API confirms the build has finished and yields its start timestamp, then
// the Cloud Logging API returns the build's log entries. Both hops authenticate
// with a bearer token from the GCP_KEY_JSON service-account key, falling back
// to Application Default Credentials when that env var is unset (or an injected
// token source in tests). No Google API client library and no cloud CLI
// subprocess — plain net/http so the fetch is unit-testable.
type cloudBuildLogFetcher struct {
	cloudBuildBaseURL string
	loggingBaseURL    string
	tokenSource       oauth2.TokenSource
	client            *http.Client
	// maxBodyBytes caps one response body read. A field rather than the const
	// directly so tests can drive the oversize guard with a few bytes instead
	// of allocating the production limit.
	maxBodyBytes int64
	// maxLogPages caps the entries:list page walk. A field rather than the const
	// directly so tests can drive the cap with a couple of pages.
	maxLogPages int
}

// NewCloudBuildLogFetcher returns the production fetcher, authenticating with
// the GCP_KEY_JSON service-account key and falling back to Google Application
// Default Credentials when that env var is unset. It performs no I/O and
// returns no error, so factory composition stays pure; credentials are
// resolved lazily inside FetchLog so a credential problem surfaces as a
// runtime result.
func NewCloudBuildLogFetcher() BuildLogFetcher {
	return newCloudBuildLogFetcher(cloudBuildAPIBaseURL, loggingAPIBaseURL, nil)
}

// newCloudBuildLogFetcher is the concrete constructor — exposed for tests
// (export_test.go) so they can point it at httptest servers and a static token.
// A nil tokenSource means "resolve GCP_KEY_JSON / ADC from the environment on
// each FetchLog".
func newCloudBuildLogFetcher(
	cloudBuildBaseURL, loggingBaseURL string,
	tokenSource oauth2.TokenSource,
) *cloudBuildLogFetcher {
	return &cloudBuildLogFetcher{
		cloudBuildBaseURL: strings.TrimSuffix(cloudBuildBaseURL, "/"),
		loggingBaseURL:    strings.TrimSuffix(loggingBaseURL, "/"),
		tokenSource:       tokenSource,
		client:            &http.Client{Timeout: cloudBuildHTTPTimeout},
		maxBodyBytes:      maxResponseBodyBytes,
		maxLogPages:       maxLogPages,
	}
}

// FetchLog returns the log text of the build named by ref. The build ID is
// validated as a UUID first — it is interpolated into a URL path and a log
// filter and must not be attacker-shaped.
func (f *cloudBuildLogFetcher) FetchLog(ctx context.Context, ref BuildRef) (string, error) {
	if _, err := uuid.Parse(ref.BuildID); err != nil {
		return "", errors.Wrapf(ctx, err, "invalid cloud build id %q", ref.BuildID)
	}

	token, err := f.accessToken(ctx)
	if err != nil {
		return "", err
	}

	build, err := f.fetchBuild(ctx, ref, token)
	if err != nil {
		return "", err
	}
	if !isTerminalBuildStatus(build.Status) {
		return "", errors.Errorf(
			ctx,
			"cloud build %s has not finished (status=%s)",
			ref.BuildID,
			build.Status,
		)
	}

	return f.fetchLogEntries(ctx, ref, build, token)
}

// accessToken resolves the token source (injected, GCP_KEY_JSON, or ADC) and
// mints a bearer token from it. A non-nil injected source is used directly —
// credentialsFromEnv is not consulted.
func (f *cloudBuildLogFetcher) accessToken(ctx context.Context) (*oauth2.Token, error) {
	tokenSource := f.tokenSource
	if tokenSource == nil {
		resolved, err := credentialsFromEnv(ctx)
		if err != nil {
			return nil, err
		}
		tokenSource = resolved
	}
	token, err := tokenSource.Token()
	if err != nil {
		return nil, errors.Wrap(ctx, err, "mint cloud build access token")
	}
	return token, nil
}

// cloudBuildResource is the subset of the Cloud Build v1 build resource the
// fetcher needs: the terminal status and the timestamps that bound the log
// query.
type cloudBuildResource struct {
	Status     string `json:"status"`
	StartTime  string `json:"startTime"`
	CreateTime string `json:"createTime"`
}

// fetchBuild reads the build resource and returns the decoded subset.
func (f *cloudBuildLogFetcher) fetchBuild(
	ctx context.Context,
	ref BuildRef,
	token *oauth2.Token,
) (cloudBuildResource, error) {
	buildURL := fmt.Sprintf(
		"%s/v1/projects/%s/locations/%s/builds/%s",
		f.cloudBuildBaseURL,
		url.PathEscape(ref.Project),
		url.PathEscape(ref.Region),
		url.PathEscape(ref.BuildID),
	)
	start := time.Now()
	body, err := f.execute(ctx, http.MethodGet, buildURL, nil, nil, token)
	if err != nil {
		return cloudBuildResource{}, err
	}

	var build cloudBuildResource
	if err := json.Unmarshal(body, &build); err != nil {
		return cloudBuildResource{}, errors.Wrapf(
			ctx, err, "decode cloud build %s response", ref.BuildID,
		)
	}
	glog.V(2).Infof(
		"build-fix: read build id=%s status=%s latency=%s",
		ref.BuildID, build.Status, time.Since(start),
	)
	return build, nil
}

// logEntriesRequest is the Cloud Logging v2 entries:list request body.
type logEntriesRequest struct {
	ResourceNames []string `json:"resourceNames"`
	Filter        string   `json:"filter"`
	OrderBy       string   `json:"orderBy"`
	PageSize      int      `json:"pageSize"`
	PageToken     string   `json:"pageToken,omitempty"`
}

// logEntry is the subset of a Cloud Logging entry the fetcher reads.
type logEntry struct {
	TextPayload string          `json:"textPayload"`
	JSONPayload json.RawMessage `json:"jsonPayload"`
}

// text returns the entry's log text: textPayload, falling back to
// jsonPayload.message. An entry that carries neither contributes no text.
//
// jsonPayload is a free-form JSON object, so a payload that is not an object —
// or whose message is not a JSON string — must not fail the whole response: a
// single odd entry cannot be allowed to abort the fetch of a 1000-entry page.
func (e logEntry) text() string {
	if e.TextPayload != "" {
		return e.TextPayload
	}
	if len(e.JSONPayload) == 0 {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(e.JSONPayload, &payload); err != nil {
		return ""
	}
	return payload.Message
}

// logEntriesResponse is the subset of the entries:list response the fetcher
// reads. An empty Entries alongside a set NextPageToken is documented
// behavior, not an empty log — see walkLogPages.
type logEntriesResponse struct {
	Entries       []logEntry `json:"entries"`
	NextPageToken string     `json:"nextPageToken"`
}

// fetchLogEntries pages through the build's log entries and returns them as one
// newline-joined log text. An empty result returns an empty string with no
// error — the caller decides what an empty analysis means.
func (f *cloudBuildLogFetcher) fetchLogEntries(
	ctx context.Context,
	ref BuildRef,
	build cloudBuildResource,
	token *oauth2.Token,
) (string, error) {
	startTime := build.StartTime
	if startTime == "" {
		startTime = build.CreateTime
	}
	// The timestamp>= clause is mandatory: without a bounded time range the
	// Logging API may answer with an empty entries array alongside a valid
	// nextPageToken, which looks exactly like an empty log. Never fall through
	// to an unbounded filter — fail instead.
	if startTime == "" {
		return "", errors.Errorf(
			ctx,
			"cloud build %s has neither startTime nor createTime — cannot bound the log query",
			ref.BuildID,
		)
	}

	start := time.Now()
	texts, pages, err := f.walkLogPages(ctx, ref, startTime, token)
	logText := strings.Join(texts, "\n")
	glog.V(2).Infof(
		"build-fix: fetched log id=%s pages=%d entries=%d bytes=%d latency=%s",
		ref.BuildID, pages, len(texts), len(logText), time.Since(start),
	)
	if err != nil {
		return logText, err
	}
	return logText, nil
}

// walkLogPages issues entries:list calls until the API stops returning a
// nextPageToken, accumulating each page's entry texts in returned order (which
// is chronological, given loggingOrderBy).
//
// The ONLY continuation condition is a non-empty nextPageToken. An empty
// entries array on a page is not a stop condition, not an error, and not a
// signal that the log is empty: the API legitimately returns an empty page
// alongside a token while it is still searching ("the search found no log
// entries so far but it did not have time to search all the possible log
// entries"), and the documented remedy is to retry with that token. Breaking on
// an empty page would silently return an empty log.
func (f *cloudBuildLogFetcher) walkLogPages(
	ctx context.Context,
	ref BuildRef,
	startTime string,
	token *oauth2.Token,
) ([]string, int, error) {
	var texts []string
	pageToken := ""
	for page := 1; page <= f.maxLogPages; page++ {
		resp, err := f.fetchLogPage(ctx, ref, startTime, pageToken, token)
		if err != nil {
			return texts, page, err
		}
		texts = appendEntryTexts(texts, resp.Entries)
		if resp.NextPageToken == "" {
			return texts, page, nil
		}
		pageToken = resp.NextPageToken
	}
	return texts, f.maxLogPages, errors.Errorf(
		ctx,
		"cloud build %s log fetch exceeded %d pages",
		ref.BuildID,
		f.maxLogPages,
	)
}

// appendEntryTexts appends each entry's log text, skipping the entries that
// contribute none so they do not become blank lines in the joined log.
func appendEntryTexts(texts []string, entries []logEntry) []string {
	for _, entry := range entries {
		if text := entry.text(); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

// fetchLogPage issues one entries:list call and decodes the page. The
// X-Goog-User-Project header names the quota project and is mandatory: without
// it the request is not authorized for the project.
func (f *cloudBuildLogFetcher) fetchLogPage(
	ctx context.Context,
	ref BuildRef,
	startTime, pageToken string,
	token *oauth2.Token,
) (logEntriesResponse, error) {
	requestBody, err := json.Marshal(logEntriesRequest{
		ResourceNames: []string{"projects/" + ref.Project},
		Filter:        logEntriesFilter(startTime, ref.BuildID),
		OrderBy:       loggingOrderBy,
		PageSize:      loggingPageSize,
		PageToken:     pageToken,
	})
	if err != nil {
		return logEntriesResponse{}, errors.Wrapf(
			ctx, err, "encode log query for build %s", ref.BuildID,
		)
	}

	body, err := f.execute(
		ctx,
		http.MethodPost,
		f.loggingBaseURL+"/v2/entries:list",
		requestBody,
		map[string]string{
			"Content-Type":        "application/json",
			"X-Goog-User-Project": ref.Project,
		},
		token,
	)
	if err != nil {
		return logEntriesResponse{}, err
	}

	var page logEntriesResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return logEntriesResponse{}, errors.Wrapf(
			ctx, err, "decode log entries for build %s", ref.BuildID,
		)
	}
	return page, nil
}

// logEntriesFilter builds the Cloud Logging filter for one build's entries.
//
// The timestamp>= clause is load-bearing, not an optimization: without a
// bounded time range the API can answer with an empty entries array alongside a
// valid nextPageToken, which is indistinguishable from a real empty log and
// would silently produce an empty failure analysis. The other two clauses are
// the resource-type restriction and the exact build selector.
func logEntriesFilter(startTime, buildID string) string {
	return fmt.Sprintf(
		`timestamp>="%s" AND resource.type="build" AND resource.labels.build_id="%s"`,
		startTime,
		buildID,
	)
}

// execute performs one authenticated request and returns the response body. A
// non-2xx status is an error naming the method actually used, the URL, the
// status code, and a bounded body snippet.
func (f *cloudBuildLogFetcher) execute(
	ctx context.Context,
	method, rawURL string,
	requestBody []byte,
	headers map[string]string,
	token *oauth2.Token,
) ([]byte, error) {
	var bodyReader io.Reader
	if requestBody != nil {
		bodyReader = bytes.NewReader(requestBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "build request %s", rawURL)
	}
	req.Header.Set("Authorization", token.Type()+" "+token.AccessToken)
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "%s %s", strings.ToLower(method), rawURL)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the cap so an oversized body is distinguishable from
	// an exactly-maximal one, and bound the read so a huge response cannot
	// exhaust memory before the caller truncates it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBodyBytes+1))
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read response body %s", rawURL)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.Errorf(
			ctx,
			"%s %s returned HTTP %d: %s",
			method,
			rawURL,
			resp.StatusCode,
			truncate(string(body), errorBodySnippetLen),
		)
	}
	if int64(len(body)) > f.maxBodyBytes {
		return nil, errors.Errorf(
			ctx,
			"%s %s body exceeds %d bytes",
			method,
			rawURL,
			f.maxBodyBytes,
		)
	}
	return body, nil
}

// isTerminalBuildStatus reports whether a Cloud Build status means the build
// has finished. QUEUED / WORKING / PENDING are non-terminal, so a build that
// has not finished is distinguishable from a fetch failure.
func isTerminalBuildStatus(status string) bool {
	switch status {
	case "QUEUED", "WORKING", "PENDING":
		return false
	default:
		return true
	}
}
