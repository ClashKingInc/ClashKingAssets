from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path

SUPPORTED_IMAGE_EXTENSIONS = frozenset({"avif", "gif", "jpeg", "jpg", "png", "svg", "webp"})


def image_is_animated(path: Path) -> bool:
    if path.suffix.lower() == ".gif":
        return gif_is_animated(path)
    if path.suffix.lower() == ".svg":
        return any(marker in path.read_text(encoding="utf-8").lower() for marker in ("<animate", "<set", "@keyframes"))
    with path.open("rb") as source:
        header = source.read(32)
        if header[:4] == b"RIFF" and header[8:12] == b"WEBP":
            return header[12:16] == b"VP8X" and len(header) > 20 and bool(header[20] & 2)
        if header[:8] == b"\x89PNG\r\n\x1a\n":
            source.seek(8)
            while chunk := source.read(8):
                if len(chunk) != 8:
                    break
                if chunk[4:] == b"acTL":
                    return True
                if chunk[4:] in {b"IDAT", b"IEND"}:
                    return False
                source.seek(int.from_bytes(chunk[:4], "big") + 4, 1)
        if header[4:8] == b"ftyp":
            length = int.from_bytes(header[:4], "big")
            source.seek(8)
            brands = source.read(min(length - 8, 4096))
            return brands[:4] == b"avis" or b"avis" in [brands[i : i + 4] for i in range(8, len(brands), 4)]
    return False


def gif_is_animated(path: Path) -> bool:
    with path.open("rb") as source:
        header = source.read(13)
        if len(header) != 13 or header[:6] not in {b"GIF87a", b"GIF89a"}:
            return False
        if header[10] & 128:
            source.seek(3 * (2 ** ((header[10] & 7) + 1)), 1)
        frames = 0
        while marker := source.read(1):
            if marker == b"\x3b":
                return False
            if marker == b"\x2c":
                descriptor = source.read(9)
                if len(descriptor) != 9:
                    return False
                frames += 1
                if frames > 1:
                    return True
                if descriptor[8] & 128:
                    source.seek(3 * (2 ** ((descriptor[8] & 7) + 1)), 1)
                source.read(1)  # LZW minimum code size
            elif marker == b"\x21":
                source.read(1)  # Extension label
            else:
                return False
            while size := source.read(1):
                if size == b"\0":
                    break
                source.seek(size[0], 1)
    return False


def file_sha(path: Path) -> str:
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


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


def build_manifest(assets_root: Path) -> dict[str, object]:
    if not assets_root.is_dir():
        raise ManifestError(f"assets root is not a directory: {assets_root}")

    assets: list[dict[str, object]] = []
    for path in assets_root.rglob("*"):
        if not path.is_file():
            continue

        relative_path = path.relative_to(assets_root)
        if relative_path.parts[0] == "bot":
            continue

        extension = path.suffix.removeprefix(".").lower()
        if extension not in SUPPORTED_IMAGE_EXTENSIONS:
            continue

        assets.append(
            {
                "path": relative_path.as_posix(),
                "display_name": display_name(relative_path),
                "sha": file_sha(path),
                "animated": image_is_animated(path),
            }
        )

    assets.sort(key=lambda asset: asset["path"])
    data = {
        category: [
            {"path": path.relative_to(assets_root).as_posix(), "sha": file_sha(path)}
            for path in sorted((assets_root / folder).glob("*.json"))
        ]
        for category, folder in (
            ("stats", "static_data"),
            ("translations", "translations"),
        )
    }
    categories: dict[str, list[dict[str, object]]] = {}
    for asset in assets:
        path = str(asset["path"])
        category = path.split("/", 1)[0] if "/" in path else "other"
        categories.setdefault(category, []).append(asset)
    return {"version": 2, "assets": categories, "data": data}


def render_manifest(assets_root: Path) -> str:
    return json.dumps(build_manifest(assets_root), ensure_ascii=False, indent=2) + "\n"


def write_manifest(assets_root: Path, output_path: Path) -> bool:
    rendered = render_manifest(assets_root)
    if output_path.exists() and output_path.read_text(encoding="utf-8") == rendered:
        return False

    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(rendered, encoding="utf-8")
    return True


def check_manifest(assets_root: Path, output_path: Path) -> None:
    if not output_path.is_file():
        raise ManifestError(f"manifest does not exist: {output_path}")
    if output_path.read_text(encoding="utf-8") != render_manifest(assets_root):
        raise ManifestError("manifest is stale: run python generate_manifest.py")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate the hosted ClashKing image asset manifest.")
    parser.add_argument("--assets-root", type=Path, default=Path("assets"))
    parser.add_argument("--output", type=Path, default=Path("assets/manifest.json"))
    parser.add_argument("--check", action="store_true", help="Fail if the existing manifest is missing or stale.")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.check:
        check_manifest(args.assets_root, args.output)
        print(f"Manifest is current: {args.output}")
        return 0

    changed = write_manifest(args.assets_root, args.output)
    status = "Updated" if changed else "Already current"
    print(f"{status}: {args.output}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ManifestError as exc:
        raise SystemExit(str(exc)) from exc
