import asyncio
import threading
import time
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

import build
import update_static
from update_static import StaticUpdater


class FakeR2Client:
    def __init__(self):
        self.lock = threading.Lock()
        self.active_uploads = 0
        self.max_active_uploads = 0
        self.uploaded = []
        self.delete_batches = []

    def upload_file(self, local_path, bucket, key, ExtraArgs=None):
        with self.lock:
            self.active_uploads += 1
            self.max_active_uploads = max(self.max_active_uploads, self.active_uploads)
        time.sleep(0.01)
        with self.lock:
            self.uploaded.append((local_path, bucket, key, ExtraArgs))
            self.active_uploads -= 1

    def delete_objects(self, *, Bucket, Delete):
        self.delete_batches.append((Bucket, Delete["Objects"]))
        return {}


def test_apply_sync_plan_uploads_concurrently_and_batches_deletes():
    client = FakeR2Client()
    plan = {
        "uploads": [
            {"local_path": f"assets/file_{index}.webp", "key": f"file_{index}.webp"} for index in range(8)
        ],
        "deletes": [{"key": f"old_{index}.webp"} for index in range(1001)],
    }
    config = build.R2Config("https://example.invalid", "key", "secret", "assets")

    with patch("build.create_r2_client", return_value=client):
        build.apply_sync_plan(plan, config, workers=4)

    assert client.max_active_uploads > 1
    assert len(client.uploaded) == 8
    assert [len(objects) for _, objects in client.delete_batches] == [1000, 1]


def test_apply_sync_plan_rejects_invalid_worker_count():
    config = build.R2Config("https://example.invalid", "key", "secret", "assets")
    with pytest.raises(build.BuildError, match="workers must be at least 1"):
        build.apply_sync_plan({"uploads": [], "deletes": []}, config, workers=0)


def test_apply_sync_plan_sets_cache_and_content_type_for_shared_cdn_assets():
    client = FakeR2Client()
    plan = {
        "uploads": [
            {"local_path": "assets/fonts/clashking.woff2", "key": "fonts/clashking.woff2"},
            {"local_path": "assets/fonts/clashking.ttf", "key": "fonts/clashking.ttf"},
            {
                "local_path": "assets/logos/clashking-wordmark-dark.svg",
                "key": "logos/clashking-wordmark-dark.svg",
            },
            {"local_path": "assets/troops/barbarian/icon.webp", "key": "troops/barbarian/icon.webp"},
        ],
        "deletes": [],
    }
    config = build.R2Config("https://example.invalid", "key", "secret", "assets")

    with patch("build.create_r2_client", return_value=client):
        build.apply_sync_plan(plan, config, workers=2)

    uploaded = {key: extra_args for _, _, key, extra_args in client.uploaded}
    assert uploaded["fonts/clashking.woff2"] == {
        "ContentType": "font/woff2",
        "CacheControl": build.CDN_CACHE_CONTROL,
    }
    assert uploaded["fonts/clashking.ttf"] == {
        "ContentType": "font/ttf",
        "CacheControl": build.CDN_CACHE_CONTROL,
    }
    assert uploaded["logos/clashking-wordmark-dark.svg"] == {
        "ContentType": "image/svg+xml",
        "CacheControl": build.CDN_CACHE_CONTROL,
    }
    assert uploaded["troops/barbarian/icon.webp"] is None


def test_scenery_metadata_keeps_music_free_and_default_fields():
    updater = StaticUpdater()
    updater.open_file = lambda _: {
        "Classic": {
            "HomeType": "HOME",
            "TID": "TID_SCENERY_CLASSIC",
            "Icon": "sc/classic_icon.sctx",
            "Thumbnail": "sc/classic_thumbnail.sctx",
            "Music": "music/classic.ogg",
            "FreeBackground": True,
            "DefaultBackground": True,
        }
    }
    updater._translate = lambda tid: "Classic Scenery" if tid == "TID_SCENERY_CLASSIC" else None

    [scenery] = updater._parse_scenery_data()

    assert scenery["music"] == "sceneries/classic_scenery/music.ogg"
    assert scenery["free"] is True
    assert scenery["default"] is True


