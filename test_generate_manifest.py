import json
from pathlib import Path

import pytest

from generate_manifest import ManifestError, build_manifest, check_manifest, write_manifest


def touch(root: Path, relative_path: str) -> None:
    path = root / relative_path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.touch()


def test_manifest_is_sorted_and_includes_supported_formats(tmp_path):
    assets_root = tmp_path / "assets"
    for relative_path in (
        "z-last/image.WEBP",
        "images/asset.SVG",
        "images/asset.PNG",
        "images/asset.JPG",
        "images/asset.JPEG",
        "images/asset.GIF",
        "a-first/name_with-hyphen.png",
    ):
        touch(assets_root, relative_path)

    manifest = build_manifest(assets_root)

    assert [asset["path"] for asset in manifest["assets"]] == sorted(
        asset["path"] for asset in manifest["assets"]
    )
    assert {asset["extension"] for asset in manifest["assets"]} == {
        "gif",
        "jpeg",
        "jpg",
        "png",
        "svg",
        "webp",
    }
    assert manifest["assets"][0] == {
        "path": "a-first/name_with-hyphen.png",
        "category": "a-first",
        "display_name": "name with hyphen",
        "extension": "png",
        "url": "https://assets.clashk.ing/a-first/name_with-hyphen.png",
    }


def test_manifest_excludes_bot_and_unsupported_files(tmp_path):
    assets_root = tmp_path / "assets"
    touch(assets_root, "bot/private/image.png")
    touch(assets_root, "troops/data.json")
    touch(assets_root, "troops/barbarian/icon.webp")

    manifest = build_manifest(assets_root)

    assert [asset["path"] for asset in manifest["assets"]] == ["troops/barbarian/icon.webp"]
    assert manifest["assets"][0]["display_name"] == "barbarian"


def test_manifest_names_leveled_buildings_and_traps_with_their_parent(tmp_path):
    assets_root = tmp_path / "assets"
    touch(assets_root, "buildings/home-village/hidden_tesla/level_12.webp")
    touch(assets_root, "traps/builder-base/push_trap/level_4.webp")
    touch(assets_root, "equipment/eternal_tome/level_3.webp")

    manifest = build_manifest(assets_root)
    names = {asset["path"]: asset["display_name"] for asset in manifest["assets"]}

    assert names["buildings/home-village/hidden_tesla/level_12.webp"] == "hidden tesla level 12"
    assert names["traps/builder-base/push_trap/level_4.webp"] == "push trap level 4"
    assert names["equipment/eternal_tome/level_3.webp"] == "level 3"


def test_manifest_output_is_reproducible_and_stale_changes_fail_check(tmp_path):
    assets_root = tmp_path / "assets"
    manifest_path = assets_root / "manifest.json"
    touch(assets_root, "troops/wizard/icon.webp")
    touch(assets_root, "buildings/cannon/level_1.png")

    assert write_manifest(assets_root, manifest_path) is True
    first_contents = manifest_path.read_bytes()
    assert write_manifest(assets_root, manifest_path) is False
    assert manifest_path.read_bytes() == first_contents
    check_manifest(assets_root, manifest_path)

    touch(assets_root, "spells/rage/icon.svg")
    with pytest.raises(ManifestError, match="manifest is stale"):
        check_manifest(assets_root, manifest_path)


def test_manifest_json_has_only_reproducible_top_level_fields(tmp_path):
    assets_root = tmp_path / "assets"
    manifest_path = assets_root / "manifest.json"
    touch(assets_root, "images/example.png")

    write_manifest(assets_root, manifest_path)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

    assert set(manifest) == {"version", "assets"}


def test_release_workflow_verifies_manifest_before_asset_sync():
    workflow = Path(".github/workflows/release-assets.yml").read_text(encoding="utf-8")

    manifest_check = workflow.index("python generate_manifest.py --check")
    release_sync = workflow.index("run: python build.py")
    assert manifest_check < release_sync
