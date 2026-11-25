# Assets delivery Worker

The production Worker serves `assets.clashk.ing` from the existing private
`clashking-assets` bucket. Its checked-in configuration owns that custom domain and
enables authenticated cache purging for asset releases.

## Request contract

The public origin remains `https://assets.clashk.ing`. R2 keys remain paths relative
to `assets/`, exactly as uploaded by `build.py`. No `/img/` prefix or public R2 origin
is introduced. The entire bound bucket must contain public assets only.

| Request | Response |
| --- | --- |
| `/troops/barbarian.webp` | Original WebP, unchanged bytes |
| `/troops/barbarian.png` | Original PNG if that exact object exists |
| `/troops/barbarian.avif` | Exact AVIF if present; otherwise convert the corresponding raster source at original dimensions |
| `/troops/barbarian.avif?size=128` | AVIF contained within 128 × 128 physical pixels, maintaining aspect ratio and never upscaling |
| `/static_data.json`, `/translations.json` | Original JSON with ETag and Last-Modified |
| Any other existing file | Original bytes and content type; fonts, audio, models and viewer files remain usable |

The example stem is illustrative; request actual paths from the existing catalog.
Replace only the last extension of a known PNG/WebP/JPEG path. Only lowercase
`.avif` selects conversion. Allowed sizes are 64, 128, 256, 512 and 1024; omission
means original dimensions. The encoding recipe is fixed AVIF quality 80. A real AVIF
wins even if PNG/WebP siblings exist, and is resized only when size is supplied.

Missing AVIFs look for `.webp`, `.png`, `.jpg`, `.jpeg` siblings in that order;
exactly one must exist. Multiple candidates fail with an uncached 409 rather than
silently selecting different artwork. `check_image_sources.py` checks release
files, and the Worker also detects collisions in bucket-only objects. The initial
checkout has 2,983 raster sources, zero collisions and zero AVIF objects. There is
no runtime catalog or manifest dependency, and newly uploaded paths work without
a Worker deployment. SVG/GIF do not create synthetic AVIF aliases.

Unknown query parameters are ignored before cache lookup, preserving legacy
cache-buster URLs without creating extra variants. `size` must be valid and appear
at most once, even on originals. `v` is ignored, like other unknown parameters;
manifest refreshes mark existing local records pending instead. A valid size on an original path does not resize
it. Escaped path segments are normalized once; separators inside segments, control
characters and the reserved `__admin` namespace are rejected. Requests never fetch
an external URL. Arbitrary query parameters cannot alter quality, source or format.

Original files support GET, HEAD and conditional requests. Single byte ranges on
non-AVIF originals use private R2 reads outside the response cache, preserving
media seeking. Multiple/malformed ranges and ranges on AVIF are ignored, returning
the full representation. CORS permits public reads and exposes validators and
length/range headers; it does not expose purge credentials.

## Cache and freshness

The public Worker entrypoint uses Cloudflare's tiered Workers Cache. Cloudflare
checks the lower and upper cache tiers before invoking Worker code, so a warm hit
does not execute the Worker or make an internal Worker request. On a miss, the
entrypoint reads private R2 and streams bytes into the Images binding when AVIF
conversion is needed. There is no KV image cache, legacy Cache API, public-origin
fetch, saved R2 variant or generated AVIF committed to Git.

Before transforming a raster, the Worker inspects up to 64 KiB of container
headers and then streams the original bytes into Images. Animated WebP/APNG and
AVIF sequences return an uncached 422 so the app uses the original animation.
Unusually long unclassified headers also fall back instead of risking flattening.

The representation includes the output extension and, for AVIF, the single size
parameter. That gives at most six intended AVIF recipes per known stem. Workers
Cache may store separate entries for extra query parameters, but every variant
receives the same family tag and is cleared by the same release purge. GET/HEAD
share a body fill. Worker version isolation stays enabled, so a deployment gets a
fresh namespace. Errors are never stored and there is no negative-cache dependency.

Clients receive `public, max-age=0, must-revalidate`, while the Worker sets a
one-year `Cloudflare-CDN-Cache-Control` lifetime for every asset, including JSON.
Releases purge affected entries before publishing the new manifest, so changed
JSON becomes visible immediately without shortening the normal cache lifetime.
On a cache miss, the Worker evaluates
If-None-Match (including weak tags, lists and wildcard), then If-Modified-Since,
against the cached metadata and returns 304 without a body when appropriate.
If-None-Match takes precedence. HEAD returns the same Last-Modified, ETag and
representation length without a body. R2 upload timestamps supply Last-Modified,
rounded to HTTP seconds, preserving the existing native HEAD comparison contract.
As with the existing service, two overwrites within one second need ETag to detect.

