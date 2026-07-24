from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from urllib.parse import quote

ASSET_BASE_URL = "https://assets.clashk.ing"
SUPPORTED_IMAGE_EXTENSIONS = frozenset({"gif", "jpeg", "jpg", "png", "svg", "webp"})


class ManifestError(RuntimeError):
    pass


def humanize(value: str) -> str:
    return re.sub(r"[_-]+", " ", value).strip()


def display_name(path: Path) -> str:
    stem = humanize(path.stem)
    if path.stem.casefold() == "icon" and path.parent != Path("."):
        return humanize(path.parent.name)

    is_leveled_structure = path.parts[0] in {"buildings", "traps"}
    if is_leveled_structure and re.fullmatch(r"level_\d+", path.stem, flags=re.IGNORECASE):
        return f"{humanize(path.parent.name)} {stem}"

    return stem


def build_manifest(assets_root: Path, base_url: str = ASSET_BASE_URL) -> dict[str, object]:
    if not assets_root.is_dir():
        raise ManifestError(f"assets root is not a directory: {assets_root}")

    assets: list[dict[str, str]] = []
    for path in assets_root.rglob("*"):
        if not path.is_file():
            continue

        relative_path = path.relative_to(assets_root)
        if relative_path.parts[0] == "bot":
            continue

        extension = path.suffix.removeprefix(".").lower()
        if extension not in SUPPORTED_IMAGE_EXTENSIONS:
            continue

        relative_url = quote(relative_path.as_posix(), safe="/")
        assets.append(
            {
                "path": relative_path.as_posix(),
                "category": relative_path.parts[0],
                "display_name": display_name(relative_path),
                "extension": extension,
                "url": f"{base_url.rstrip('/')}/{relative_url}",
            }
        )

    assets.sort(key=lambda asset: asset["path"])
    return {"version": 1, "assets": assets}


def render_manifest(assets_root: Path, base_url: str = ASSET_BASE_URL) -> str:
    return json.dumps(build_manifest(assets_root, base_url), ensure_ascii=False, indent=2) + "\n"


def write_manifest(assets_root: Path, output_path: Path, base_url: str = ASSET_BASE_URL) -> bool:
    rendered = render_manifest(assets_root, base_url)
    if output_path.exists() and output_path.read_text(encoding="utf-8") == rendered:
        return False

    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(rendered, encoding="utf-8")
    return True


def check_manifest(assets_root: Path, output_path: Path, base_url: str = ASSET_BASE_URL) -> None:
    if not output_path.is_file():
        raise ManifestError(f"manifest does not exist: {output_path}")
    if output_path.read_text(encoding="utf-8") != render_manifest(assets_root, base_url):
        raise ManifestError("manifest is stale: run python generate_manifest.py")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate the hosted ClashKing image asset manifest.")
    parser.add_argument("--assets-root", type=Path, default=Path("assets"))
    parser.add_argument("--output", type=Path, default=Path("assets/manifest.json"))
    parser.add_argument("--base-url", default=ASSET_BASE_URL)
    parser.add_argument("--check", action="store_true", help="Fail if the existing manifest is missing or stale.")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.check:
        check_manifest(args.assets_root, args.output, args.base_url)
        print(f"Manifest is current: {args.output}")
        return 0

    changed = write_manifest(args.assets_root, args.output, args.base_url)
    status = "Updated" if changed else "Already current"
    print(f"{status}: {args.output}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ManifestError as exc:
        raise SystemExit(str(exc)) from exc
