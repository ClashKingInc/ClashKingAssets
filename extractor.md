# Asset Extraction

This repository includes a Go CLI for turning Supercell `.sc` and `.sctx` files into images and export manifests.

## Repository Layout

- [`main.go`](main.go): CLI entrypoint
- [`internal/render/`](internal/render): export pipeline, batching, manifests, and image encoding
- [`internal/sc/`](internal/sc): `.sc` and texture parsing

## Prerequisites

Install `astcenc` so it is available on your `PATH`.

macOS:

```bash
brew install astc-encoder
```

Linux:

```bash
sudo snap install astc-encoder
```

Animated WebP exports require `img2webp` from the WebP tools.

macOS:

```bash
brew install webp
```

Windows:

```powershell
# Chocolatey
choco install webp

# or Scoop
scoop install libwebp
```

Verify the exporter can find it:

```bash
img2webp -version
```

Build the CLI:

```bash
go build -o sc-export .
```

## Capabilities

- Decode and process `.sc` files
- Decode raw `.sctx` texture files
- Filter exports to specific asset names
- Route specific exports to exact output paths
- Emit a `manifest.json` with exported files, metadata, and skipped entries

## Flags

- `--out <dir>`: output directory for single-file or directory exports
- `--workers <n>`: number of export workers
- `--file-concurrency <n>`: number of `.sc` files to process concurrently with `--process-sc-root`
- `--render-scale <n>`: final output canvas scale
- `--prefer-webp`: write still images as WebP instead of PNG
- `--first-frame`: render only the first frame of matching exports
- `--last-frame`: render only the last frame of matching exports
- `--profile`: print bottleneck timing summaries
- `--profile-top-n <n>`: number of slowest targets to show when profiling
- `--skip-tiny-threshold <n>`: skip outputs whose width and height are both `<= n`
- `--process-image-root <dir>`: recursively process all `.sctx` files under a root
- `--process-sc-root <dir>`: process all top-level `.sc` files in a root
- `--include-prefix <prefix>`: restrict `--process-sc-root` to matching basenames; repeatable
- `--asset <name>`: export only a specific asset/export name; repeatable
- `--asset-output <name=path>`: write a specific export to an exact output path; repeatable
- `--delete-source`: delete source files after successful processing
- `--delete-sctx`: delete `.sctx` sidecars after `--process-sc-root`

## Examples

```bash
# Export one SC file
./sc-export input.sc --out out/

# Export only a couple of named assets
./sc-export input.sc --asset barbarian --asset archer

# Write one export to an exact path
./sc-export input.sc --asset-output barbarian=out/troops/barbarian.png

# Export still WebP files from selected assets
./sc-export input.sc --prefer-webp --first-frame --asset cannon_lvl1

# Batch-process every top-level .sc in a root
./sc-export --process-sc-root raw/ --file-concurrency 4 --workers 16 --prefer-webp

# Decode every .sctx file under a root and remove the sources afterward
./sc-export --process-image-root raw-textures/ --delete-source
```
