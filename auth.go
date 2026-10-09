package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	flag "github.com/spf13/pflag"
	"golang.org/x/term"
	"rsc.io/qr"
)

const tokenEnv = "SHAROSOO_CDN_TOKEN"

// Keys from https://developers.cloudflare.com/fundamentals/api/how-to/account-owned-token-template/
const tokenPermissions = `[{"key":"workers_r2","type":"edit"},{"key":"cache","type":"purge"}]`

func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sharosoo-cdn", "token"), nil
}

func apiToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv(tokenEnv)); t != "" {
		return t, nil
	}
	p, err := tokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if t := strings.TrimSpace(string(data)); err == nil && t != "" {
		return t, nil
	}
	return "", errors.New("not logged in: run `sharosoo-cdn login` (or set " + tokenEnv + ")")
}

func tokenTemplateURL() string {
	q := url.Values{
		"permissionGroupKeys": {tokenPermissions},
		"accountId":           {accountID},
		"zoneId":              {zoneID},
		"name":                {"sharosoo-cdn"},
	}
	return "https://dash.cloudflare.com/profile/api-tokens?" + q.Encode()
}

// canOpenBrowser reports whether a local graphical browser is reachable from this process.
func canOpenBrowser() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		_, err := exec.LookPath("xdg-open")
		return err == nil
	}
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

// printQR renders text as a QR code with explicit black/white ANSI colors so it scans on any terminal theme.
func printQR(w io.Writer, text string) error {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return err
	}
	const quiet = 2
	black := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	n := code.Size + 2*quiet
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := range n {
			fg, bg := "97", "107"
			if black(x, y) {
				fg = "30"
			}
			if black(x, y+1) {
				bg = "40"
			}
			fmt.Fprintf(&b, "\x1b[%s;%sm▀", fg, bg)
		}
		b.WriteString("\x1b[0m\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

type check struct {
	name string
	err  error
}

// checkToken verifies the token is active and can do everything the CLI needs.
func checkToken(token string) []check {
	c := &r2Client{token: token, http: &http.Client{Timeout: time.Minute}}
	_, userErr := c.call("GET", apiRoot+"/user/tokens/verify", nil, nil)
	active := userErr
	if userErr != nil {
		// Account-owned tokens verify against the account endpoint instead.
		if _, err := c.call("GET", fmt.Sprintf("%s/accounts/%s/tokens/verify", apiRoot, accountID), nil, nil); err == nil {
			active = nil
		}
	}
	_, r2Err := c.call("GET", apiBase+"/objects?per_page=1", nil, nil)
	// Purging a URL that was never served is harmless and proves the Cache Purge permission.
	purgeErr := c.purge([]string{baseURL + "/.sharosoo-cdn-login-probe"})
	return []check{
		{"token active", active},
		{"R2 bucket " + bucket + " (Workers R2 Storage: Edit)", r2Err},
		{"cache purge on " + strings.TrimPrefix(baseURL, "https://") + " (Cache Purge: Purge)", purgeErr},
	}
}

func reportChecks(checks []check) bool {
	ok := true
	for _, c := range checks {
		if c.err != nil {
			ok = false
			fmt.Fprintf(os.Stderr, "  FAIL  %s\n        %v\n", c.name, c.err)
		} else {
			fmt.Fprintf(os.Stderr, "  ok    %s\n", c.name)
		}
	}
	return ok
}

func readToken(withToken bool) (string, error) {
	if withToken || !term.IsTerminal(int(os.Stdin.Fd())) {
		if !withToken {
			return "", errors.New("stdin is not a terminal; pipe the token with --with-token")
		}
		data, err := io.ReadAll(bufio.NewReader(os.Stdin))
		return strings.TrimSpace(string(data)), err
	}
	fmt.Fprint(os.Stderr, "Paste the token (input hidden): ")
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return strings.TrimSpace(string(data)), err
}

func cmdLogin(args []string) error {
	fl := flag.NewFlagSet("login", flag.ContinueOnError)
	withToken := fl.Bool("with-token", false, "read the token from stdin instead of prompting")
	noBrowser := fl.Bool("no-browser", false, "never open a browser; print the URL and a QR code")
	fl.Usage = usageFor(fl, "sharosoo-cdn login [--with-token] [--no-browser]",
		"Create a Cloudflare API token in the dashboard and save it for later commands.\n"+
			"Without a local browser (SSH, servers) it prints the URL and a QR code to open on another device.")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if !*withToken {
		u := tokenTemplateURL()
		opened := false
		if !*noBrowser && canOpenBrowser() {
			opened = openBrowser(u) == nil
		}
		fmt.Fprintf(os.Stderr, "Create a token in the Cloudflare dashboard (permissions and account are pre-filled):\n"+
			"  Account > Workers R2 Storage: Edit\n"+
			"  Zone    > Cache Purge: Purge\n"+
			"Optionally narrow Zone Resources from \"All zones\" to \"Specific zone: sharosoo.com\",\n"+
			"then Continue to summary > Create Token and copy it.\n\n%s\n\n", u)
		if opened {
			fmt.Fprintln(os.Stderr, "Opened the Cloudflare dashboard in your browser.")
		} else if term.IsTerminal(int(os.Stderr.Fd())) {
			fmt.Fprintln(os.Stderr, "No local browser. Scan this on your phone or open the URL above on any device:")
			if err := printQR(os.Stderr, u); err != nil {
				return err
			}
		}
		fmt.Fprintln(os.Stderr)
	}
	token, err := readToken(*withToken)
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("empty token")
	}
	fmt.Fprintln(os.Stderr, "Checking token:")
	if !reportChecks(checkToken(token)) {
		return errors.New("token rejected; nothing saved")
	}
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved to %s\n", p)
	if os.Getenv(tokenEnv) != "" {
		fmt.Fprintf(os.Stderr, "note: %s is set and takes precedence over the saved token\n", tokenEnv)
	}
	return nil
}

func cmdLogout(args []string) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(os.Stderr, "Removed %s. Revoke the token at https://dash.cloudflare.com/profile/api-tokens if it is no longer needed.\n", p)
	return nil
}

func cmdStatus(args []string) error {
	token, err := apiToken()
	if err != nil {
		return err
	}
	source := tokenEnv
	if os.Getenv(tokenEnv) == "" {
		source, _ = tokenPath()
	}
	fmt.Fprintf(os.Stderr, "Token from %s\n", source)
	if !reportChecks(checkToken(token)) {
		return errSilent
	}
	return nil
}
