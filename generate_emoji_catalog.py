"""Derive Discord emoji definitions from the same static data and images as the CDN."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path

# Prefix categories whose display names overlap (e.g. CWL and Capital leagues).
COLLECTIONS = {
    "troops": ("troops", "", True),
    "spells": ("spells", "", False),
    "heroes": ("heroes", "", True),
    "pets": ("pets", "", True),
    "equipment": ("equipment", "", False),
    "capital_leagues": ("leagues/capital-leagues", "capital", False),
    "league_tiers": ("leagues/league-tier", "", False),
    "war_leagues": ("leagues/cwl", "cwl", False),
    "builder_leagues": ("leagues/builder-base", "builder", False),
    "resources": ("resources", "", False),
    "player_labels": ("player_labels", "playerlabel", False),
    "clan_labels": ("clan_labels", "clanlabel", False),
}
IMAGE_EXTENSIONS = {".png", ".webp", ".jpg", ".jpeg", ".gif"}
# Compatibility names used by existing log renderers, backed by canonical exports.
ICON_ALIASES = {
    "clash_sword": "icons/Icon_HV_Sword.png", "shield": "icons/Icon_HV_Shield.png",
    "war_star": "icons/Icon_HV_Attack_Star.png", "no_star": "icons/Icon_BB_Empty_Star.png",
    "clan_games": "icons/Icon_HV_Clan_Games_Medal.png", "trophy": "icons/Icon_HV_Trophy.png",
    "xp": "icons/Icon_HV_XP.png", "capital_trophy": "icons/Icon_CC_Resource_Capital_Trophy.png",
    "raid_medal": "resources/raid_medals.webp", "cwl_medal": "resources/league_medals.webp",
    "capital_gold": "resources/capital_gold.webp", "gold": "resources/gold.webp",
    "elixir": "resources/elixir.webp", "dark_elixir": "resources/dark_elixir.webp",
    "unranked": "leagues/league-tier/unranked.png",
}


def emoji_name(value: str) -> str:
    name = re.sub(r"[^a-z0-9_]", "", value.lower())
    if len(name) < 2:
        name = "emoji" + name
    if len(name) > 32:
        name = name[:23] + hashlib.sha256(value.encode()).hexdigest()[:9]
    return name


def asset_slug(name: str) -> str:
    return name.lower().replace(" ", "_").replace(".", "").replace("?", "").replace("\\q", "").replace("’", "")


def build_catalog(root: Path) -> dict:
    entries: dict[str, dict] = {}

    def add(name: str, path: Path, category: str):
        if not path.is_file():
            raise ValueError(f"Missing emoji asset: {path}")
        normalized = emoji_name(name)
        entry = {"name": normalized, "path": "/" + path.relative_to(root).as_posix(),
                 "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "category": category}
        previous = entries.get(normalized)
        if previous and previous["path"] != entry["path"]:
            raise ValueError(f"Emoji name collision: {normalized}: {previous['path']} / {entry['path']}")
        entries[normalized] = entry

    for collection, (directory, prefix, nested) in COLLECTIONS.items():
        data = json.loads((root / "static_data" / f"{collection}.json").read_text())
        items = data["items"]
        if not items:
            raise ValueError(f"Empty static collection: {collection}")
        for item in items:
            slug = asset_slug(item["name"])
            if collection in {"capital_leagues", "war_leagues", "builder_leagues", "league_tiers"}:
                for roman, number in [("v", "5"), ("iv", "4"), ("iii", "3"), ("ii", "2"), ("i", "1")]:
                    if slug.endswith("_" + roman):
                        slug = slug[:-(len(roman))] + number
                        break
            stem = root / directory / slug
            if nested:
                stem = stem / "icon"
            paths = [stem.with_suffix(ext) for ext in (".webp", ".png") if stem.with_suffix(ext).is_file()]
            if not paths:
                raise ValueError(f"No generated icon for {collection}: {item['name']} ({stem})")
            add(prefix + item["name"], paths[0], collection)

    for directory, category in [("icons", "icons"), ("bot/numbers", "numbers")]:
        for path in sorted((root / directory).rglob("*")):
            if path.suffix.lower() in IMAGE_EXTENSIONS:
                add(path.stem, path, category)
    for name, relative in ICON_ALIASES.items():
        add(name, root / relative, "icons")
    # Preserve utility artwork that has no game-generated equivalent. These are
    # still consumed by deployed clients at /bot/icons; they are not game data.
    for path in sorted((root / "bot/icons").glob("*")):
        if path.suffix.lower() in IMAGE_EXTENSIONS and path.stem not in ICON_ALIASES:
            add(path.stem, path, "utility_icons")
    # Town Hall badges remain useful in upgrade logs, but come from generated buildings.
    for path in sorted((root / "buildings/home-village/town_hall").glob("level_*.webp")):
        add("th" + path.stem.removeprefix("level_"), path, "town_halls")

    if not entries or len(entries) > 2000:
        raise ValueError(f"Invalid application emoji count: {len(entries)} (maximum 2000)")
    return {"version": 1, "emojis": [entries[name] for name in sorted(entries)]}


def write_catalog(root: Path, *, check: bool = False):
    catalog = build_catalog(root)
    # Validation is build-time only. Discord's application inventory is the
    # runtime source of emoji IDs; do not publish a redundant asset catalog.
    print(f"Validated {len(catalog['emojis'])} application emoji source images")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--assets-root", type=Path, default=Path("assets"))
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    write_catalog(args.assets_root, check=args.check)
