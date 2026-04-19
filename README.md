# ClashKing Assets

Static Clash of Clans asset files, plus a Go extractor for turning Supercell `.sc` / `.sctx` files into usable images and export manifests.

## Asset URLs

Anything under [`/assets`](/Users/matthewanderson/PycharmProjects/clashking_assets/assets) is available at `https://assets.clashk.ing/<path-under-assets>`.

Example:

- `assets/troops/barbarian/icon.webp`
- `https://assets.clashk.ing/troops/barbarian/icon.webp`

The hosted assets are cached and served through Cloudflare. You are welcome to use them in your own project, however, as maintaining this 
does cost us time & money please credit us somewhere in your project. Thanks!

## Repository Layout

- [`assets/`](/Users/matthewanderson/PycharmProjects/clashking_assets/assets): published static assets
- [`main.go`](/Users/matthewanderson/PycharmProjects/clashking_assets/main.go): extractor CLI entrypoint
- [`internal/render/`](/Users/matthewanderson/PycharmProjects/clashking_assets/internal/render): export pipeline, batching, manifests, image encoding
- [`internal/sc/`](/Users/matthewanderson/PycharmProjects/clashking_assets/internal/sc): `.sc` / texture parsing

## Extractor

Build the CLI:

```bash
go build -o sc-export .
```

What it can do:

- Export a single `.sc` file into an output directory
- Export every eligible `.sc` file in a directory
- Decode raw `.sctx` texture files
- Batch-process all `.sctx` files under a root with progress reporting
- Batch-process all top-level `.sc` files in a root with configurable per-file concurrency
- Filter exports to specific asset names
- Route specific exports to exact output paths
- Emit a `manifest.json` describing exported files, metadata, and skipped entries
- Prefer WebP for still images, scale renders, skip tiny outputs, and print profiling summaries

### Usage

```bash
./sc-export <file-or-dir> [flags]
./sc-export --process-image-root <dir> [flags]
./sc-export --process-sc-root <dir> [flags]
```

### Flags

- `--out <dir>`: output directory for single-file or directory exports
- `--workers <n>`: number of export workers
- `--file-concurrency <n>`: number of `.sc` files to process concurrently with `--process-sc-root`
- `--render-scale <n>`: final output canvas scale
- `--prefer-webp`: write still images as WebP instead of PNG
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

### Examples

```bash
# Export one SC file
./sc-export input.sc --out out/

# Export only a couple of named assets
./sc-export input.sc --asset barbarian --asset archer

# Write one export to an exact path
./sc-export input.sc --asset-output barbarian=out/troops/barbarian.png

# Batch-process every top-level .sc in a root
./sc-export --process-sc-root raw/ --file-concurrency 4 --workers 16 --prefer-webp

# Decode every .sctx file under a root and remove the sources afterward
./sc-export --process-image-root raw-textures/ --delete-source
```

## Contributing

Contributions are welcome, especially:

- new assets
- extractor improvements
- fixes for export accuracy, naming, or performance
- documentation updates

If you add assets, keep paths organized under [`assets/`](/Users/matthewanderson/PycharmProjects/clashking_assets/assets) so they map cleanly to `https://assets.clashk.ing/...`.

## License

This repository is licensed under the GNU GPL v3. See [`LICENSE`](/Users/matthewanderson/PycharmProjects/clashking_assets/LICENSE).
