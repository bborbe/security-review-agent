// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bitbucket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bborbe/errors"
)

// PRBranches holds the source and target branch names of a pull request.
type PRBranches struct {
	Source string
	Target string
}

// Client interacts with Bitbucket Server REST API v1.0.
//
//counterfeiter:generate -o ../../mocks/bitbucket-client.go --fake-name BitbucketClient . Client
type Client interface {
	GetPRBranches(ctx context.Context, host, project, repo string, number int) (PRBranches, error)
	PostComment(ctx context.Context, host, project, repo string, number int, body string) error
	Approve(ctx context.Context, host, project, repo string, number int) error
	NeedsWork(ctx context.Context, host, project, repo string, number int, userSlug string) error
}

// NewClient creates a Client that uses the Bitbucket Server REST API.
// Token is used for Bearer authentication.
func NewClient(token string) Client {
	return &httpClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type httpClient struct {
	token      string
	httpClient *http.Client
}

type prResponse struct {
	FromRef struct {
		DisplayID string `json:"displayId"`
	} `json:"fromRef"`
	ToRef struct {
		DisplayID string `json:"displayId"`
	} `json:"toRef"`
}

type commentRequest struct {
	Text string `json:"text"`
}

type participantRequest struct {
	User     participantUser `json:"user"`
	Approved bool            `json:"approved"`
	Status   string          `json:"status"`
}

type participantUser struct {
	Slug string `json:"slug"`
}

// GetPRBranches fetches the source and target branch names for a pull request.
func (c *httpClient) GetPRBranches(
	ctx context.Context,
	host, project, repo string,
	number int,
) (PRBranches, error) {
	url := c.buildURL(host, fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d",
		project, repo, number))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return PRBranches{}, errors.Wrapf(ctx, err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return PRBranches{}, errors.Wrapf(ctx, err, "request failed for %s", host)
	}
	defer resp.Body.Close()

	if err := checkResponseStatus(ctx, resp, host, project, repo, number); err != nil {
		return PRBranches{}, errors.Wrap(ctx, err, "check response status")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return PRBranches{}, errors.Wrapf(ctx, err, "failed to read response body")
	}

	var prResp prResponse
	if err := json.Unmarshal(body, &prResp); err != nil {
		return PRBranches{}, errors.Wrapf(ctx, err, "failed to parse response")
	}

	if prResp.FromRef.DisplayID == "" {
		return PRBranches{}, errors.Errorf(ctx, "PR response missing source branch")
	}
	if prResp.ToRef.DisplayID == "" {
		return PRBranches{}, errors.Errorf(ctx, "PR response missing target branch")
	}

	return PRBranches{
		Source: prResp.FromRef.DisplayID,
		Target: prResp.ToRef.DisplayID,
	}, nil
}

// PostComment posts a comment on a pull request.
func (c *httpClient) PostComment(
	ctx context.Context,
	host, project, repo string,
	number int,
	body string,
) error {
	url := c.buildURL(
		host,
		fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/comments",
			project, repo, number),
	)

	commentReq := commentRequest{Text: body}
	jsonData, err := json.Marshal(commentReq)
	if err != nil {
		return errors.Wrapf(ctx, err, "failed to marshal comment")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return errors.Wrapf(ctx, err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrapf(ctx, err, "request failed for %s", host)
	}
	defer resp.Body.Close()

	if err := checkResponseStatus(ctx, resp, host, project, repo, number); err != nil {
		return errors.Wrap(ctx, err, "check response status")
	}

	return nil
}

// Approve approves a pull request.
func (c *httpClient) Approve(
	ctx context.Context,
	host, project, repo string,
	number int,
) error {
	url := c.buildURL(
		host,
		fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/approve",
			project, repo, number),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return errors.Wrapf(ctx, err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrapf(ctx, err, "request failed for %s", host)
	}
	defer resp.Body.Close()

	if err := checkApproveResponseStatus(ctx, resp, host, project, repo, number); err != nil {
		return errors.Wrap(ctx, err, "check response status")
	}

	return nil
}

// NeedsWork marks a pull request as needing work.
func (c *httpClient) NeedsWork(
	ctx context.Context,
	host, project, repo string,
	number int,
	userSlug string,
) error {
	url := c.buildURL(
		host,
		fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/participants/%s",
			project, repo, number, userSlug),
	)

	participantReq := participantRequest{
		User: participantUser{
			Slug: userSlug,
		},
		Approved: false,
		Status:   "NEEDS_WORK",
	}

	jsonData, err := json.Marshal(participantReq)
	if err != nil {
		return errors.Wrapf(ctx, err, "failed to marshal participant request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(jsonData))
	if err != nil {
		return errors.Wrapf(ctx, err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrapf(ctx, err, "request failed for %s", host)
	}
	defer resp.Body.Close()

	if err := checkResponseStatus(ctx, resp, host, project, repo, number); err != nil {
		return errors.Wrap(ctx, err, "check response status")
	}

	return nil
}

// buildURL constructs the full URL with scheme detection.
// Only loopback addresses are allowed to use http://; all other hosts are upgraded to https://.
func (c *httpClient) buildURL(host, path string) string {
	if strings.HasPrefix(host, "https://") {
		return host + path
	}
	if strings.HasPrefix(host, "http://") {
		// Allow http only for loopback (test servers); upgrade everything else.
		u := strings.TrimPrefix(host, "http://")
		if strings.HasPrefix(u, "127.0.0.1") || strings.HasPrefix(u, "localhost") ||
			strings.HasPrefix(u, "[::1]") {
			return host + path
		}
		return "https://" + u + path
	}
	return "https://" + host + path
}

// checkResponseStatus validates HTTP response status and returns appropriate errors.
// Token is intentionally excluded from error messages for security.
func checkResponseStatus(
	ctx context.Context,
	resp *http.Response,
	host, project, repo string,
	number int,
) error {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusUnauthorized:
		return errors.Errorf(ctx, "authentication failed for %s", host)
	case http.StatusForbidden:
		return errors.Errorf(ctx, "insufficient permissions for %s", host)
	case http.StatusNotFound:
		return errors.Errorf(ctx, "PR not found: %s/projects/%s/repos/%s/pull-requests/%d",
			host, project, repo, number)
	default:
		return errors.Errorf(ctx, "unexpected status %d from %s", resp.StatusCode, host)
	}
}

// checkApproveResponseStatus validates HTTP response status for approve requests.
// Treats 409 Conflict (already approved) as success.
// Token is intentionally excluded from error messages for security.
func checkApproveResponseStatus(
	ctx context.Context,
	resp *http.Response,
	host, project, repo string,
	number int,
) error {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return nil
	case http.StatusUnauthorized:
		return errors.Errorf(ctx, "authentication failed for %s", host)
	case http.StatusForbidden:
		return errors.Errorf(ctx, "insufficient permissions for %s", host)
	case http.StatusNotFound:
		return errors.Errorf(ctx, "PR not found: %s/projects/%s/repos/%s/pull-requests/%d",
			host, project, repo, number)
	default:
		return errors.Errorf(ctx, "unexpected status %d from %s", resp.StatusCode, host)
	}
}
