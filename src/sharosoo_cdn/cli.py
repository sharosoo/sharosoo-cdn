"""cdn — upload files to R2 bucket sharosoo-cdn and print their https://cdn.sharosoo.com URLs."""

from __future__ import annotations

import argparse
import hashlib
import json
import mimetypes
import os
import re
import subprocess
import sys
import tomllib
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path

ACCOUNT_ID = os.environ.get("CDN_ACCOUNT_ID", "93b84e890d66e1c2c6890b27c1e3b96b")
BUCKET = os.environ.get("CDN_BUCKET", "sharosoo-cdn")
BASE_URL = os.environ.get("CDN_BASE_URL", "https://cdn.sharosoo.com").rstrip("/")
API = f"https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/r2/buckets/{BUCKET}"
DEFAULT_CACHE_CONTROL = "public, max-age=31536000, immutable"
# Cloudflare REST object upload limit; larger files need the S3 API multipart upload.
MAX_UPLOAD_BYTES = 300 * 1024 * 1024
SKIP_NAMES = {".git", ".DS_Store", "Thumbs.db", "__pycache__"}

EXTRA_TYPES = {
    ".webp": "image/webp",
    ".avif": "image/avif",
    ".svg": "image/svg+xml",
    ".woff2": "font/woff2",
    ".woff": "font/woff",
    ".ttf": "font/ttf",
    ".otf": "font/otf",
    ".md": "text/markdown",
    ".mjs": "text/javascript",
    ".webm": "video/webm",
    ".mp4": "video/mp4",
    ".json": "application/json",
}
TEXT_TYPES = ("text/", "application/json", "application/javascript", "image/svg+xml")

LEGACY_URL = re.compile(
    r"https://(?:"
    r"cdn\.jsdelivr\.net/gh/sharosoo/image@[^/\s\"')]+/"
    r"|raw\.githubusercontent\.com/sharosoo/image/(?:refs/heads/)?[^/\s\"')]+/"
    r"|github\.com/sharosoo/image/(?:raw|blob)/[^/\s\"')]+/"
    r")"
)


class CdnError(Exception):
    pass


def _wrangler_config() -> Path:
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return Path(base) / ".wrangler" / "config" / "default.toml"


def _refresh_wrangler_login() -> None:
    # Any authenticated wrangler call rotates an expired OAuth token and rewrites the config file.
    env = {**os.environ, "CLOUDFLARE_ACCOUNT_ID": ACCOUNT_ID}
    for runner in (["bunx", "wrangler"], ["npx", "--yes", "wrangler"]):
        try:
            done = subprocess.run([*runner, "r2", "bucket", "list"], env=env, capture_output=True, text=True)
        except FileNotFoundError:
            continue
        if done.returncode == 0:
            return
        raise CdnError(f"wrangler token refresh failed (run: bunx wrangler login)\n{done.stderr.strip()}")
    raise CdnError("token expired and neither bunx nor npx is available to refresh it")


def api_token() -> str:
    token = os.environ.get("CDN_CLOUDFLARE_API_TOKEN")
    if token:
        return token
    path = _wrangler_config()
    if not path.exists():
        raise CdnError("no CDN_CLOUDFLARE_API_TOKEN and no wrangler login (run: bunx wrangler login)")
    cfg = tomllib.loads(path.read_text())
    expires = datetime.fromisoformat(cfg.get("expiration_time", "1970-01-01T00:00:00+00:00").replace("Z", "+00:00"))
    if expires <= datetime.now(timezone.utc) + timedelta(minutes=2):
        _refresh_wrangler_login()
        cfg = tomllib.loads(path.read_text())
    return cfg["oauth_token"]


