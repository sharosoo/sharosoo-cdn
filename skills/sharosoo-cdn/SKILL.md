---
name: sharosoo-cdn
description: Use when an image, screenshot, figure, font or other static file needs a public URL (articles, docs, READMEs, PRs). Uploads to cdn.sharosoo.com with the `sharosoo-cdn` CLI. Replaces the old sharosoo/image GitHub repo with jsDelivr or raw.githubusercontent URLs.
---

# sharosoo-cdn

Public static hosting backed by Cloudflare R2. URLs look like `https://cdn.sharosoo.com/<topic>/<file>`.

Never commit files to `github.com/sharosoo/image` or emit `cdn.jsdelivr.net/gh/sharosoo/image@…` / `raw.githubusercontent.com/sharosoo/image/…` URLs. That repo is archived; its files live on the CDN under the same paths.

## Upload

```sh
sharosoo-cdn put fig01.png fig02.png --prefix kimi-k3
sharosoo-cdn put ./figures --prefix goa2              # recursive, keeps relative paths
sharosoo-cdn put shot.png --key screenshots/login.png
sharosoo-cdn put ./figures --prefix goa2 --json       # [{key,url,status,bytes}]
```

stdout is one URL per line in input order; embed them verbatim. `put` already checks HTTP 200 and size, so no extra curl check is needed. Re-running is safe: identical files report `unchanged`.

- Topic: short kebab-case slug of the article or project. Screenshots go under `screenshots/`.
- Changing a published file: `sharosoo-cdn put <file> --key <same key> --force` overwrites and purges the edge cache. Browsers may show the old file for up to a day; use a new key if readers must see the change immediately.

## Other commands

```sh
sharosoo-cdn ls goa2/                     # list keys (--json available)
sharosoo-cdn url goa2/board.png           # URL without uploading
sharosoo-cdn rewrite post.md --write      # old sharosoo/image URLs → cdn.sharosoo.com
sharosoo-cdn rewrite - < in.md > out.md
sharosoo-cdn rm goa2/old.png --yes        # delete and purge; breaks pages that embed it
sharosoo-cdn status                       # check the saved token
```

## Setup

- Missing binary: `curl -fsSL https://cdn.sharosoo.com/tools/sharosoo-cdn/install.sh | sh`
- `not logged in` or a failed `status`: ask the user to run `sharosoo-cdn login` themselves. It needs them to create a token in the Cloudflare dashboard; never create, guess or print tokens.

## Limits

300 MiB per file. Everything uploaded is world-readable: never upload secrets, private photos or customer data.
