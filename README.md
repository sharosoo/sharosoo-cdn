# sharosoo-cdn

Upload files to `https://cdn.sharosoo.com` (Cloudflare R2 bucket `sharosoo-cdn`) and print their URLs.

Infrastructure: [sharosoo/infra](https://github.com/sharosoo/infra).

## Install

```sh
curl -fsSL https://cdn.sharosoo.com/tools/sharosoo-cdn/install.sh | sh
```

Static binary for Linux and macOS (amd64, arm64), installed to `~/.local/bin/sharosoo-cdn`. Override with `SHAROSOO_CDN_INSTALL_DIR`, pin with `SHAROSOO_CDN_VERSION`. Windows: `https://cdn.sharosoo.com/tools/sharosoo-cdn/v<version>/sharosoo-cdn-windows-<arch>.exe`.

## Log in

```sh
sharosoo-cdn login
```

Opens the Cloudflare dashboard with a pre-filled token form (Account › Workers R2 Storage: Edit, Zone › Cache Purge: Purge). Create the token, paste it back, and the CLI checks it and saves it to `<user config dir>/sharosoo-cdn/token` (mode 0600).

Without a browser (SSH, servers, containers), `login` prints the URL and a QR code; create the token on any other device and paste it. Non-interactive:

```sh
echo "$TOKEN" | sharosoo-cdn login --with-token
SHAROSOO_CDN_TOKEN=... sharosoo-cdn put ...   # env overrides the saved token
```

`sharosoo-cdn status` re-checks the token; `sharosoo-cdn logout` deletes it.

## Usage

```sh
sharosoo-cdn put fig.png --prefix my-post        # https://cdn.sharosoo.com/my-post/fig.png
sharosoo-cdn put ./figures --prefix my-post      # recursive, keeps relative paths
sharosoo-cdn put shot.png --key screenshots/x.png
sharosoo-cdn put fig.png --prefix my-post --force   # overwrite and purge the edge cache
sharosoo-cdn ls my-post/
sharosoo-cdn url my-post/fig.png
sharosoo-cdn rm my-post/fig.png --yes            # delete and purge
sharosoo-cdn rewrite post.md --write             # old sharosoo/image GitHub/jsDelivr URLs → cdn.sharosoo.com
```

- `put` verifies each public URL (HTTP 200, matching size). Unchanged files are skipped. Different content under an existing key needs `--force`.
- `Cache-Control` defaults to `public, max-age=86400`. The edge caches for a year and is purged on `--force` and `rm`, so browsers may show an old file for up to a day.
- `--json` prints `[{key,url,status,bytes}]`; `--dry-run` uploads nothing.
- 300 MiB per file. Everything uploaded is public.

## Configuration

|Env|Default|
|---|---|
|`SHAROSOO_CDN_TOKEN`|saved token|
|`SHAROSOO_CDN_ACCOUNT_ID`|`93b84e890d66e1c2c6890b27c1e3b96b`|
|`SHAROSOO_CDN_ZONE_ID`|`fe8e9c88bba292dd12402cd4e3548c49`|
|`SHAROSOO_CDN_BUCKET`|`sharosoo-cdn`|
|`SHAROSOO_CDN_BASE_URL`|`https://cdn.sharosoo.com`|

## Release

```sh
scripts/release.sh 0.4.0
```

Requires a clean tree, Go and a logged-in CLI. Tests, cross-builds six targets, uploads them to `tools/sharosoo-cdn/v<version>/` with `SHA256SUMS`, updates `latest` and `install.sh`, then tags `v<version>` and pushes the tag.

## Agents

[`skills/sharosoo-cdn/SKILL.md`](skills/sharosoo-cdn/SKILL.md) is an agent skill; [`llms.txt`](llms.txt) is a short reference.