def test_asset_extraction_builds_go_extractor_once_and_reuses_it(tmp_path, monkeypatch):
    updater = StaticUpdater()
    updater.BASE_PATH = str(tmp_path / "assets")
    updater.APK_URL = "https://example.invalid/game.apk"
    updater.register_sc_asset("sc/one.sctx", "", "textures/one")
    updater.register_sc_asset("sc/two.sctx", "", "textures/two")

    monkeypatch.chdir(tmp_path)
    monkeypatch.setattr(
        update_static,
        "fetch_fingerprint_manifest",
        AsyncMock(
            return_value=(
                "fingerprint",
                {"files": [{"file": "sc/one.sctx"}, {"file": "sc/two.sctx"}]},
            )
        ),
    )
    monkeypatch.setattr(update_static, "download_file", AsyncMock(return_value=b"texture"))
    commands = []

    def fake_run(command, *, check):
        commands.append(command)
        if "--out" in command:
            output_base = Path(command[command.index("--out") + 1])
            output_base.with_suffix(".webp").write_bytes(b"RIFFxxxxWEBP")

    monkeypatch.setattr(update_static.subprocess, "run", fake_run)

    asyncio.run(updater.extract_assets())

    build_commands = [command for command in commands if command[:2] == ["go", "build"]]
    extractor_commands = [command for command in commands if command[:2] != ["go", "build"]]
    assert len(build_commands) == 1
    assert len(extractor_commands) == 2
    assert extractor_commands[0][0] == extractor_commands[1][0] == build_commands[0][3]


def test_existing_asset_is_not_regenerated_for_special_frame_modes(tmp_path, monkeypatch):
    updater = StaticUpdater()
    updater.BASE_PATH = str(tmp_path / "assets")
    destination = Path(updater.BASE_PATH) / "decorations/home-village/torch.webp"
    destination.parent.mkdir(parents=True)
    destination.write_bytes(b"existing")
    updater.register_sc_asset(
        "sc/decorations.sc",
        "deco_torch1",
        "decorations/home-village/torch",
        first_frame=True,
        preferred_frame_label="store_idle,idle_end,idle_start",
    )
    fetch_manifest = AsyncMock()
    monkeypatch.setattr(update_static, "fetch_fingerprint_manifest", fetch_manifest)

    asyncio.run(updater.extract_assets())

    fetch_manifest.assert_not_awaited()
    assert destination.read_bytes() == b"existing"


def test_super_wizard_tower_levels_share_the_level_one_building_base():
    updater = StaticUpdater()
    building_data = {"TID": "TID_BUILDING_MERGED_WIZARD_TOWER"}

    assert updater.building_base_asset_name(building_data, {"BuildingLevel": 1}) == (
        "merged_wizard_tower_lvl1_base"
    )
    assert updater.building_base_asset_name(building_data, {"BuildingLevel": 2}) == (
        "merged_wizard_tower_lvl1_base"
    )


def test_configured_building_base_names_still_take_precedence():
    updater = StaticUpdater()

    assert updater.building_base_asset_name(
        {
            "TID": "TID_BUILDING_HOUSING",
            "ExportNameBase": "housing_base",
        },
        {},
    ) == "housing_base"
    assert updater.building_base_asset_name(
        {"TID": "TID_BUILDING_CANNON"},
        {"ExportNameBase": "unexpected_base"},
    ) is None


def test_ignored_ids_are_loaded_from_local_file(tmp_path):
    ignored_file = tmp_path / ".ignored.txt"
    ignored_file.write_text(
        "# Decorations\n18000000 # Anniversary Fountain\n\n90000042\n",
        encoding="utf-8",
    )

    assert update_static.load_ignored_ids(ignored_file) == {18000000, 90000042}


def test_ignored_decoration_is_not_emitted_or_registered():
    updater = StaticUpdater()
    updater.ignored_ids = {18000000}
    updater.open_file = lambda _: {
        "IgnoredDecoration": {
            "TID": "TID_IGNORED_DECORATION",
            "SWF": "sc/decorations.sc",
            "ExportName": "ignored_decoration",
        }
    }

    assert updater._parse_decoration_data() == []
    assert updater.sc_asset_requests == {}


