# cdn

`cdn` uploads files to Cloudflare R2 bucket `sharosoo-cdn` and prints their public URLs on `https://cdn.sharosoo.com`.

The bucket, custom domain and CORS are defined in [sharosoo/infra](https://github.com/sharosoo/infra) (`cloudflare/cdn.tf`). This repo is only the client.

## Install

Single static binary (Go, no runtime) for Linux/macOS/Windows on amd64/arm64, hosted on the CDN itself:

```sh
curl -fsSL https://cdn.sharosoo.com/tools/cdn/install.sh | sh     # → ~/.local/bin/cdn
CDN_VERSION=0.2.0 CDN_INSTALL_DIR=/usr/local/bin sh -c "$(curl -fsSL https://cdn.sharosoo.com/tools/cdn/install.sh)"
```

The installer picks `cdn-<os>-<arch>` from `https://cdn.sharosoo.com/tools/cdn/v<version>/` and checks it against `SHA256SUMS`. Windows: download `cdn-windows-amd64.exe` from the same directory.

From a checkout: `go build -o ~/.local/bin/cdn .`.

### Authentication

1. `CDN_CLOUDFLARE_API_TOKEN`: a Cloudflare API token with *Workers R2 Storage: Edit* on account `93b84e89…`. Use this on servers, CI and machines without wrangler.
2. Otherwise the wrangler OAuth login (`bunx wrangler login`). An expired token is refreshed by running wrangler once (`bunx` or `npx` must be on `PATH`).

## Usage

```sh
cdn put fig.png --prefix my-article          # https://cdn.sharosoo.com/my-article/fig.png
cdn put ./figures --prefix my-article        # recursive, keeps relative paths
cdn put shot.png --key screenshots/x.png     # exact key
cdn put ./figures --prefix my-article --json --dry-run
cdn ls my-article/
cdn url my-article/fig.png
cdn rewrite notes.md --write                 # legacy sharosoo/image URLs → cdn.sharosoo.com
cdn rm my-article/fig.png --yes
```

### Behavior

|Case|Result|
|---|---|
|Key absent|upload, then HEAD the public URL (200 and matching size)|
|Key present, same MD5|`unchanged`, nothing uploaded|
|Key present, different content|error; `--force` overwrites|
|Directory input|every file below it, `.git`/`__pycache__`/`.DS_Store` skipped|

Objects get `Cache-Control: public, max-age=31536000, immutable` (override with `--cache-control`). Treat keys as immutable: publish changed content under a new key. The same applies after `cdn rm`: re-uploading a deleted key can serve the old bytes from the edge cache. `Content-Type` comes from the extension (`webp`, `avif`, `svg`, `woff2`, `md` covered); text types get `charset=utf-8`.

`cdn rewrite` maps these legacy URLs to `https://cdn.sharosoo.com/<path>`:

- `https://cdn.jsdelivr.net/gh/sharosoo/image@<ref>/<path>`
- `https://raw.githubusercontent.com/sharosoo/image/<ref>/<path>`
- `https://github.com/sharosoo/image/{raw,blob}/<ref>/<path>`

## Configuration

|Env|Default|
|---|---|
|`CDN_CLOUDFLARE_API_TOKEN`|wrangler OAuth token|
|`CDN_ACCOUNT_ID`|`93b84e890d66e1c2c6890b27c1e3b96b`|
|`CDN_BUCKET`|`sharosoo-cdn`|
|`CDN_BASE_URL`|`https://cdn.sharosoo.com`|

## Release

```sh
scripts/release.sh 0.3.0
```

Needs a clean tree. Runs `go vet` and `go test`, cross-builds six targets with `CGO_ENABLED=0`, uploads them with the freshly built binary to `tools/cdn/v<version>/` plus `SHA256SUMS`, updates `tools/cdn/latest` and `tools/cdn/install.sh` (60 s cache), then tags and pushes `v<version>`.

## For agents

- [`skills/cdn/SKILL.md`](skills/cdn/SKILL.md): agent skill, symlinked into `~/.agents/skills`, `~/.claude/skills` and `~/.hermes/skills`.
- [`llms.txt`](llms.txt): short reference for LLM context.
