# Asset Extraction

This repository includes a Go CLI for turning Supercell `.sc` and `.sctx` files into images and export manifests.

## Repository Layout

- [`main.go`](main.go): CLI entrypoint
- [`internal/render/`](internal/render): export pipeline, batching, manifests, and image encoding
- [`internal/sc/`](internal/sc): `.sc` and texture parsing
- [`internal/sc3d/`](internal/sc3d): embedded browser viewer for hero and skin models

## Prerequisites

On macOS 11 or later, a normal cgo-enabled build uses Metal for ASTC decoding and complete scene composition, including soft and nested masks. The renderer uses its portable CPU implementation when Metal is unavailable; Metal rendering errors are reported instead of silently changing compositors mid-export.

`astcenc` remains an automatic fallback when Metal is unavailable or does not support the texture. Installing it on macOS is optional but useful if this tool will run on varied hardware:

```bash
brew install astc-encoder
```

Linux and other non-macOS builds require `astcenc` on `PATH` (or under `lib/`):

```bash
sudo snap install astc-encoder
```

Still and animated WebP encoding is built into the Go executable. You do not need `cwebp`, `img2webp`, or FFmpeg.

On macOS, composed animated sceneries default to hardware HEVC in a QuickTime `.mov`. Use `--scenery-format webp` when portability matters more than export time; `auto` falls back to WebP if a hardware HEVC encoder is unavailable.

Build the CLI:

```bash
go build -o sc-export .
```

## Capabilities

- Decode and process `.sc` files
- Decode raw `.sctx` texture files
- Filter exports to specific asset names
- Route specific exports to exact output paths
- Compose mapped scenery foreground and base files into camera-cropped still or animated output
- Emit a `manifest.json` with exported files, metadata, and skipped entries

## Commands

- `sc-export export [flags] INPUT`: export one `.sc`/`.sctx` file or a directory
- `sc-export texture-root [flags] ROOT`: recursively export every `.sctx` file under a directory
- `sc-export sc-root [flags] ROOT`: export each top-level `.sc` file under a directory
- `sc-export sc3d-viewer [--fingerprint SHA] [--addr HOST:PORT]`: serve the browser-based hero and skin viewer

The older command shape remains compatible, including flags written after the input path, but new scripts should use the explicit subcommands.

Root commands treat `--workers` as one global budget. `texture-root` plans one output name per input and deletes a source only after that exact file exists. `sc-root` renders into a staging directory and keeps the previous output intact if the replacement fails.

## Flags

- `--out <dir>`: output directory for single-file or directory exports
- `--workers <n>`: global export-worker budget; `0` uses the CPU count
- `--file-concurrency <n>`: number of `.sc` files processed concurrently by `sc-root`, within the global worker budget
- `--render-scale <n>`: final output canvas scale
- `--scenery-max-dimension <n>`: cap the longest scenery edge; default `2048`, or `0` for original resolution
- `--scenery-format <auto|hevc|webp>`: animated scenery output; default `auto` uses hardware HEVC on macOS and WebP elsewhere
- `--hevc-quality <0-100>`: HEVC video quality; default `80`
- `--prefer-webp`: write still images as WebP instead of PNG
- `--webp-quality <0-100>`: WebP quality; default `88`
- `--webp-method <0-6>`: WebP encoding effort from fastest to smallest; default `0`
- `--disable-gpu`: use the CPU compositor instead of Metal (useful for comparisons and troubleshooting)
- `--first-frame`: render only the first frame of matching exports
- `--last-frame`: render only the last frame of matching exports
- `--profile`: print bottleneck timing summaries
- `--profile-top-n <n>`: number of slowest targets to show when profiling
- `--skip-tiny-threshold <n>`: skip outputs whose width and height are both `<= n`
- `--include-prefix <prefix>`: restrict `sc-root` to matching basenames; repeatable
- `--asset <name>`: export only a specific asset/export name; repeatable
- `--asset-output <name=path>`: write a specific export to an exact output path; repeatable
- `--delete-source`: delete source files after successful processing
- `--delete-sctx`: delete `.sctx` sidecars after a successful `sc-root` run

## Examples

```bash
# Export one SC file
./sc-export export --out out/ input.sc

# Export only a couple of named assets
./sc-export export --asset barbarian --asset archer input.sc

# Write one export to an exact path
./sc-export export --asset-output barbarian=out/troops/barbarian.png input.sc

# Export still WebP files from selected assets
./sc-export export --prefer-webp --first-frame --asset cannon_lvl1 input.sc

# Export a complete animated scenery as hardware HEVC. When INPUT is
# referenced by logic/village_backgrounds.json, BaseSWF is included automatically.
./sc-export export --asset Player_Background --out scenery-out/ path/to/sc/background_player.sc

# Force the portable, built-in animated WebP path instead
./sc-export export --asset Player_Background --scenery-format webp --out scenery-out/ path/to/sc/background_player.sc

# Batch-process every top-level .sc in a root
./sc-export sc-root --file-concurrency 4 --workers 16 --prefer-webp raw/

# Decode every .sctx file under a root and remove the sources afterward
./sc-export texture-root --delete-source raw-textures/

# Browse live hero/skin models, configure landing behavior, and export model.glb plus skin.json
./sc-export sc3d-viewer --fingerprint <asset-fingerprint>
```