class R2:
    def __init__(self) -> None:
        self.token = api_token()

    def _call(self, method: str, url: str, data: bytes | None = None, headers: dict[str, str] | None = None) -> dict:
        req = urllib.request.Request(url, method=method, data=data, headers={"Authorization": f"Bearer {self.token}", **(headers or {})})
        try:
            with urllib.request.urlopen(req, timeout=300) as resp:
                body = resp.read()
        except urllib.error.HTTPError as err:
            body = err.read()
            try:
                errors = json.loads(body).get("errors")
            except ValueError:
                errors = body[:300]
            raise CdnError(f"{method} {url} -> HTTP {err.code}: {errors}") from None
        return json.loads(body) if body else {}

    def list(self, prefix: str = "") -> list[dict]:
        objects: list[dict] = []
        cursor = None
        while True:
            query = {"per_page": "1000"}
            if prefix:
                query["prefix"] = prefix
            if cursor:
                query["cursor"] = cursor
            page = self._call("GET", f"{API}/objects?{urllib.parse.urlencode(query)}")
            objects.extend(page.get("result") or [])
            info = page.get("result_info") or {}
            cursor = info.get("cursor")
            if not info.get("is_truncated") or not cursor:
                return objects

    def put(self, key: str, data: bytes, content_type: str, cache_control: str) -> None:
        self._call(
            "PUT",
            f"{API}/objects/{urllib.parse.quote(key)}",
            data=data,
            headers={"Content-Type": content_type, "Cache-Control": cache_control},
        )

    def delete(self, key: str) -> None:
        self._call("DELETE", f"{API}/objects/{urllib.parse.quote(key)}")


def public_url(key: str) -> str:
    return f"{BASE_URL}/{urllib.parse.quote(key)}"


def content_type(path: Path) -> str:
    ctype = EXTRA_TYPES.get(path.suffix.lower()) or mimetypes.guess_type(path.name)[0] or "application/octet-stream"
    if ctype.startswith(TEXT_TYPES) and "charset" not in ctype:
        ctype += "; charset=utf-8"
    return ctype


def normalize_key(key: str) -> str:
    key = key.strip().lstrip("/")
    if not key or key.endswith("/") or any(part in ("", ".", "..") for part in key.split("/")):
        raise CdnError(f"invalid key: {key!r}")
    return key


@dataclass
class Item:
    path: Path
    key: str


def collect(paths: list[str], prefix: str, key: str | None) -> list[Item]:
    prefix = prefix.strip("/")
    items: list[Item] = []
    for raw in paths:
        path = Path(raw)
        if path.is_dir():
            for file in sorted(path.rglob("*")):
                rel = file.relative_to(path)
                if file.is_file() and not any(part in SKIP_NAMES for part in rel.parts):
                    items.append(Item(file, normalize_key(f"{prefix}/{rel.as_posix()}" if prefix else rel.as_posix())))
        elif path.is_file():
            name = key if key else (f"{prefix}/{path.name}" if prefix else path.name)
            items.append(Item(path, normalize_key(name)))
        else:
            raise CdnError(f"not found: {raw}")
    if key and len(items) != 1:
        raise CdnError("--key needs exactly one file")
    seen: dict[str, Path] = {}
    for item in items:
        if item.key in seen:
            raise CdnError(f"two files map to {item.key}: {seen[item.key]} and {item.path}")
        seen[item.key] = item.path
    return items


