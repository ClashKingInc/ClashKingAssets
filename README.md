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
cleaned_name = s.lower().replace(" ", "_").replace(".", "").replace("?", "").replace("\\q", "").replace("’", "")
```

Then insert `cleaned_name` into the path template for the asset type.

Examples:

- `https://assets.clashk.ing/spells/{cleaned_name}.webp`
- `https://assets.clashk.ing/troops/{cleaned_name}/icon.webp`
- `https://assets.clashk.ing/buildings/home-village/{cleaned_name}/level_{level}.webp`
- `https://assets.clashk.ing/decorations/home-village/{cleaned_name}.webp`

## Contributing

Contributions are welcome, especially:

- new assets
- fixes for export accuracy, naming, or performance
- documentation updates

If you add assets, keep paths organized under [`assets/`](assets) so they map cleanly to `https://assets.clashk.ing/...`.

## License

This repository is licensed under the GNU GPL v3. See [`LICENSE`](LICENSE).

## Wiki data

The obstacle collection contains seasonal/event collectibles only. It uses the
raw `AppearancePeriodHours` event window, with the two legacy Clashmas trees,
Cursed Seedling and ore-event meteorites retained by TID because those records
omit the window. Recurring Gem Boxes and tombstones are excluded. Terrain,
deploy markers, ordinary trees, bushes, roots, stones, ponds and campfires are
not part of this collection. Filtering preserves original game IDs. Raw game tables remain unmodified.

The static export includes squad composition (`squad.members` with stable `troop_id` and `count`), whether member levels use the player's troops, and squad ability references per level. These fields come from `logic/squads.csv`; they do not infer composition from descriptions.

Ranked leagues export `ranked_medals.maximum_per_week` when defined in `MaxRankedPrestige`. The highest Legend tier exports `placement_rewards` from `legend_prestige_rewards.csv`. Absence means unknown/not applicable, never zero. CWL currencies remain separate.

Troops with a source portrait export to `troops/<name>/portrait.webp`; keep icon.webp for compact rows. The portrait is registered from BigPictureSWF/BigPicture, not guessed from a filename.


The November 2026 `19.6.1` APK was used to verify the new squad and medal source fields. All enrichment runs in `update_static.py` against its decoded source tables. 130 verified portrait exports are included (110 troop/squad, 8 hero, 12 pet). Game packages may wrap SC files in `base-attachments/` with a `source_assets:/` virtual root, and SC2 may contain SCTX bytes despite its KTX format tag. The extractor recognizes these containers without relaxing traversal or SHA verification.
