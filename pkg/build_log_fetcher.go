// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
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
	// storageAPIBaseURL is the production Cloud Storage JSON API host, used to
	// read the build's log object out of its logs bucket.
	storageAPIBaseURL = "https://storage.googleapis.com"
	// cloudBuildHTTPTimeout bounds each Cloud Build / Cloud Storage HTTP call.
	cloudBuildHTTPTimeout = 30 * time.Second
	// errorBodySnippetLen bounds the response-body snippet embedded in an HTTP
	// error message.
	errorBodySnippetLen = 200
	// maxResponseBodyBytes bounds how much of a response body is read into
	// memory. A Cloud Build log is normally far smaller, but the size of the
	// GCS object is unbounded from this side, so a pathological object must
	// fail loudly rather than exhaust the pod's memory before the caller
	// truncates it.
	maxResponseBodyBytes = 50 << 20 // 50 MiB
)

// cloudBuildScope is the ONLY OAuth scope the Cloud Build API accepts. A
// cloud-platform.read-only token is refused with HTTP 403 "insufficient
// authentication scopes". Read-only-ness is enforced by the runtime identity's
// two IAM grants (roles/cloudbuild.builds.viewer +
// roles/storage.objectViewer), not by this scope string.
const cloudBuildScope = "https://www.googleapis.com/auth/cloud-platform"

// storageReadScope is the read-only Cloud Storage scope used to read the log
// object from the build's logs bucket.
const storageReadScope = "https://www.googleapis.com/auth/devstorage.read_only"

// gcpKeyJSONEnvVar is the environment variable carrying the GCP
// service-account key JSON. The fleet passes the key as an env var, never as
// a file, so no temp file and no GOOGLE_APPLICATION_CREDENTIALS dance.
const gcpKeyJSONEnvVar = "GCP_KEY_JSON"

// credentialsFromEnv returns the token source for the Cloud Build and Cloud
// Storage calls: built from the service-account key in gcpKeyJSONEnvVar when
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

// cloudBuildLogFetcher reads a Cloud Build's log through the Cloud Build REST
// API plus the Cloud Storage JSON API, authenticating with a bearer token from
// the GCP_KEY_JSON service-account key, falling back to Application Default
// Credentials when that env var is unset (or an injected token source in
// tests). No Google API client library and no gcloud subprocess — plain
// net/http so the fetch is unit-testable.
type cloudBuildLogFetcher struct {
	cloudBuildBaseURL string
	storageBaseURL    string
	tokenSource       oauth2.TokenSource
	client            *http.Client
	// maxBodyBytes caps one response body read. A field rather than the const
	// directly so tests can drive the oversize guard with a few bytes instead
	// of allocating the production limit.
	maxBodyBytes int64
}

// NewCloudBuildLogFetcher returns the production fetcher, authenticating with
// the GCP_KEY_JSON service-account key and falling back to Google Application
// Default Credentials when that env var is unset. It performs no I/O and
// returns no error, so factory composition stays pure; credentials are
// resolved lazily inside FetchLog so a credential problem surfaces as a
// runtime result.
func NewCloudBuildLogFetcher() BuildLogFetcher {
	return newCloudBuildLogFetcher(cloudBuildAPIBaseURL, storageAPIBaseURL, nil)
}

// newCloudBuildLogFetcher is the concrete constructor — exposed for tests
// (export_test.go) so they can point it at httptest servers and a static token.
// A nil tokenSource means "resolve GCP_KEY_JSON / ADC from the environment on
// each FetchLog".
func newCloudBuildLogFetcher(
	cloudBuildBaseURL, storageBaseURL string,
	tokenSource oauth2.TokenSource,
) *cloudBuildLogFetcher {
	return &cloudBuildLogFetcher{
		cloudBuildBaseURL: strings.TrimSuffix(cloudBuildBaseURL, "/"),
		storageBaseURL:    strings.TrimSuffix(storageBaseURL, "/"),
		tokenSource:       tokenSource,
		client:            &http.Client{Timeout: cloudBuildHTTPTimeout},
		maxBodyBytes:      maxResponseBodyBytes,
	}
}

