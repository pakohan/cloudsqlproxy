package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
)

// Auth loads Application Default Credentials and reports when they stop
// working, e.g. because the refresh token expired or reauth is required.
type Auth struct {
	mu    sync.Mutex
	creds *auth.Credentials
	// gen increases on every reload so errors from stale credentials are ignored.
	gen atomic.Int64
	// reported is set once a failure was reported, so it isn't reported again
	// until the credentials are reloaded.
	reported atomic.Bool

	OnInvalid func(err error)
}

// Credentials returns the current credentials, loading them if necessary.
func (a *Auth) Credentials() (*auth.Credentials, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.creds != nil {
		return a.creds, nil
	}
	if err := a.loadLocked(); err != nil {
		a.report(a.gen.Load(), err)
		return nil, err
	}
	return a.creds, nil
}

// Reload re-reads the credentials from disk, e.g. after a new login.
func (a *Auth) Reload() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadLocked()
}

func (a *Auth) loadLocked() error {
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		return fmt.Errorf("load application default credentials: %w", err)
	}
	gen := a.gen.Add(1)
	a.reported.Store(false)
	a.creds = auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider:          &watchedTokenProvider{tp: creds.TokenProvider, auth: a, gen: gen},
		JSON:                   creds.JSON(),
		ProjectIDProvider:      auth.CredentialsPropertyFunc(creds.ProjectID),
		QuotaProjectIDProvider: auth.CredentialsPropertyFunc(creds.QuotaProjectID),
		UniverseDomainProvider: auth.CredentialsPropertyFunc(creds.UniverseDomain),
	})
	return nil
}

// Check fetches a token so expired credentials are noticed before a client
// tries to connect.
func (a *Auth) Check(ctx context.Context) error {
	creds, err := a.Credentials()
	if err != nil {
		return err
	}
	_, err = creds.Token(ctx)
	return err
}

func (a *Auth) report(gen int64, err error) {
	if gen != a.gen.Load() || a.reported.Swap(true) {
		return
	}
	log.Printf("Credentials are no longer valid: %v", err)
	if a.OnInvalid != nil {
		a.OnInvalid(err)
	}
}

type watchedTokenProvider struct {
	tp   auth.TokenProvider
	auth *Auth
	gen  int64
}

func (w *watchedTokenProvider) Token(ctx context.Context) (*auth.Token, error) {
	tok, err := w.tp.Token(ctx)
	if isCredentialError(err) {
		w.auth.report(w.gen, err)
	}
	return tok, err
}

// isCredentialError reports whether the token endpoint rejected the
// credentials, as opposed to a temporary or network failure.
func isCredentialError(err error) bool {
	var authErr *auth.Error
	if !errors.As(err, &authErr) || authErr.Response == nil {
		return false
	}
	sc := authErr.Response.StatusCode
	return sc == http.StatusBadRequest || sc == http.StatusUnauthorized
}

// SignIn runs `gcloud auth application-default login`, which opens a browser
// for the login. It blocks until gcloud exits or ctx is cancelled.
func SignIn(ctx context.Context) error {
	gcloud, err := findGcloud()
	if err != nil {
		return err
	}
	return runLogged(ctx, gcloud, "auth", "application-default", "login")
}

// runLogged runs a command and writes its output to the log.
func runLogged(ctx context.Context, name string, args ...string) error {
	log.Printf("Running %s %s", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			log.Printf("gcloud: %s", line)
		}
	}
	if err := sc.Err(); err != nil {
		log.Printf("Reading gcloud output: %v", err)
	}
	return cmd.Wait()
}

// findGcloud locates the gcloud binary. Apps started from Finder don't inherit
// the shell's PATH, so common install locations are checked as well.
func findGcloud() (string, error) {
	if p, err := exec.LookPath("gcloud"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/opt/homebrew/bin/gcloud",
		"/usr/local/bin/gcloud",
		"/opt/homebrew/share/google-cloud-sdk/bin/gcloud",
		"/usr/local/share/google-cloud-sdk/bin/gcloud",
		"/opt/homebrew/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin/gcloud",
		filepath.Join(home, "google-cloud-sdk", "bin", "gcloud"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("gcloud not found; install the Google Cloud SDK")
}
