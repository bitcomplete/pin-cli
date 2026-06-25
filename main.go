// Command pin is the CLI companion to pin-api: sign in via Google + share
// HTML files. Designed to be invoked both by humans and by LLM agents
// running on the human's laptop — `pin share <file>` "just works" once
// the human has run `pin login` once.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	clientID     = "pin-cli"
	defaultHost  = "https://pin.bitcomplete.dev"
	envHost      = "PIN_HOST"
	envAgent     = "PIN_AGENT"
	tokenLeeway  = 30 * time.Second
	pollInterval = 2 * time.Second
)

// Version is overridden at build time via goreleaser's ldflags
// (`-X main.Version=...`). Stays "dev" for `go install` builds.
var Version = "dev"

type creds struct {
	Issuer       string `json:"issuer"`
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	AccessExpAt  int64  `json:"access_token_expires_at"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "login":
		os.Exit(runLogin(os.Args[2:]))
	case "logout":
		os.Exit(runLogout(os.Args[2:]))
	case "share":
		os.Exit(runShare(os.Args[2:]))
	case "get":
		os.Exit(runGet(os.Args[2:]))
	case "publish":
		os.Exit(runPublish(os.Args[2:]))
	case "unpublish":
		os.Exit(runUnpublish(os.Args[2:]))
	case "components":
		os.Exit(runComponents(os.Args[2:]))
	case "whoami":
		os.Exit(runWhoami(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "pin: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`pin — share HTML files behind Google SSO.

Usage:
  pin login [--device]      Sign in via Google. Use --device on SSH/headless.
  pin share <file>          Upload an HTML or MDX file. Prints the share URL.
  pin get <id-or-url>       Fetch a share's MDX source to stdout. --html for
                            the rendered HTML form.
  pin publish <id-or-url>   Make a share publicly viewable (no login) via a
                            capability link. --ttl <dur> sets the lifetime
                            (default 7d, max 30d). Prints the public URL.
  pin unpublish <token|url> Revoke a public link before it expires.
  pin components            List MDX components, grouped by category.
  pin components get <Name> Show one component's props + example.
  pin components dump       Print every component's full detail.
                            All three support --json for machine output.
  pin whoami                Show current logged-in user.
  pin logout                Revoke the current refresh token + forget local creds.
  pin version               Print the CLI version.

Environment:
  PIN_HOST    Override pin's base URL (default https://pin.bitcomplete.dev).
  PIN_AGENT   Override the actor string sent with auth requests
              (default: "pin-cli@<hostname>").`)
}

func host() string {
	if h := os.Getenv(envHost); h != "" {
		return strings.TrimRight(h, "/")
	}
	return defaultHost
}

func agent() string {
	if a := os.Getenv(envAgent); a != "" {
		return a
	}
	hn, _ := os.Hostname()
	if hn == "" {
		hn = "unknown"
	}
	return "pin-cli@" + hn
}

// ----- login -----

func runLogin(args []string) int {
	device := false
	for _, a := range args {
		if a == "--device" {
			device = true
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var c *creds
	var err error
	if device {
		c, err = loginDevice(ctx)
	} else {
		c, err = loginLoopback(ctx)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin login: %v\n", err)
		return 1
	}
	if err := saveCreds(c); err != nil {
		fmt.Fprintf(os.Stderr, "pin login: save credentials: %v\n", err)
		return 1
	}
	fmt.Printf("logged in as %s\n", c.Email)
	return 0
}

func loginLoopback(ctx context.Context) (*creds, error) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("loopback listen: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/", port)

	stateRaw := make([]byte, 16)
	_, _ = readRandom(stateRaw)
	state := base64.RawURLEncoding.EncodeToString(stateRaw)

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"share:write"},
		"actor":                 {agent()},
	}
	authURL := host() + "/oauth/authorize?" + q.Encode()

	type result struct {
		code string
		err  error
	}
	out := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			out <- result{err: errors.New("state mismatch")}
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			out <- result{err: errors.New("no code in callback")}
			http.Error(w, "no code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h1>pin: signed in</h1><p>You can close this tab.</p>"))
		out <- result{code: code}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	fmt.Printf("Opening browser to %s ...\n", host())
	_ = openBrowser(authURL)
	fmt.Println("If the browser didn't open, paste this URL:")
	fmt.Println("  " + authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-out:
		if res.err != nil {
			return nil, res.err
		}
		return exchangeCode(ctx, res.code, verifier, redirectURI)
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timed out waiting for browser redirect")
	}
}

func loginDevice(ctx context.Context) (*creds, error) {
	form := url.Values{
		"client_id": {clientID},
		"scope":     {"share:write"},
		"actor":     {agent()},
	}
	resp, err := http.PostForm(host()+"/oauth/device_authorization", form)
	if err != nil {
		return nil, fmt.Errorf("device_authorization: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device_authorization http %d: %s", resp.StatusCode, body)
	}
	var dr struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, err
	}
	interval := time.Duration(dr.Interval) * time.Second
	if interval == 0 {
		interval = pollInterval
	}
	fmt.Printf("Go to %s and enter: %s\nWaiting for approval ...\n", dr.VerificationURI, dr.UserCode)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		c, err := pollDevice(ctx, dr.DeviceCode)
		if err == nil {
			return c, nil
		}
		if errors.Is(err, errPending) {
			continue
		}
		return nil, err
	}
}

var errPending = errors.New("authorization_pending")

func pollDevice(ctx context.Context, deviceCode string) (*creds, error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
	}
	resp, err := http.PostForm(host()+"/oauth/token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 {
		return tokensFromBody(body)
	}
	var oe struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &oe)
	if oe.Error == "authorization_pending" {
		return nil, errPending
	}
	return nil, fmt.Errorf("token: %s", oe.Error)
}

func exchangeCode(ctx context.Context, code, verifier, redirectURI string) (*creds, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
	}
	resp, err := http.PostForm(host()+"/oauth/token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token http %d: %s", resp.StatusCode, body)
	}
	return tokensFromBody(body)
}

func tokensFromBody(body []byte) (*creds, error) {
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	email, err := emailFromJWT(tr.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("decode access token: %w", err)
	}
	return &creds{
		Issuer:       host(),
		Email:        email,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		AccessExpAt:  time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).Unix(),
	}, nil
}

// emailFromJWT cracks the `sub` claim out of a JWT without verifying — we
// trust the server who just issued it.
func emailFromJWT(tok string) (string, error) {
	parts := strings.SplitN(tok, ".", 3)
	if len(parts) != 3 {
		return "", errors.New("malformed jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var c struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", err
	}
	return c.Sub, nil
}

// ----- logout -----

func runLogout(_ []string) int {
	c, err := loadCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin logout: not logged in\n")
		return 1
	}
	form := url.Values{"token": {c.RefreshToken}}
	_, _ = http.PostForm(c.Issuer+"/oauth/revoke", form) // best-effort
	if err := deleteCreds(); err != nil {
		fmt.Fprintf(os.Stderr, "pin logout: %v\n", err)
		return 1
	}
	fmt.Println("logged out")
	return 0
}

// ----- whoami -----

func runWhoami(_ []string) int {
	c, err := loadCreds()
	if err != nil {
		fmt.Fprintln(os.Stderr, "not logged in")
		return 1
	}
	fmt.Println(c.Email)
	return 0
}

// ----- share -----

func runShare(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pin share <file>")
		return 2
	}
	body, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin share: %v\n", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	c, err := loadCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin share: not logged in. Run `pin login`.\n")
		return 1
	}
	c, err = ensureFreshAccess(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin share: refresh: %v\n", err)
		return 1
	}

	// Content-Type from the file extension. MDX uploads let the server
	// store the raw .mdx alongside the rendered HTML, which downstream
	// agents can fetch via GET /p/{id}.mdx instead of paying tokens for
	// the rendered markup.
	contentType := "text/html; charset=utf-8"
	if strings.HasSuffix(strings.ToLower(args[0]), ".mdx") {
		contentType = "text/mdx; charset=utf-8"
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, c.Issuer+"/api/pins", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("X-Agent", agent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin share: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	rbody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "pin share: http %d: %s\n", resp.StatusCode, rbody)
		return 1
	}
	var out struct {
		URL    string `json:"url"`
		MDXURL string `json:"mdx_url"`
	}
	if err := json.Unmarshal(rbody, &out); err != nil {
		fmt.Fprintf(os.Stderr, "pin share: parse: %v\nbody: %s\n", err, rbody)
		return 1
	}
	if out.MDXURL != "" {
		fmt.Fprintf(os.Stderr, "mdx: %s\n", out.MDXURL)
	}
	fmt.Println(out.URL)
	return 0
}

// ----- get -----

// runGet fetches a share by id (or full URL) and writes its contents
// to stdout. Default to the .mdx representation since that's the
// agent-cheap form; --html fetches the rendered HTML. Designed to be
// piped:
//
//	pin get 01HX... | claude --some-flag
//	pin get https://pin.bitcomplete.dev/p/01HX... > /tmp/plan.mdx
func runGet(args []string) int {
	wantHTML := false
	var idArg string
	for _, a := range args {
		switch {
		case a == "--html":
			wantHTML = true
		case a == "--mdx":
			wantHTML = false
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "pin get: unknown flag %q\n", a)
			return 2
		case idArg == "":
			idArg = a
		default:
			fmt.Fprintln(os.Stderr, "pin get: too many arguments")
			return 2
		}
	}
	if idArg == "" {
		fmt.Fprintln(os.Stderr, "usage: pin get <id-or-url> [--html]")
		return 2
	}

	id, hostFromURL := parseShareRef(idArg)
	if id == "" {
		fmt.Fprintf(os.Stderr, "pin get: not a share id or URL: %q\n", idArg)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	c, err := loadCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin get: not logged in. Run `pin login`.\n")
		return 1
	}
	c, err = ensureFreshAccess(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin get: refresh: %v\n", err)
		return 1
	}

	base := hostFromURL
	if base == "" {
		base = host()
	}
	path := "/p/" + id
	if !wantHTML {
		path += ".mdx"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("X-Agent", agent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin get: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// Distinguish "no such share" from "no MDX representation".
		body, _ := io.ReadAll(resp.Body)
		if !wantHTML && strings.Contains(string(body), "MDX representation") {
			fmt.Fprintf(os.Stderr, "pin get: %s was uploaded as HTML; try --html\n", id)
		} else {
			fmt.Fprintf(os.Stderr, "pin get: not found: %s\n", id)
		}
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "pin get: http %d: %s\n", resp.StatusCode, body)
		return 1
	}
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "pin get: stream: %v\n", err)
		return 1
	}
	return 0
}

// ----- publish / unpublish -----

// runPublish flips an existing share public by minting a capability
// token, then prints a login-free URL. This is the deliberate "safety
// off" action — invoking it is the conscious step; the server still
// requires the explicit confirm sentinel we send below.
func runPublish(args []string) int {
	var idArg, ttl string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--ttl":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "pin publish: --ttl needs a value, e.g. --ttl 24h")
				return 2
			}
			i++
			ttl = args[i]
		case strings.HasPrefix(a, "--ttl="):
			ttl = strings.TrimPrefix(a, "--ttl=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "pin publish: unknown flag %q\n", a)
			return 2
		case idArg == "":
			idArg = a
		default:
			fmt.Fprintln(os.Stderr, "pin publish: too many arguments")
			return 2
		}
	}
	if idArg == "" {
		fmt.Fprintln(os.Stderr, "usage: pin publish <id-or-url> [--ttl 7d]")
		return 2
	}

	id, hostFromURL := parseShareRef(idArg)
	if id == "" {
		fmt.Fprintf(os.Stderr, "pin publish: not a share id or URL: %q\n", idArg)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	c, err := loadCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin publish: not logged in. Run `pin login`.\n")
		return 1
	}
	c, err = ensureFreshAccess(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin publish: refresh: %v\n", err)
		return 1
	}

	base := hostFromURL
	if base == "" {
		base = host()
	}
	reqBody := map[string]string{"confirm": "publish-public"}
	if ttl != "" {
		reqBody["ttl"] = ttl
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/pins/"+id+"/public", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("X-Agent", agent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin publish: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	rbody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "pin publish: http %d: %s\n", resp.StatusCode, strings.TrimSpace(string(rbody)))
		return 1
	}
	var out struct {
		PublicURL string `json:"public_url"`
		ExpiresAt string `json:"expires_at"`
		TTLDays   int    `json:"ttl_days"`
	}
	if err := json.Unmarshal(rbody, &out); err != nil {
		fmt.Fprintf(os.Stderr, "pin publish: parse: %v\nbody: %s\n", err, rbody)
		return 1
	}
	// Warn loudly on stderr; the URL itself stays the only thing on stdout
	// so it's safe to pipe/capture.
	fmt.Fprintf(os.Stderr, "⚠ public — anyone with this link can view it, no login. Expires %s (%dd). Revoke with `pin unpublish`.\n", out.ExpiresAt, out.TTLDays)
	fmt.Println(out.PublicURL)
	return 0
}

// runUnpublish revokes a public link. Accepts the raw token or the full
// public URL (from which it lifts the ?token= value).
func runUnpublish(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pin unpublish <token-or-url>")
		return 2
	}
	token, hostFromURL := parsePublicRef(args[0])
	if token == "" {
		fmt.Fprintf(os.Stderr, "pin unpublish: no token in %q\n", args[0])
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	c, err := loadCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin unpublish: not logged in. Run `pin login`.\n")
		return 1
	}
	c, err = ensureFreshAccess(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin unpublish: refresh: %v\n", err)
		return 1
	}

	base := hostFromURL
	if base == "" {
		base = host()
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/pins/public/revoke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("X-Agent", agent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin unpublish: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		rbody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "pin unpublish: http %d: %s\n", resp.StatusCode, strings.TrimSpace(string(rbody)))
		return 1
	}
	fmt.Fprintln(os.Stderr, "revoked — the public link no longer works")
	return 0
}

// parsePublicRef extracts the capability token from either a bare token
// or a full public URL like
// "https://pin.bitcomplete.dev/public/p/{id}?token=…". Returns (token,
// host) where host is "" unless a URL was passed.
func parsePublicRef(ref string) (token, hostFromURL string) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil || u.Host == "" {
			return "", ""
		}
		return u.Query().Get("token"), u.Scheme + "://" + u.Host
	}
	return ref, ""
}

// parseShareRef accepts a bare ULID, a "p/{id}" path, or a full URL
// like "https://pin.bitcomplete.dev/p/{id}". Returns (id, host) where
// host is "" unless a URL was passed (in which case it overrides
// PIN_HOST so cross-instance fetches work).
func parseShareRef(ref string) (id string, hostFromURL string) {
	ref = strings.TrimSpace(ref)
	// Strip .mdx / .html suffix if user pasted one.
	for _, suf := range []string{".mdx", ".html"} {
		ref = strings.TrimSuffix(ref, suf)
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil || u.Host == "" {
			return "", ""
		}
		hostFromURL = u.Scheme + "://" + u.Host
		ref = strings.TrimPrefix(u.Path, "/")
	}
	ref = strings.TrimPrefix(ref, "p/")
	if ref == "" || strings.ContainsAny(ref, "/?# ") {
		return "", ""
	}
	return ref, hostFromURL
}

// ensureFreshAccess refreshes the access token if it's within the leeway
// window of expiring.
func ensureFreshAccess(ctx context.Context, c *creds) (*creds, error) {
	if time.Now().Unix() < c.AccessExpAt-int64(tokenLeeway.Seconds()) {
		return c, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {clientID},
	}
	resp, err := http.PostForm(c.Issuer+"/oauth/token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("refresh http %d: %s", resp.StatusCode, body)
	}
	newC, err := tokensFromBody(body)
	if err != nil {
		return nil, err
	}
	if err := saveCreds(newC); err != nil {
		return nil, err
	}
	return newC, nil
}

// ----- PKCE + random -----

func pkcePair() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := readRandom(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256Sum(verifier)
	challenge = base64.RawURLEncoding.EncodeToString(sum)
	return verifier, challenge, nil
}

// ----- browser open -----

func openBrowser(rawURL string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{rawURL}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		cmd, args = "xdg-open", []string{rawURL}
	}
	return exec.Command(cmd, args...).Start()
}

// ----- credentials persistence -----

// We prefer OS keychain; fall back to a 0600 file at ~/.config/pin/credentials.json.
// The keychain key namespaces by issuer so users hopping between dev/prod
// instances each have their own row.

func credsFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pin", "credentials.json"), nil
}

func saveCreds(c *creds) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := keychainSet(c.Issuer, string(b)); err == nil {
		// also keep a 0600 file as belt+suspenders for headless reads (CI).
	}
	p, err := credsFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func loadCreds() (*creds, error) {
	issuer := host()
	if raw, err := keychainGet(issuer); err == nil && raw != "" {
		var c creds
		if err := json.Unmarshal([]byte(raw), &c); err == nil && c.Email != "" {
			return &c, nil
		}
	}
	p, err := credsFilePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c creds
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func deleteCreds() error {
	issuer := host()
	_ = keychainDel(issuer)
	p, err := credsFilePath()
	if err != nil {
		return err
	}
	_ = os.Remove(p)
	return nil
}