// FetchLog returns the log text of the build named by ref. The build ID is
// validated as a UUID first — it is interpolated into two URL paths and must
// not be attacker-shaped.
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

	bucket, err := logBucketFor(ctx, ref.BuildID, build)
	if err != nil {
		return "", err
	}
	return f.fetchLogObject(ctx, ref.BuildID, bucket, token)
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
// fetcher needs to locate the log object.
type cloudBuildResource struct {
	Status     string `json:"status"`
	LogsBucket string `json:"logsBucket"`
	Options    struct {
		Logging string `json:"logging"`
	} `json:"options"`
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
	body, err := f.get(ctx, buildURL, token)
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
		"build-fix: read build id=%s status=%s logging=%s latency=%s",
		ref.BuildID, build.Status, build.Options.Logging, time.Since(start),
	)
	return build, nil
}

// fetchLogObject reads the build's log object out of its logs bucket. The
// object name is exactly "log-<buildID>.txt".
func (f *cloudBuildLogFetcher) fetchLogObject(
	ctx context.Context,
	buildID, bucket string,
	token *oauth2.Token,
) (string, error) {
	objectName := "log-" + buildID + ".txt"
	logURL := fmt.Sprintf(
		"%s/storage/v1/b/%s/o/%s?alt=media",
		f.storageBaseURL,
		url.PathEscape(bucket),
		url.PathEscape(objectName),
	)
	start := time.Now()
	body, err := f.get(ctx, logURL, token)
	if err != nil {
		return "", err
	}
	glog.V(2).Infof(
		"build-fix: fetched log id=%s bucket=%s bytes=%d latency=%s",
		buildID, bucket, len(body), time.Since(start),
	)
	return string(body), nil
}

// get performs one authenticated GET and returns the response body. A non-2xx
// status is an error naming the status, the URL, and a bounded body snippet.
func (f *cloudBuildLogFetcher) get(
	ctx context.Context,
	rawURL string,
	token *oauth2.Token,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "build request %s", rawURL)
	}
	req.Header.Set("Authorization", token.Type()+" "+token.AccessToken)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "get %s", rawURL)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the cap so an oversized body is distinguishable from
	// an exactly-maximal one, and bound the read so a huge GCS object cannot
	// exhaust memory before the caller truncates it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBodyBytes+1))
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read response body %s", rawURL)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.Errorf(
			ctx,
			"GET %s returned HTTP %d: %s",
			rawURL,
			resp.StatusCode,
			truncate(string(body), errorBodySnippetLen),
		)
	}
	if int64(len(body)) > f.maxBodyBytes {
		return nil, errors.Errorf(
			ctx,
			"GET %s body exceeds %d bytes",
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

// logBucketFor returns the logs bucket holding the build's log object. Logs
// routed away from a bucket (CLOUD_LOGGING_ONLY, its legacy alias
// STACKDRIVER_ONLY, or NONE) are not bucket-addressable — v1 covers
// bucket-addressable logs only.
func logBucketFor(ctx context.Context, buildID string, build cloudBuildResource) (string, error) {
	switch build.Options.Logging {
	case "CLOUD_LOGGING_ONLY", "STACKDRIVER_ONLY", "NONE":
		return "", errors.Errorf(
			ctx,
			"cloud build %s logs are not bucket-addressable (options.logging=%s)",
			buildID,
			build.Options.Logging,
		)
	}
	bucket := strings.TrimPrefix(build.LogsBucket, "gs://")
	if bucket == "" {
		return "", errors.Errorf(
			ctx,
			"cloud build %s has no logsBucket (options.logging=%s)",
			buildID,
			build.Options.Logging,
		)
	}
	return bucket, nil
}
