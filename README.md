# ClashKing Assets

Static Clash of Clans asset files served from `https://assets.clashk.ing`.

## Asset URLs

Anything under `/assets` is available at `https://assets.clashk.ing/<path-under-assets>`.

Example:

- `assets/troops/barbarian/icon.webp`
- `https://assets.clashk.ing/troops/barbarian/icon.webp`

The hosted assets are cached and served through Cloudflare. You are welcome to use them in your own project, to have the latest assets. However, as maintaining this
does cost us time & money please credit us somewhere in your project. Thanks!

Asset responses include a `Last-Modified` header. You can make a `HEAD` request and compare that header to see if an asset has a newer version.

## Programmatic Paths

Names are standardized so URLs can be generated from static data names.

Use this cleanup rule:

```python
cleaned_name = s.lower().replace(" ", "_").replace(".", "").replace("?", "")
```

Then insert `cleaned_name` into the path template for the asset type.

Examples:

- `https://assets.clashk.ing/spells/{cleaned_name}.webp`
- `https://assets.clashk.ing/troops/{cleaned_name}/icon.webp`
- `https://assets.clashk.ing/buildings/home-village/{cleaned_name}/level_{level}.webp`
- `https://assets.clashk.ing/decorations/home-village/{cleaned_name}.webp`

## Repository Layout

- [`assets/`](assets): published static assets
- [`assets/static_data.json`](assets/static_data.json): upgrade costs and times, ids, stats, and more for buildings, troops, etc.
- [`assets/translations.json`](assets/translations.json): translation strings for static data

## Contributing

Contributions are welcome, especially:

- new assets
- fixes for export accuracy, naming, or performance
- documentation updates

If you add assets, keep paths organized under [`assets/`](assets) so they map cleanly to `https://assets.clashk.ing/...`.

## License

This repository is licensed under the GNU GPL v3. See [`LICENSE`](LICENSE).