Cloudflare's current limits page lists a 1,200-pixel AVIF limit for remote/hosted
images and Enterprise-only AVIF input. The binding section separately lists a
20 MB input limit; it does not clearly exempt the binding from the format limits.
There are 34 current raster sources exceeding 1,200 pixels. The five thumbnail
sizes are within that bound, but original-dimension AVIF on these larger sources
and resizing existing AVIFs must be verified on the target account in staging.
The Worker never deliberately downsizes a no-size request or labels a WebP/JPEG
fallback as AVIF: an unsupported conversion returns an uncached 502, and clients
can use the original URL or a supported size. Exact AVIF passthrough requires no
Images decoding. Do not promise universal full-dimension AVIF until this account
capability is established.

No periodic image revalidation or extra runtime manifest is introduced. A TTL is a
freshness limit, not a storage guarantee; Cloudflare can evict an entry earlier.
Device copies may remain old after a rare overwrite, as accepted in the plan.

## Selective release cache clearing

Releases upload changed files and delete removed files, then purge their cache
tags before publishing the new manifest. The manifest is uploaded last and its
tag is purged separately. A failed source purge prevents manifest publication.
Each raster filename family shares one hashed tag across its original formats
and every AVIF size; unrelated files retain their caches. Added paths are included
so a new real AVIF also invalidates an older generated representation.

Before releasing, provision a server-only `PURGE_TOKEN` of at least 32 characters
and set `PURGE_ENABLED=true`. Configure the GitHub variable
`ASSETS_WORKER_PURGE_URL` with the Worker's `/__admin/purge` URL and the secret
`ASSETS_WORKER_PURGE_TOKEN` with the same token. Missing configuration fails the
release before R2 writes. Never put the token in the app. The authenticated POST
accepts only explicit tags, not a whole-cache purge. Manual retries can use
`python worker_purge.py --keys troops/barbarian.webp manifest.json`.

The helper batches tags in groups of 100 and makes two passes, 60 seconds apart,
to reduce stale in-flight fills. This is not an atomic upload/cache transaction;
live staging must verify propagation. Retry a failed release with the same file
changes. The endpoint purges the public Worker's cache directly.

## Configuration and local checks

The R2 `ASSETS` binding points to the existing `clashking-assets` bucket and the
Images `IMAGES` binding performs AVIF conversion. The purge token remains a
server-side Worker secret. Translation files are generated by
`update_static.py` and uploaded to R2; translation KV is no longer used.

From `worker/`, using Node 24 or newer:

```sh
npm ci
npm test
npm run types
npm run typecheck
npm run check:layout
npm run build
```

`npm run build` is a local Wrangler dry run, not a deployment. In a restricted
checkout, set `WRANGLER_LOG_PATH=/tmp/assets-worker-wrangler.log` if needed.
From the repository root, run:

```sh
.venv/bin/python -m pytest -q tests
.venv/bin/python -m ruff check build.py worker_purge.py check_image_sources.py tests/test_worker_purge.py
```

Tests cover source mapping, size bounds/options, request normalization, real AVIF
precedence, original and range headers, JSON conditions, authentication and
public-cache scope, and selective release-triggered purges. Bindings and entrypoint
contexts are mocked; these tests do not establish live tiered-cache behavior, AVIF encoder
output, custom-domain readiness or purge propagation. No visual tests are used.

## Production migration checklist

1. Inventory the current `assets.clashk.ing` DNS/R2 custom-domain association,
   bucket name, R2 public-access/CORS settings and legacy cache rules. Save rollback
   settings. Audit that the bound bucket contains only intended public assets;
   do not repurpose a mixed/private bucket. Compare bucket-only image stems for
   collisions as well as running the repository validator.
2. Confirm account support, Images plan/quota and Wrangler 4.129.0. Create an
   isolated staging configuration using a fixture R2 bucket and a temporary Worker
   hostname; do not change the production bucket or hostname. Leave manual purge disabled. Stage small PNG/WebP, real AVIF, JSON, font and audio fixtures.
3. Deploy staging only after approval. Use HTTP/code-level probes to compare
   original byte hashes and content types; inspect transformed dimensions and
   aspect ratio/no-upscale; request each size and format in alternating order and
   inspect cache status/logs. Confirm that a cold request invokes the Worker and a
   repeated warm request is returned before the Worker runs.
4. Verify cached JSON/image reads, conditional GET and HEAD behavior. Same-path
   overwrites remain cached until the selective release purge or cache expiry.
5. Pause asset releases and other R2 writers for the production migration. Prepare
   the production Worker with the existing private R2 binding, and
   confirm readiness before detaching the existing R2 custom-domain association.
   Do not attempt simultaneous R2 and Worker ownership of `assets.clashk.ing`.
6. In the approved window, remove the old R2 domain association/DNS conflict and
   attach `assets.clashk.ing` as a Worker custom domain using the route in
   `wrangler.jsonc`. Keep the hostname and all object keys unchanged.
   Disable any remaining public R2 access only after private-binding reads work.
   No `r2.clashk.ing` hostname is needed. Clear old zone/R2 cache as part of retiring
   that serving path; releases subsequently purge the public Workers Cache by tag.