def test_hero_troop_and_pet_weights_are_emitted_from_top_level_data():
    updater = StaticUpdater()
    updater.full_building_data = {"Barrack": {"TID": "TID_BARRACK"}}
    updater.lab_to_townhall = {1: 1}
    updater.pethouse_to_townhall = {1: 14}
    updater._translate = lambda tid: tid
    updater.register_sc_asset = lambda **_: None
    files = {
        "logic/characters.json": {
            "WeightedTroop": {
                "GlobalID": 4000000,
                "TID": "TID_WEIGHTED_TROOP",
                "InfoTID": "TID_WEIGHTED_TROOP_INFO",
                "ProductionBuilding": "Barrack",
                "FriendlyGroupWeight": 3000,
                "HealerWeight": 0,
                "1": {"LaboratoryLevel": 1},
            },
            "WeightedBuilderTroop": {
                "GlobalID": 4000001,
                "TID": "TID_WEIGHTED_BUILDER_TROOP",
                "InfoTID": "TID_WEIGHTED_BUILDER_TROOP_INFO",
                "ProductionBuilding": "Barrack",
                "VillageType": 1,
                "FriendlyGroupWeight": 3000,
                "HealerWeight": 21,
                "1": {"LaboratoryLevel": 1},
            }
        },
        "logic/super_licences.json": {},
        "logic/heroes.json": {
            "WeightedHero": {
                "TID": "TID_WEIGHTED_HERO",
                "InfoTID": "TID_WEIGHTED_HERO_INFO",
                "FriendlyGroupWeight": 230,
                "HealerWeight": 21,
                "1": {},
            },
            "WeightedBuilderHero": {
                "TID": "TID_WEIGHTED_BUILDER_HERO",
                "InfoTID": "TID_WEIGHTED_BUILDER_HERO_INFO",
                "VillageType": 1,
                "FriendlyGroupWeight": 230,
                "HealerWeight": 21,
                "1": {},
            }
        },
        "logic/pets.json": {
            "WeightedPet": {
                "TID": "TID_WEIGHTED_PET",
                "InfoTID": "TID_WEIGHTED_PET_INFO",
                "FriendlyGroupWeight": 390,
                "HealerWeight": 4,
                "1": {"LaboratoryLevel": 1},
            }
        },
    }
    updater.open_file = files.__getitem__

    troop, builder_troop = updater._parse_troop_data()
    hero, builder_hero = updater._parse_hero_data()
    [pet] = updater._parse_pet_data()

    assert troop["warden_weight"] == 30
    assert troop["healer_weight"] == 0
    assert hero["warden_weight"] == 2.3
    assert hero["healer_weight"] == 21
    assert pet["warden_weight"] == 3.9
    assert pet["healer_weight"] == 4
    for item in (builder_troop, builder_hero):
        assert "warden_weight" not in item
        assert "healer_weight" not in item
    for item in (troop, hero, pet):
        assert list(item).index("warden_weight") < list(item).index("levels")
        assert list(item).index("healer_weight") < list(item).index("levels")
    assert updater._parse_unit_weights({}) == {}


def test_previous_release_lookup_rejects_an_invalid_current_ref():
    with patch("build.run_git", side_effect=build.BuildError("unknown revision")) as run_git:
        with pytest.raises(build.BuildError, match="invalid current release ref"):
            build.infer_previous_ref("missing-release", None)

    run_git.assert_called_once_with(["rev-parse", "--verify", "missing-release^{commit}"])


def test_previous_release_lookup_returns_none_when_valid_ref_has_no_older_tag():
    with patch(
        "build.run_git",
        side_effect=["commit-sha", build.BuildError("no tags before release")],
    ) as run_git:
        assert build.infer_previous_ref("v1.0.0", None) is None

    assert run_git.call_args_list[0].args[0] == ["rev-parse", "--verify", "v1.0.0^{commit}"]


def test_release_sync_plan_includes_manifest(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    manifest = Path("assets/manifest.json")
    manifest.parent.mkdir()
    manifest.write_text('{"version": 1, "assets": []}\n', encoding="utf-8")

    plan = build.build_sync_plan(
        [build.DiffEntry(status="A", path=manifest.as_posix())],
        assets_root="assets",
    )

    assert plan["uploads"] == [
        {
            "local_path": "assets/manifest.json",
            "key": "manifest.json",
            "reason": "added",
        }
    ]