def verify(url: str, size: int) -> None:
    req = urllib.request.Request(url, method="HEAD", headers={"User-Agent": "sharosoo-cdn"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            length = resp.headers.get("Content-Length")
    except urllib.error.HTTPError as err:
        raise CdnError(f"verify failed: {url} -> HTTP {err.code}") from None
    if length is not None and int(length) != size:
        raise CdnError(f"verify failed: {url} serves {length} bytes, expected {size}")


def cmd_put(args: argparse.Namespace) -> int:
    items = collect(args.paths, args.prefix, args.key)
    r2 = R2()
    common = os.path.commonprefix([i.key for i in items])
    existing = {o["key"]: o for o in r2.list(common)} if items else {}
    results = []
    for item in items:
        data = item.path.read_bytes()
        if len(data) > MAX_UPLOAD_BYTES:
            raise CdnError(f"{item.path}: {len(data)} bytes exceeds the 300 MiB REST upload limit")
        url = public_url(item.key)
        old = existing.get(item.key)
        if old and old.get("etag") == hashlib.md5(data).hexdigest():
            status = "unchanged"
        elif old and not args.force:
            raise CdnError(
                f"{item.key} already exists with different content. Upload under a new key; "
                "--force overwrites, but caches keep serving the old bytes for up to a year."
            )
        else:
            status = "overwritten" if old else "uploaded"
            if not args.dry_run:
                r2.put(item.key, data, args.content_type or content_type(item.path), args.cache_control)
                if not args.no_verify:
                    verify(url, len(data))
        results.append({"key": item.key, "url": url, "status": status if not args.dry_run else f"would-{status}", "bytes": len(data)})
        if not args.json:
            print(url if not args.verbose else f"{results[-1]['status']:<12} {url}")
    if args.json:
        print(json.dumps(results, ensure_ascii=False, indent=2))
    return 0


def cmd_ls(args: argparse.Namespace) -> int:
    objects = R2().list(args.prefix.lstrip("/"))
    if args.json:
        print(json.dumps([{"key": o["key"], "url": public_url(o["key"]), "bytes": int(o.get("size", 0)), "uploaded": o.get("last_modified")} for o in objects], ensure_ascii=False, indent=2))
    else:
        for o in objects:
            print(f"{int(o.get('size', 0)):>10}  {o['key']}")
    return 0


def cmd_rm(args: argparse.Namespace) -> int:
    if not args.yes:
        raise CdnError("rm breaks every page that embeds these URLs; pass --yes to confirm")
    r2 = R2()
    for key in args.keys:
        r2.delete(normalize_key(key))
        print(f"deleted {key}")
    return 0


def cmd_url(args: argparse.Namespace) -> int:
    for key in args.keys:
        print(public_url(normalize_key(key)))
    return 0


def rewrite_text(text: str) -> tuple[str, int]:
    return LEGACY_URL.subn(BASE_URL + "/", text)


def cmd_rewrite(args: argparse.Namespace) -> int:
    if args.files == ["-"]:
        out, _ = rewrite_text(sys.stdin.read())
        sys.stdout.write(out)
        return 0
    total = 0
    for name in args.files:
        path = Path(name)
        text = path.read_text()
        out, count = rewrite_text(text)
        if count:
            total += count
            print(f"{count:>4}  {path}")
            if args.write:
                path.write_text(out)
    if total and not args.write:
        print(f"{total} legacy URL(s) found; rerun with --write to apply", file=sys.stderr)
    return 1 if args.check and total else 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="cdn", description=f"Upload to R2 bucket {BUCKET} served at {BASE_URL}.")
    sub = parser.add_subparsers(dest="command", required=True)

    put = sub.add_parser("put", help="upload files or directories and print public URLs")
    put.add_argument("paths", nargs="+", help="files or directories (directories upload recursively, keeping relative paths)")
    put.add_argument("-p", "--prefix", default="", help="key prefix, usually a topic dir such as goa2 or kimi-k3")
    put.add_argument("-k", "--key", help="exact key for a single file")
    put.add_argument("-f", "--force", action="store_true", help="overwrite keys that exist with different content")
    put.add_argument("--content-type", help="override the detected Content-Type")
    put.add_argument("--cache-control", default=DEFAULT_CACHE_CONTROL)
    put.add_argument("-n", "--dry-run", action="store_true", help="show what would be uploaded")
    put.add_argument("--no-verify", action="store_true", help="skip the public HEAD check after upload")
    put.add_argument("-v", "--verbose", action="store_true", help="prefix each URL with its status")
    put.add_argument("--json", action="store_true", help="print a JSON array of {key,url,status,bytes}")
    put.set_defaults(func=cmd_put)

    ls = sub.add_parser("ls", help="list keys under a prefix")
    ls.add_argument("prefix", nargs="?", default="")
    ls.add_argument("--json", action="store_true")
    ls.set_defaults(func=cmd_ls)

    rm = sub.add_parser("rm", help="delete keys")
    rm.add_argument("keys", nargs="+")
    rm.add_argument("--yes", action="store_true", help="confirm deletion")
    rm.set_defaults(func=cmd_rm)

    url = sub.add_parser("url", help="print the public URL of keys")
    url.add_argument("keys", nargs="+")
    url.set_defaults(func=cmd_url)

    rw = sub.add_parser("rewrite", help="replace legacy sharosoo/image GitHub/jsDelivr URLs with CDN URLs")
    rw.add_argument("files", nargs="+", help="text files, or - for stdin to stdout")
    rw.add_argument("-w", "--write", action="store_true", help="edit files in place")
    rw.add_argument("--check", action="store_true", help="exit 1 if any legacy URL is found")
    rw.set_defaults(func=cmd_rewrite)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except CdnError as err:
        print(f"cdn: {err}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
