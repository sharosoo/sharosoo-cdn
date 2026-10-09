// Command sharosoo-cdn uploads files to the R2 bucket sharosoo-cdn and prints their https://cdn.sharosoo.com URLs.
package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	flag "github.com/spf13/pflag"
)

var version = "dev"

const (
	defaultAccountID = "93b84e890d66e1c2c6890b27c1e3b96b"
	defaultZoneID    = "fe8e9c88bba292dd12402cd4e3548c49"
	defaultBucket    = "sharosoo-cdn"
	defaultBaseURL   = "https://cdn.sharosoo.com"
	// Browser TTL only; the edge keeps objects for a year via a cache rule and is purged on overwrite/delete.
	defaultCacheControl = "public, max-age=86400"
	// purge_cache takes at most 30 URLs per request outside Enterprise plans.
	purgeBatch = 30
	// Cloudflare REST object upload limit; larger files need the S3 API multipart upload.
	maxUploadBytes = 300 << 20
	// Cloudflare bot protection rejects some default client user agents with 403.
	userAgent = "sharosoo-cdn"
)

var (
	accountID = envOr("SHAROSOO_CDN_ACCOUNT_ID", defaultAccountID)
	zoneID    = envOr("SHAROSOO_CDN_ZONE_ID", defaultZoneID)
	bucket    = envOr("SHAROSOO_CDN_BUCKET", defaultBucket)
	baseURL   = strings.TrimRight(envOr("SHAROSOO_CDN_BASE_URL", defaultBaseURL), "/")
	apiRoot   = "https://api.cloudflare.com/client/v4"
	apiBase   = fmt.Sprintf("%s/accounts/%s/r2/buckets/%s", apiRoot, accountID, bucket)

	skipNames = map[string]bool{".git": true, ".DS_Store": true, "Thumbs.db": true, "__pycache__": true}

	extraTypes = map[string]string{
		".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml",
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
		".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".otf": "font/otf",
		".md": "text/markdown", ".txt": "text/plain", ".html": "text/html", ".css": "text/css",
		".js": "text/javascript", ".mjs": "text/javascript", ".json": "application/json",
		".sh": "text/x-shellscript", ".pdf": "application/pdf",
		".mp4": "video/mp4", ".webm": "video/webm",
	}
	textPrefixes = []string{"text/", "application/json", "application/javascript", "image/svg+xml"}

	legacyURL = regexp.MustCompile(`https://(?:` +
		`cdn\.jsdelivr\.net/gh/sharosoo/image@[^/\s"')]+/` +
		`|raw\.githubusercontent\.com/sharosoo/image/(?:refs/heads/)?[^/\s"')]+/` +
		`|github\.com/sharosoo/image/(?:raw|blob)/[^/\s"')]+/` +
		`)`)
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type r2Client struct {
	token string
	http  *http.Client
}

type r2Object struct {
	Key          string          `json:"key"`
	ETag         string          `json:"etag"`
	Size         json.RawMessage `json:"size"`
	LastModified string          `json:"last_modified"`
}

func (o r2Object) bytes() int64 {
	s := strings.Trim(string(o.Size), `"`)
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func newR2() (*r2Client, error) {
	t, err := apiToken()
	if err != nil {
		return nil, err
	}
	return &r2Client{token: t, http: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (c *r2Client) call(method, u string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Errors json.RawMessage `json:"errors"`
		}
		detail := string(data)
		if json.Unmarshal(data, &env) == nil && env.Errors != nil {
			detail = string(env.Errors)
		}
		if len(detail) > 300 {
			detail = detail[:300]
		}
		return nil, fmt.Errorf("%s %s -> HTTP %d: %s", method, u, resp.StatusCode, detail)
	}
	return data, nil
}

func (c *r2Client) list(prefix string) ([]r2Object, error) {
	var all []r2Object
	cursor := ""
	for {
		q := url.Values{"per_page": {"1000"}}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		data, err := c.call("GET", apiBase+"/objects?"+q.Encode(), nil, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Result     []r2Object `json:"result"`
			ResultInfo struct {
				Cursor      string `json:"cursor"`
				IsTruncated bool   `json:"is_truncated"`
			} `json:"result_info"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Result...)
		if !page.ResultInfo.IsTruncated || page.ResultInfo.Cursor == "" {
			return all, nil
		}
		cursor = page.ResultInfo.Cursor
	}
}

func objectURL(key string) string {
	return apiBase + "/objects/" + url.PathEscape(key)
}

func (c *r2Client) put(key string, data []byte, contentType, cacheControl string) error {
	_, err := c.call("PUT", objectURL(key), data, map[string]string{"Content-Type": contentType, "Cache-Control": cacheControl})
	return err
}

func (c *r2Client) delete(key string) error {
	_, err := c.call("DELETE", objectURL(key), nil, nil)
	return err
}

// purge evicts URLs from the Cloudflare edge so overwritten or deleted keys stop serving old bytes.
func (c *r2Client) purge(urls []string) error {
	for start := 0; start < len(urls); start += purgeBatch {
		end := min(start+purgeBatch, len(urls))
		body, _ := json.Marshal(map[string][]string{"files": urls[start:end]})
		if _, err := c.call("POST", fmt.Sprintf("%s/zones/%s/purge_cache", apiRoot, zoneID), body, map[string]string{"Content-Type": "application/json"}); err != nil {
			return fmt.Errorf("cache purge failed (token needs Zone > Cache Purge): %w", err)
		}
	}
	return nil
}

func publicURL(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return baseURL + "/" + strings.Join(parts, "/")
}

func contentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	ct := extraTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	if !strings.Contains(ct, "charset") {
		for _, p := range textPrefixes {
			if strings.HasPrefix(ct, p) {
				return ct + "; charset=utf-8"
			}
		}
	}
	return ct
}

func normalizeKey(key string) (string, error) {
	k := strings.TrimLeft(strings.TrimSpace(key), "/")
	if k == "" || strings.HasSuffix(k, "/") {
		return "", fmt.Errorf("invalid key: %q", key)
	}
	for _, part := range strings.Split(k, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid key: %q", key)
		}
	}
	return k, nil
}

type item struct {
	path string
	key  string
}

func joinKey(prefix, rel string) string {
	if prefix == "" {
		return rel
	}
	return prefix + "/" + rel
}

func collect(paths []string, prefix, exactKey string) ([]item, error) {
	prefix = strings.Trim(prefix, "/")
	var items []item
	for _, raw := range paths {
		info, err := os.Stat(raw)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", raw)
		}
		if info.IsDir() {
			err := filepath.WalkDir(raw, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if skipNames[d.Name()] && p != raw {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				rel, err := filepath.Rel(raw, p)
				if err != nil {
					return err
				}
				k, err := normalizeKey(joinKey(prefix, filepath.ToSlash(rel)))
				if err != nil {
					return err
				}
				items = append(items, item{p, k})
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		name := exactKey
		if name == "" {
			name = joinKey(prefix, filepath.Base(raw))
		}
		k, err := normalizeKey(name)
		if err != nil {
			return nil, err
		}
		items = append(items, item{raw, k})
	}
	if exactKey != "" && len(items) != 1 {
		return nil, errors.New("--key needs exactly one file")
	}
	seen := map[string]string{}
	for _, it := range items {
		if prev, ok := seen[it.key]; ok {
			return nil, fmt.Errorf("two files map to %s: %s and %s", it.key, prev, it.path)
		}
		seen[it.key] = it.path
	}
	return items, nil
}

func commonPrefix(items []item) string {
	if len(items) == 0 {
		return ""
	}
	p := items[0].key
	for _, it := range items[1:] {
		for !strings.HasPrefix(it.key, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

// verify HEADs the public URL. Retries cover purge propagation after an overwrite.
func verify(u string, size int) error {
	var last error
	for attempt := range 5 {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		req, _ := http.NewRequest("HEAD", u, nil)
		req.Header.Set("User-Agent", userAgent)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return fmt.Errorf("verify failed: %s: %w", u, err)
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode != 200:
			last = fmt.Errorf("verify failed: %s -> HTTP %d", u, resp.StatusCode)
		case resp.ContentLength >= 0 && resp.ContentLength != int64(size):
			last = fmt.Errorf("verify failed: %s serves %d bytes, expected %d", u, resp.ContentLength, size)
		default:
			return nil
		}
	}
	return last
}

type putResult struct {
	Key    string `json:"key"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Bytes  int    `json:"bytes"`
}

func cmdPut(args []string) error {
	fl := flag.NewFlagSet("put", flag.ContinueOnError)
	prefix := fl.StringP("prefix", "p", "", "key prefix, usually a topic dir such as goa2 or kimi-k3")
	key := fl.StringP("key", "k", "", "exact key for a single file")
	force := fl.BoolP("force", "f", false, "overwrite keys that exist with different content")
	ctype := fl.String("content-type", "", "override the detected Content-Type")
	cache := fl.String("cache-control", defaultCacheControl, "Cache-Control header")
	dry := fl.BoolP("dry-run", "n", false, "show what would be uploaded")
	noVerify := fl.Bool("no-verify", false, "skip the public HEAD check after upload")
	verbose := fl.BoolP("verbose", "v", false, "prefix each URL with its status")
	asJSON := fl.Bool("json", false, "print a JSON array of {key,url,status,bytes}")
	fl.Usage = usageFor(fl, "sharosoo-cdn put <files|dirs>... [flags]", "Upload files or directories (recursive, relative paths kept) and print public URLs in input order.")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() == 0 {
		fl.Usage()
		return errUsage
	}
	items, err := collect(fl.Args(), *prefix, *key)
	if err != nil {
		return err
	}
	c, err := newR2()
	if err != nil {
		return err
	}
	existing := map[string]r2Object{}
	if len(items) > 0 {
		objs, err := c.list(commonPrefix(items))
		if err != nil {
			return err
		}
		for _, o := range objs {
			existing[o.Key] = o
		}
	}
	var results []putResult
	for _, it := range items {
		data, err := os.ReadFile(it.path)
		if err != nil {
			return err
		}
		if len(data) > maxUploadBytes {
			return fmt.Errorf("%s: %d bytes exceeds the 300 MiB REST upload limit", it.path, len(data))
		}
		u := publicURL(it.key)
		sum := md5.Sum(data)
		old, exists := existing[it.key]
		var status string
		switch {
		case exists && old.ETag == hex.EncodeToString(sum[:]):
			status = "unchanged"
		case exists && !*force:
			return fmt.Errorf("%s already exists with different content; pass --force to overwrite (the edge cache is purged, browsers may keep the old file until Cache-Control expires)", it.key)
		default:
			status = "uploaded"
			if exists {
				status = "overwritten"
			}
			if !*dry {
				ct := *ctype
				if ct == "" {
					ct = contentType(it.path)
				}
				if err := c.put(it.key, data, ct, *cache); err != nil {
					return err
				}
				if exists {
					if err := c.purge([]string{u}); err != nil {
						return err
					}
				}
				if !*noVerify {
					if err := verify(u, len(data)); err != nil {
						return err
					}
				}
			}
		}
		if *dry {
			status = "would-" + status
		}
		results = append(results, putResult{it.key, u, status, len(data)})
		if !*asJSON {
			if *verbose {
				fmt.Printf("%-12s %s\n", status, u)
			} else {
				fmt.Println(u)
			}
		}
	}
	if *asJSON {
		return printJSON(results)
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdLs(args []string) error {
	fl := flag.NewFlagSet("ls", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	fl.Usage = usageFor(fl, "sharosoo-cdn ls [prefix] [--json]", "List keys under a prefix.")
	if err := fl.Parse(args); err != nil {
		return err
	}
	c, err := newR2()
	if err != nil {
		return err
	}
	objs, err := c.list(strings.TrimLeft(fl.Arg(0), "/"))
	if err != nil {
		return err
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
	if *asJSON {
		type row struct {
			Key      string `json:"key"`
			URL      string `json:"url"`
			Bytes    int64  `json:"bytes"`
			Uploaded string `json:"uploaded"`
		}
		rows := make([]row, 0, len(objs))
		for _, o := range objs {
			rows = append(rows, row{o.Key, publicURL(o.Key), o.bytes(), o.LastModified})
		}
		return printJSON(rows)
	}
	for _, o := range objs {
		fmt.Printf("%10d  %s\n", o.bytes(), o.Key)
	}
	return nil
}

func cmdRm(args []string) error {
	fl := flag.NewFlagSet("rm", flag.ContinueOnError)
	yes := fl.Bool("yes", false, "confirm deletion")
	fl.Usage = usageFor(fl, "sharosoo-cdn rm <keys>... --yes", "Delete keys.")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() == 0 {
		fl.Usage()
		return errUsage
	}
	if !*yes {
		return errors.New("rm breaks every page that embeds these URLs; pass --yes to confirm")
	}
	c, err := newR2()
	if err != nil {
		return err
	}
	var urls []string
	for _, raw := range fl.Args() {
		k, err := normalizeKey(raw)
		if err != nil {
			return err
		}
		if err := c.delete(k); err != nil {
			return err
		}
		fmt.Println("deleted", k)
		urls = append(urls, publicURL(k))
	}
	return c.purge(urls)
}

func cmdURL(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sharosoo-cdn url <keys>...")
	}
	for _, raw := range args {
		k, err := normalizeKey(raw)
		if err != nil {
			return err
		}
		fmt.Println(publicURL(k))
	}
	return nil
}

func rewriteText(s string) (string, int) {
	n := len(legacyURL.FindAllStringIndex(s, -1))
	return legacyURL.ReplaceAllLiteralString(s, baseURL+"/"), n
}

func cmdRewrite(args []string) error {
	fl := flag.NewFlagSet("rewrite", flag.ContinueOnError)
	write := fl.BoolP("write", "w", false, "edit files in place")
	check := fl.Bool("check", false, "exit 1 if any legacy URL is found")
	fl.Usage = usageFor(fl, "sharosoo-cdn rewrite <files>... [-w] [--check] | sharosoo-cdn rewrite -", "Replace legacy sharosoo/image GitHub/jsDelivr URLs with CDN URLs. `-` filters stdin to stdout.")
	if err := fl.Parse(args); err != nil {
		return err
	}
	files := fl.Args()
	if len(files) == 0 {
		fl.Usage()
		return errUsage
	}
	if len(files) == 1 && files[0] == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		out, _ := rewriteText(string(data))
		_, err = os.Stdout.WriteString(out)
		return err
	}
	total := 0
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		out, n := rewriteText(string(data))
		if n == 0 {
			continue
		}
		total += n
		fmt.Printf("%4d  %s\n", n, name)
		if *write {
			info, _ := os.Stat(name)
			if err := os.WriteFile(name, []byte(out), info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	if total > 0 && !*write {
		fmt.Fprintf(os.Stderr, "%d legacy URL(s) found; rerun with --write to apply\n", total)
	}
	if *check && total > 0 {
		return errSilent
	}
	return nil
}

var (
	errUsage  = errors.New("usage")
	errSilent = errors.New("")
)

func usageFor(fl *flag.FlagSet, synopsis, summary string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "usage: %s\n%s\n\n%s", synopsis, summary, fl.FlagUsages())
	}
}

const mainUsage = `usage: sharosoo-cdn <command> [args]

Upload to R2 bucket %s served at %s.

commands:
  login                 create and save an API token (opens the Cloudflare dashboard or prints a QR code)
  logout                delete the saved token
  status                check the token and its permissions
  put <files|dirs>...   upload and print public URLs
  ls [prefix]           list keys
  url <keys>...         print public URLs without uploading
  rm <keys>... --yes    delete keys
  rewrite <files>...    replace legacy sharosoo/image URLs with CDN URLs
  version               print version

Run "sharosoo-cdn <command> --help" for flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, mainUsage, bucket, baseURL)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"put": cmdPut, "ls": cmdLs, "rm": cmdRm, "url": cmdURL, "rewrite": cmdRewrite,
		"login": cmdLogin, "logout": cmdLogout, "status": cmdStatus,
	}
	name, args := os.Args[1], os.Args[2:]
	switch name {
	case "version", "--version", "-V":
		fmt.Println("sharosoo-cdn", version)
		return
	case "help", "-h", "--help":
		fmt.Fprintf(os.Stdout, mainUsage, bucket, baseURL)
		return
	}
	run, ok := cmds[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "sharosoo-cdn: unknown command %q\n\n"+mainUsage, name, bucket, baseURL)
		os.Exit(2)
	}
	if err := run(args); err != nil {
		switch {
		case errors.Is(err, flag.ErrHelp):
			return
		case errors.Is(err, errUsage):
			os.Exit(2)
		case errors.Is(err, errSilent):
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "sharosoo-cdn:", err)
		os.Exit(1)
	}
}
