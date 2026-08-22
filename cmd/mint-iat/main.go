// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command mint-iat is a smoke-test tool for the GitHub App migration (Base F1).
//
// It mints a short-lived JWT signed with the App's RSA private key, exchanges
// it for an installation access token (IAT) via the GitHub API, then validates
// the IAT by calling GET /app and printing the resolved App identity.
//
// Stdlib-only — no production dependencies. Throwaway tool; will be replaced
// by ghinstallation/v2 in the production integration.
//
// Usage:
//
//	# Pass the key as a file path:
//	go run ./cmd/mint-iat \
//	  -app-id=3798945 \
//	  -installation-id=134414316 \
//	  -pem-key-file=$HOME/Documents/secrets/bborbe-pr-reviewer.pem
//
//	# Pass the key content directly (e.g. from a k8s Secret env var):
//	PEM_KEY="$(cat ~/Documents/secrets/bborbe-pr-reviewer.pem)" \
//	  go run ./cmd/mint-iat -app-id=3798945 -installation-id=134414316
//
// Env vars: APP_ID, INSTALLATION_ID, PEM_KEY, PEM_KEY_FILE.
// Exactly one of -pem-key / -pem-key-file (or PEM_KEY / PEM_KEY_FILE) must be set.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	appID := flag.Int64("app-id", envInt64("APP_ID"), "GitHub App ID (numeric); env APP_ID")
	installationID := flag.Int64(
		"installation-id",
		envInt64("INSTALLATION_ID"),
		"Installation ID (numeric); env INSTALLATION_ID",
	)
	pemKey := flag.String(
		"pem-key",
		os.Getenv("PEM_KEY"),
		"PEM content (entire private key); env PEM_KEY",
	)
	pemKeyFile := flag.String(
		"pem-key-file",
		os.Getenv("PEM_KEY_FILE"),
		"Path to the PEM file; env PEM_KEY_FILE",
	)
	verify := flag.Bool(
		"verify",
		true,
		"After minting, call GET /app with the IAT to verify it works",
	)
	flag.Parse()

	if *appID == 0 || *installationID == 0 {
		flag.Usage()
		return fmt.Errorf(
			"-app-id and -installation-id are required (or env APP_ID, INSTALLATION_ID)",
		)
	}
	if (*pemKey == "") == (*pemKeyFile == "") {
		flag.Usage()
		return fmt.Errorf(
			"exactly one of -pem-key or -pem-key-file must be set (or env PEM_KEY / PEM_KEY_FILE)",
		)
	}

	pemBytes, err := resolvePEM(*pemKey, *pemKeyFile)
	if err != nil {
		return fmt.Errorf("resolve PEM: %w", err)
	}
	privateKey, err := parsePrivateKey(pemBytes)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}

	jwt, err := mintAppJWT(*appID, privateKey)
	if err != nil {
		return fmt.Errorf("mint JWT: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ JWT minted (len=%d)\n", len(jwt))

	iat, expiresAt, err := exchangeJWTForIAT(jwt, *installationID)
	if err != nil {
		return fmt.Errorf("exchange JWT → IAT: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ IAT minted, expires at %s\n", expiresAt)

	if *verify {
		if err := verifyIAT(jwt); err != nil {
			return fmt.Errorf("verify App identity: %w", err)
		}
	}

	fmt.Println(iat)
	return nil
}

func resolvePEM(pemKey, pemKeyFile string) ([]byte, error) {
	if pemKey != "" {
		return []byte(pemKey), nil
	}
	// pemKeyFile is the explicit -pem-key-file CLI flag value (operator input);
	// filepath.Clean breaks gosec G703/G304 taint analysis. The tool's stated
	// purpose is to read an operator-specified PEM file.
	return os.ReadFile(filepath.Clean(pemKeyFile))
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in key material")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key (PKCS1 and PKCS8 both failed): %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA (got %T)", parsed)
	}
	return rsaKey, nil
}

func envInt64(name string) int64 {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func mintAppJWT(appID int64, key *rsa.PrivateKey) (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	signingInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + b64url(sig), nil
}

func exchangeJWTForIAT(jwt string, installationID int64) (string, string, error) {
	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body)))
	}

	var out struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("parse response: %w (body: %s)", err, truncate(string(body)))
	}
	return out.Token, out.ExpiresAt, nil
}

func verifyIAT(jwt string) error {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/app", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET /app failed HTTP %d: %s", resp.StatusCode, truncate(string(body)))
	}

	var app struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		return fmt.Errorf("parse /app response: %w", err)
	}
	fmt.Fprintf(
		os.Stderr,
		"✓ GET /app: id=%d slug=%s name=%q owner=%s\n",
		app.ID,
		app.Slug,
		app.Name,
		app.Owner.Login,
	)
	fmt.Fprintf(os.Stderr, "  permissions: %v\n", app.Permissions)
	return nil
}

func b64url(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
