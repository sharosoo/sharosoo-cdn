---
name: cdn
description: Use when an image, screenshot, figure, font or other static file needs a public URL (articles, artifact-hub/arthub posts, READMEs, PRs). Uploads to cdn.sharosoo.com (Cloudflare R2) with the `cdn` CLI. Replaces the old sharosoo/image GitHub repo + jsDelivr/raw.githubusercontent pattern.
---

# cdn.sharosoo.com uploads

Public static hosting for 정혁 (sharosoo). Bucket `sharosoo-cdn` (Cloudflare R2) is served at `https://cdn.sharosoo.com/<key>`.

Do NOT commit images to `github.com/sharosoo/image` and do NOT emit `cdn.jsdelivr.net/gh/sharosoo/image@…` or `raw.githubusercontent.com/sharosoo/image/…` URLs. That repo is a frozen archive; every file in it is already on the CDN under the same path.

## Upload

```sh
cdn put fig01.png fig02.png --prefix kimi-k3        # → https://cdn.sharosoo.com/kimi-k3/fig01.png …
cdn put ./out/figures --prefix goa2                 # directory: recursive, keeps relative paths
cdn put shot.png --key screenshots/2026/login.png   # exact key for one file
cdn put ./figures --prefix goa2 --json              # [{key,url,status,bytes}] for scripts
```

stdout is one URL per line, in input order. Embed those URLs verbatim.

- Key layout: `<topic>/<file>`. Topic = short kebab-case slug of the article/project (`grok-4-7-ko`, `guards-of-atlantis-2`). Screenshots go under `screenshots/`.
- After each upload the CLI HEAD-checks the public URL and fails if it is not 200 with the right size. No separate curl check is needed.
- Re-running the same upload is safe: identical files report `unchanged`.

## Changing a published file

Objects are cached for a year (`Cache-Control: public, max-age=31536000, immutable`). To change an image, upload it under a NEW key (`fig02-v2.png`) and update the embed. `cdn put` refuses to overwrite a key with different content; `--force` overwrites but readers may keep seeing the old bytes.

## Other commands

```sh
cdn ls goa2/                       # list keys under a prefix (--json for machine output)
cdn url goa2/board.png             # print a URL without uploading
cdn rewrite post.md --write        # replace legacy sharosoo/image jsDelivr/GitHub URLs with cdn.sharosoo.com
cdn rewrite - < in.md > out.md     # same, stdin → stdout
cdn rm goa2/old.png --yes          # delete (breaks every page that embeds it)
```

## Auth

Uses the wrangler OAuth login (`bunx wrangler login`) and refreshes it automatically; `CDN_CLOUDFLARE_API_TOKEN` overrides it with a token that has R2 write on account `93b84e89…`. If `cdn` is missing: `uv tool install --editable ~/workspaces/sharosoo/cdn`.

## Limits

- 300 MiB per file (Cloudflare REST upload limit).
- Public: never upload secrets, private photos or customer data. Anything uploaded is world-readable.
- CORS allows GET/HEAD from any origin, so `fetch()`, canvas and web fonts work cross-origin.