7. Repeat production read/conditional/CORS probes. Configure the GitHub Worker
   purge URL and matching token described above before resuming releases. Query
   Extra cache-buster parameters do not change the selected representation and
   every variant remains covered by the asset family's release-purge tag.
8. If cutover fails, pause releases, detach the Worker custom domain, restore the
   recorded R2 custom domain/public settings and prior DNS, and verify original
   URLs before resuming. Do not delete bucket objects or deployment history.

## Remaining app integration — no app files changed

Keep original URLs as the fallback and use AVIF only on supported decoder paths.
Use `.avif?size=<allowed size>` for raster thumbnails, selecting the smallest bound
covering physical display pixels (layout size × device scale), capped at 1024.
Keep animations on the original path unless the platform's AVIF result has been
verified to retain the intended animation; never silently substitute a thumbnail
for an animation. Keep SVG, fonts, audio and JSON URLs unchanged.

Implement a Clear image cache button that clears the native image library's memory
and disk entries, including original and AVIF URLs. For automatic refresh, retain
the latest manifest and compare source SHAs with a persistent per-file inventory.
Each inventory record has `file`, `sha`, and `pendingSha` (null when current).
Manifest checks update pending SHAs on existing records without downloading unused
images. Images use an app-owned native disk cache, with independent records for
each requested size. On display, retain the previous file while downloading the
replacement; commit its file and SHA together, then remove the previous copy.
Each size expires after 30 days without being viewed. Cleanup runs on native app
startup/resume and during image use, coalesced to at most once per minute. Viewing
one size does not renew the other sizes; JSON files are excluded from expiry.
The cache is bounded to 512 entries / 256 MiB and checks file existence, so OS
eviction cannot leave a false cache hit. Web image rendering retains browser
caching; the managed disk-cache adapter applies to the native app.

Refresh changed static sections and the selected language immediately. Save the
JSON body before committing its local SHA and clearing its matching pending SHA.
Failures retain the old file and pending SHA across app restarts. If another
manifest arrives during downloading, update `pendingSha`; completion of an older
download must not clear that newer pending version. Notice processing runs even
when the saved manifest is reused, so an interrupted update remains retryable.

For JSON, retain a cached-first/offline body and validators. Check on login/resume
and at most once per minute while foregrounded, coalescing concurrent checks.
Prefer If-None-Match conditional GET: retain the body on 304, atomically replace
body and validators on 200, and keep the last good body on network failure. Existing
HEAD/Last-Modified followed by GET remains compatible during migration.

The app starts with `app_metadata/v1/index.json`. It references two immutable
objects by SHA-256, `static-<sha>.json` with all app static sections and
`images-<sha>.json` with image paths, hashes and animation flags. Locale catalogs
stay separate under `translations/<LOCALE>.json`. The release uploads both
objects and purges changed source assets before publishing the index. Previous
hash-addressed objects remain available for clients with cached indexes.

The full `manifest.json` remains available for the Wiki and older clients. Its
`data` object still lists the individual `static_data/<section>.json` files and
translations. The app uses that path when the startup metadata is unavailable.

## Official capability and billing references (verified 2026-09-04)

- [Workers Cache configuration](https://developers.cloudflare.com/workers/cache/configuration/)
  documents entrypoint opt-in/out. The installed Wrangler schema accepts the
  configuration; `cross_version_cache` is global, not inside an export override.
- [Cache keys](https://developers.cloudflare.com/workers/cache/cache-keys/)
  include path/query, entrypoint and version, with additional header partitions.
  Extra query variants can reduce hit rate, but shared tags keep release purging correct.
- [Purge API](https://developers.cloudflare.com/workers/cache/purge/) specifies
  entrypoint scope, `purgeEverything`, result checking and rate limiting. Zone
  purge does not affect Workers Cache. Its rate limits use the Free-tier purge
  limits irrespective of the zone plan; backoff remains necessary.
- [Workers Cache pricing](https://developers.cloudflare.com/workers/cache/#pricing)
  charges requests on hits, but a hit avoids Worker execution, R2 reads and Images
  conversion. There is no additional loopback Worker request.
- [Images binding](https://developers.cloudflare.com/images/optimization/binding/)
  accepts private R2 bytes and recommends Workers Cache. It does not retain a
  response cache itself; an uncached call decodes/re-encodes again.
- [Images pricing](https://developers.cloudflare.com/images/pricing/) now bills
  binding calls per unique source/recipe per calendar month, with 5,000 included
  and $0.50 per additional 1,000 on Paid. R2-backed transformations do not add
  Images Stored/Delivered charges. If every one of the initial 2,983 source images
  uses all six recipes in one month, that is at most 17,898 requested recipes,
  roughly $6.45 beyond the included transformations, before Workers/R2 costs.
- [Image features](https://developers.cloudflare.com/images/optimization/features/#scale-down)
  confirm that square width/height with `fit: scale-down` preserves the full image
  and aspect ratio without enlargement. [Limits and formats](https://developers.cloudflare.com/images/get-started/limits/)
  still apply; unsupported or oversized conversions fail without caching errors.
