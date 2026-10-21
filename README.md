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

## Ability data

`assets/static_data/abilities.json` contains player-facing abilities grouped by owning
item `_id`, with ability names, available `TID.name`/`TID.info`, and values per item
level. It covers exported hero equipment and Builder Base troops. This collection
is separate from the master `assets/static_data.json`; existing equipment fields
in the master remain compatible.

`generate_abilities.py` contains the generator. `update_static.py` calls it with
parsed game tables, the current item catalog, and patched translations before
translation pruning and source cleanup. No prototype or temporary files are needed.
Spatial values retain the existing static-data scale, and no `unit` or ability
`key` is exported. Source ability names group levels internally. Unknown effect
fields stop generation rather than silently dropping new mechanics; known
mechanics on new equipment require no item-specific rule.

## Contributing

### Discord application emojis

`python generate_emoji_catalog.py --check` validates a transient sync inventory
from static game data and canonical images. No emoji catalog is published or
loaded by bot renderers. `update_static.py` validates the same sources after
extraction. Missing images fail validation before Discord can be changed.
The sync script derives its desired inventory directly from these sources.
Renderers read Discord application emojis by name, such as `war_star`, `pekka`,
and `gold_15`; utility and number underscores are preserved.

The catalog includes troops, spells, heroes, pets, equipment, Capital leagues,
league tiers, CWL leagues, Builder leagues, resources, player/clan labels, icons,
Town Hall badges, and the bot's number images. Category prefixes disambiguate
Capital/CWL/Builder leagues and labels; game unit names retain the normalized
names used by log renderers. Utility icons without generated game equivalents
remain supported under `/bot/icons`, including URLs used by deployed app and
dashboard versions. Do not remove those paths until those consumers migrate.

After the R2 sync, the release action mirrors configured Discord applications:

- Production: repository variable `DISCORD_APPLICATION_ID`, secret `DISCORD_BOT_TOKEN`.
- Beta: repository variable `DISCORD_BETA_APPLICATION_ID`, secret `DISCORD_BETA_BOT_TOKEN`.

An unset application variable skips that target; a configured target with missing
or mismatched credentials fails. No credentials are included in the catalog.
For a read-only plan, run `python sync_discord_emojis.py` with
`DISCORD_APPLICATION_ID` and `DISCORD_BOT_TOKEN` in the environment; add `--apply`
to mirror that application's **entire** emoji inventory. New definitions are added,
changed images are replaced, and definitions no longer present are removed.
This is not a guild emoji sync. Manually added application emojis must have a
catalog definition or the next sync will remove them.
Use `--preserve-unmanaged` for a targeted update that keeps remote names outside
the generated inventory, including manually added application emojis. Review
the read-only plan before adding `--apply`; use the same preservation option for
both commands. This does not preserve old IDs when a managed image changes.

All images are validated before mutations. Discord cannot change an existing
emoji's image, so replacement creates a temporary emoji before removing the old
one; it needs one free application slot. GIF animation is retained. The tracker
refreshes application emoji IDs periodically because replaced images get new IDs.
The action is not transactional with R2; a failed Discord step can be rerun after
fixing its cause without repeating unchanged emojis.

Contributions are welcome, especially:

- new assets
- fixes for export accuracy, naming, or performance
- documentation updates

If you add assets, keep paths organized under [`assets/`](assets) so they map cleanly to `https://assets.clashk.ing/...`.

## License

This repository is licensed under the GNU GPL v3. See [`LICENSE`](LICENSE).
