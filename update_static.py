"""
Automates updating the static files.
Now saves both the raw CSV and the generated JSON files.
If new files need to be added, then place them in the TARGETS list.
"""

import asyncio
import json
import logging
import csv
import io
import os
import hashlib
import shutil
import subprocess
import tempfile
import zipfile
import zstandard
import lzma
from dataclasses import dataclass
from pathlib import Path
from typing import Any, cast

from utils import apk_url, download_file


@dataclass(frozen=True)
class SCAssetRequest:
    source_sc: str
    asset_name: str | None
    save_path: str


DIRECT_ASSET_EXTENSIONS = {".sctx", ".ttf", ".otf", ".woff", ".woff2", ".mp4", ".ogg"}
DIRECT_WEBP_EXTENSIONS = {".sctx"}


def is_sc_bundle_file(source_sc: str, candidate: str) -> bool:
    source_path = Path(source_sc)
    candidate_path = Path(candidate)
    if candidate_path.parent.as_posix() != source_path.parent.as_posix():
        return False

    base = source_path.stem
    name = candidate_path.name
    if name == f"{base}.sc" or name == f"{base}_tex.sc":
        return True
    return name.startswith(f"{base}_") and name.endswith(".sctx")


def remove_empty_parents(path: Path, stop_at: Path) -> None:
    current = path.resolve()
    stop_at = stop_at.resolve()
    while current != stop_at:
        try:
            current.rmdir()
        except OSError:
            return
        parent = current.parent
        if parent == current:
            return
        current = parent


def is_exported_via_go(source_sc: str) -> bool:
    return source_sc.endswith((".sc", ".sctx"))

def hash_15_digits(s: str) -> int:
    digest = hashlib.blake2b(s.encode("utf-8"), digest_size=8).digest()
    return int.from_bytes(digest, "big") % 10**15

class StaticUpdater:
    def __init__(self):
        self.USED_TIDS = set()

        # keep the raw CSV files
        self.KEEP_CSV = False
        # keep the raw JSON files
        self.KEEP_JSON = False
        # removes any TIDs not used in the static files
        self.PRUNE_TRANSLATIONS = True
        # base path for the static files to be stored in
        self.BASE_PATH = "assets"

        self.FINGERPRINT = os.getenv("FINGERPRINT", "")
        self.APK_URL = apk_url()
        self.APK_PATH = os.getenv("APK_PATH", "").strip()
        self._apk_bytes_cache: bytes | None = None

        self.translation_data: dict[str, dict[str, str | None]] = {}
        self.full_building_data = {}
        self.full_supercharges_data = {}
        self.full_abilities_data = {}
        self.full_hero_data = {}
        self.full_townhall_data = {}
        self.full_troop_data = {}
        self.full_resource_data = {}

        self.lab_to_townhall = {}
        self.smithy_to_townhall = {}
        self.bb_lab_to_townhall = {}
        self.pethouse_to_townhall = {}

        self.animations_data = {}
        self.sc_asset_requests: dict[tuple[str, str | None], list[SCAssetRequest]] = {}

        self.build_mappping = {}

    async def _get_apk_bytes(self) -> bytes:
        if self._apk_bytes_cache is not None:
            return self._apk_bytes_cache

        if self.APK_PATH:
            apk_path = Path(self.APK_PATH)
            if apk_path.exists():
                self._apk_bytes_cache = apk_path.read_bytes()
                return self._apk_bytes_cache
            logging.warning("APK_PATH does not exist: %s", apk_path)

        apk_bytes = await download_file(self.APK_URL, show_progress=True)
        if not isinstance(apk_bytes, bytes):
            raise TypeError("Expected bytes when downloading APK")
        self._apk_bytes_cache = apk_bytes
        return apk_bytes

    async def _ensure_fingerprint(self) -> str:
        if self.FINGERPRINT:
            return self.FINGERPRINT

        apk_bytes = await self._get_apk_bytes()
        with zipfile.ZipFile(io.BytesIO(apk_bytes), "r") as apk_zip:
            with apk_zip.open("assets/fingerprint.json") as fp:
                fingerprint = json.loads(fp.read())["sha"]
        self.FINGERPRINT = fingerprint
        return self.FINGERPRINT

    def register_sc_asset(self, source_sc: str, asset_name: str, save_path: str) -> str:
        source_sc = source_sc.strip()
        normalized_asset_name = (asset_name or "").strip()
        save_path = save_path.strip()
        source_ext = Path(source_sc).suffix.lower()
        if source_ext != ".sc" and source_ext not in DIRECT_ASSET_EXTENSIONS:
            raise ValueError(f"invalid asset source: {source_sc!r}")
        if source_ext == ".sc" and not normalized_asset_name:
            raise ValueError(f"invalid SC asset name for {source_sc!r}")
        request_asset_name = normalized_asset_name if source_ext == ".sc" else None
        if not save_path:
            raise ValueError(f"save_path must not be empty for {source_sc}:{normalized_asset_name}")
        if source_ext in DIRECT_WEBP_EXTENSIONS or source_ext == ".sc":
            if Path(save_path).suffix.lower() != ".webp":
                save_path = f"{save_path}.webp"
        elif Path(save_path).suffix.lower() != source_ext:
            save_path = f"{save_path}{source_ext}"

        key = (source_sc, request_asset_name)
        request = SCAssetRequest(source_sc=source_sc, asset_name=request_asset_name, save_path=save_path)
        requests = self.sc_asset_requests.setdefault(key, [])
        if any(existing.save_path == save_path for existing in requests):
            return save_path
        requests.append(request)
        return save_path

    def should_skip_registered_asset(self, save_path: str) -> bool:
        return self.resolve_asset_output_path(save_path).exists()

    def resolve_asset_output_path(self, save_path: str) -> Path:
        normalized = Path(save_path.strip().lstrip("/"))
        return Path(self.BASE_PATH) / normalized

    def save_registered_asset(self, request: SCAssetRequest, local_path: Path) -> None:
        destination = self.resolve_asset_output_path(request.save_path)
        if destination.exists():
            return
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(local_path, destination)

    async def _download_sc_bundle(self, base_url: str, source_sc: str, available_files: set[str]) -> list[Path]:
        downloaded: list[Path] = []

        bundle_files = sorted(file_path for file_path in available_files if is_sc_bundle_file(source_sc, file_path))
        if source_sc not in bundle_files:
            raise FileNotFoundError(f"missing source bundle file in fingerprint: {source_sc}")

        for remote_path in bundle_files:
            data = await download_file(url=f"{base_url}/{remote_path}")
            local_path = Path(remote_path)
            local_path.parent.mkdir(parents=True, exist_ok=True)
            local_path.write_bytes(data)
            downloaded.append(local_path)

        return downloaded

    async def extract_assets(self):
        if not self.sc_asset_requests:
            return

        if not self.FINGERPRINT:
            self.FINGERPRINT = await self._ensure_fingerprint()
        base_url = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"
        try:
            fingerprint_file_raw = await download_file(url=f"{base_url}/fingerprint.json", as_json=True)
        except RuntimeError as exc:
            message = str(exc)
            if "HTTP 403" in message and "AccessDenied" in message:
                logging.warning("Skipping asset extraction: access denied for remote fingerprint.json")
                return
            raise
        if not isinstance(fingerprint_file_raw, dict):
            raise TypeError("fingerprint.json payload must be an object")
        fingerprint_file = cast(dict[str, Any], fingerprint_file_raw)
        available_files = {item.get("file") for item in fingerprint_file.get("files", []) if item.get("file")}

        grouped: dict[str, dict[str | None, list[SCAssetRequest]]] = {}
        for requests in self.sc_asset_requests.values():
            pending_requests = [
                request for request in requests if not self.should_skip_registered_asset(request.save_path)
            ]
            if not pending_requests:
                continue
            primary = pending_requests[0]
            grouped.setdefault(primary.source_sc, {})[primary.asset_name] = pending_requests

        for source_sc, asset_requests in sorted(grouped.items()):
            downloaded_files: list[Path] = []
            legacy_assets_dir = Path(source_sc).parent / f"{Path(source_sc).stem}_assets"
            try:
                if source_sc.endswith(".sc"):
                    downloaded_files = await self._download_sc_bundle(base_url, source_sc, available_files)
                else:
                    data = await download_file(url=f"{base_url}/{source_sc}")
                    local_path = Path(source_sc)
                    local_path.parent.mkdir(parents=True, exist_ok=True)
                    local_path.write_bytes(data)
                    downloaded_files = [local_path]
                if not is_exported_via_go(source_sc):
                    for requests in asset_requests.values():
                        for request in requests:
                            self.save_registered_asset(request, downloaded_files[0])
                    continue
                with tempfile.TemporaryDirectory(prefix="update-static-sc-") as temp_dir:
                    command = ["go", "run", "main.go", "--workers", str(max(1, os.cpu_count() or 1)), "--prefer-webp"]
                    if source_sc.endswith(".sc"):
                        command.extend(["--out", temp_dir])
                        for asset_name, requests in sorted(asset_requests.items()):
                            if asset_name is None:
                                raise ValueError(f"missing asset name for SC source {source_sc}")
                            temp_output_base = str(Path(temp_dir) / asset_name)
                            command.extend(["--asset", asset_name])
                            command.extend(["--asset-output", f"{asset_name}={temp_output_base}"])
                    else:
                        direct_output = str(Path(temp_dir) / Path(source_sc).stem)
                        command.extend(["--out", direct_output])
                    command.append(source_sc)
                    subprocess.run(command, check=True)

                    if source_sc.endswith(".sc"):
                        manifest_path = Path(temp_dir) / "manifest.json"
                        manifest_data = json.loads(manifest_path.read_text())
                        exported_files = {
                            entry["export_name"]: entry["output_file"] for entry in manifest_data.get("exports", [])
                        }
                        skipped_exports = {
                            entry["export_name"]: entry.get("reason", "unknown reason")
                            for entry in manifest_data.get("skipped", [])
                        }

                        for asset_name, requests in asset_requests.items():
                            exported_file = exported_files.get(asset_name)
                            if not exported_file:
                                skip_reason = skipped_exports.get(asset_name)
                                if skip_reason:
                                    raise RuntimeError(f"asset {source_sc}:{asset_name} was skipped: {skip_reason}")
                                raise FileNotFoundError(f"asset output missing for {source_sc}:{asset_name}")
                            for request in asset_requests[asset_name]:
                                self.save_registered_asset(request, Path(exported_file))
                    else:
                        exported_file = str(Path(temp_dir) / f"{Path(source_sc).stem}.webp")
                        for request in next(iter(asset_requests.values())):
                            self.save_registered_asset(request, Path(exported_file))
                shutil.rmtree(legacy_assets_dir, ignore_errors=True)
            finally:
                for path in downloaded_files:
                    try:
                        path.unlink()
                    except OSError:
                        pass
                shutil.rmtree(legacy_assets_dir, ignore_errors=True)
                if downloaded_files:
                    remove_empty_parents(Path(downloaded_files[0]).parent, Path.cwd())

    def decompress(self, data):
        """
        Decompresses the given bytes 'data' if needed (LZHAM, ZSTD, or LZMA).
        Returns (decompressed_bytes, compression_details).
        """
        if data[0:4] == b"SCLZ":
            logging.debug("Decompressing using LZHAM ...")
            import lzham

            dict_size = int.from_bytes(data[4:5], byteorder="big")
            uncompressed_size = int.from_bytes(data[5:9], byteorder="little")

            decompressed = lzham.decompress(data[9:], uncompressed_size, {"dict_size_log2": dict_size})
            return decompressed, {"dict_size": dict_size, "uncompressed_size": uncompressed_size}

        if int.from_bytes(data[0:4], byteorder="little") == zstandard.MAGIC_NUMBER:
            logging.debug("Decompressing using ZSTD ...")
            decompressed = zstandard.decompress(data)
            return decompressed, {"dict_size": None, "uncompressed_size": None}

        logging.debug("Decompressing using LZMA ...")
        data = data[0:9] + (b"\x00" * 4) + data[9:]
        prop = data[0]
        o_prop = prop
        if prop > (4 * 5 + 4) * 9 + 8:
            raise Exception("LZMA properties error")
        decompressed = lzma.LZMADecompressor().decompress(data)
        return decompressed, {"lzma_prop": o_prop}

    def process_csv(self, data, file_path: str):
        if self.is_compressed(data):
            if data[:4] == b"Sig:":
                logging.debug("Stripping Sig: header and signature...")
                data = data[68:]
            decompressed_data, _ = self.decompress(data)
        else:
            decompressed_data = data

        Path(file_path).parent.mkdir(parents=True, exist_ok=True)
        with open(file_path, "wb") as f:
            f.write(decompressed_data)

        with open(file_path, encoding="utf-8") as csvf:
            rows = list(csv.reader(csvf))
        if len(rows) < 2:
            return

        columns = rows[0]
        types_row = rows[1]

        if len(columns) > 1 and columns[1] == "GlobalID":
            columns = [columns[0]] + columns[2:] + [columns[1]]
            types_row = [types_row[0]] + types_row[2:] + [types_row[1]]

            reordered_rows = []
            for row in rows[2:]:
                if len(row) > 1:
                    reordered_row = [row[0]] + row[2:] + [row[1]]
                    reordered_rows.append(reordered_row)
                else:
                    reordered_rows.append(row)
            rows = rows[:2] + reordered_rows

        is_numeric_level = types_row[1].lower() == "int" or "level" in columns[1].lower()

        final_data = {}
        current_troop = None
        level_counter = 0
        current_level = None

        for row in rows[2:]:
            if not any(cell.strip() for cell in row):
                continue

            if row[0].strip():
                current_troop = row[0].strip()
                final_data[current_troop] = {}
                if not is_numeric_level:
                    level_counter = 1
                current_level = None

            if current_troop is None:
                continue

            if is_numeric_level:
                if len(row) > 1 and row[1].strip():
                    current_level = row[1].strip()
                if not current_level:
                    continue
                lvl_key = current_level
            else:
                if level_counter == 0:
                    level_counter = 1
                lvl_key = str(level_counter)
                level_counter += 1

            level_dict = final_data[current_troop].setdefault(lvl_key, {})

            for idx, col_name in enumerate(columns):
                if idx >= len(row):
                    break
                val = row[idx].strip()
                if val == "":
                    continue
                low = val.lower()
                if low == "true":
                    conv = True
                elif low == "false":
                    conv = False
                elif val.isdigit() or (val.startswith("-") and val[1:].isdigit()):
                    conv = int(val)
                else:
                    conv = val
                level_dict[col_name] = conv

        for troop, levels in list(final_data.items()):
            lvl_keys = sorted(levels.keys(), key=lambda x: int(x) if x.isdigit() else 999999)
            if len(lvl_keys) <= 1:
                continue
            base = lvl_keys[0]
            for col in list(levels[base].keys()):
                if not any(col in levels[l] for l in lvl_keys[1:]):
                    final_data[troop][col] = levels[base][col]
                    del levels[base][col]

        for troop in list(final_data.keys()):
            levels = final_data[troop]
            if isinstance(levels, dict) and len(levels) == 1:
                only_lvl, data_dict = next(iter(levels.items()))
                if isinstance(data_dict, dict):
                    final_data[troop] = data_dict

        save_file = file_path.replace(".csv", ".json")
        with open(save_file, "w", encoding="utf-8") as jf:
            json.dump(final_data, jf, indent=2)

        if not self.KEEP_CSV:
            try:
                os.remove(file_path)
            except OSError as e:
                logging.warning(f"Could not delete {file_path}: {e}")

    def process_animations_csv(self, data, file_path: str):
        if self.is_compressed(data):
            try:
                if data[:4] == b"Sig:":
                    data = data[68:]
                decompressed_data, _ = self.decompress(data)
            except Exception:
                decompressed_data = data
        else:
            decompressed_data = data

        Path(file_path).parent.mkdir(parents=True, exist_ok=True)
        with open(file_path, "wb") as f:
            f.write(decompressed_data)

        with open(file_path, newline="", encoding="utf-8") as csvf:
            rows = list(csv.reader(csvf))

        final_data = {}
        index = 0
        while index < len(rows):
            row = rows[index]
            character_name = row[0].strip() if row else ""
            if not character_name:
                index += 1
                continue

            if index + 1 >= len(rows):
                break

            header_row = rows[index + 1]
            columns = [cell.strip() for cell in header_row]
            if "HasDirections" not in columns:
                index += 1
                while index < len(rows) and not (rows[index] and rows[index][0].strip()):
                    index += 1
                continue

            column_map = {name: position for position, name in enumerate(columns) if name}
            current_export_name = None
            next_index = index + 3
            character_swf = None
            animations = []
            seen_animations = set()

            while next_index < len(rows):
                data_row = rows[next_index]
                if data_row and data_row[0].strip():
                    break

                has_directions = (
                    data_row[column_map["HasDirections"]].strip().upper()
                    if len(data_row) > column_map["HasDirections"]
                    else ""
                )
                export_name = (
                    data_row[column_map["ExportName"]].strip()
                    if "ExportName" in column_map and len(data_row) > column_map["ExportName"]
                    else ""
                )
                swf_value = (
                    data_row[column_map["SWF"]].strip()
                    if "SWF" in column_map and len(data_row) > column_map["SWF"]
                    else ""
                )

                if export_name:
                    current_export_name = export_name
                if swf_value and not character_swf:
                    character_swf = swf_value

                if has_directions == "TRUE" and current_export_name and current_export_name not in seen_animations:
                    seen_animations.add(current_export_name)
                    animations.append(current_export_name)

                next_index += 1

            if character_swf:
                final_data[character_name] = {"swf": character_swf, "animations": animations}

            index = next_index

        save_file = file_path.replace(".csv", ".json")
        with open(save_file, "w", encoding="utf-8") as jf:
            json.dump(final_data, jf, indent=2)

        if not self.KEEP_CSV:
            try:
                os.remove(file_path)
            except OSError as e:
                logging.warning(f"Could not delete {file_path}: {e}")

    def is_compressed(self, data):
        if data[:4] == b"Sig:":
            return True
        if data[:4] == b"SCLZ":
            return True
        if len(data) >= 4 and int.from_bytes(data[0:4], byteorder="little") == zstandard.MAGIC_NUMBER:
            return True
        if data[0] == 0x5D:
            return True
        if data[:2] == b"\x53\x43":
            return True
        if data[:6] == b'"Name"' or data[:6] == b'"name"' or data[:5] == b'"TID"':
            return False
        return True

    def open_file(self, file_path: str) -> dict[str, Any]:
        with open(file_path, "r", encoding="utf-8") as f:
            data = json.load(f)
        if not isinstance(data, dict):
            raise TypeError(f"Expected object at {file_path}")
        return data

    def clean_name(self, s: str | None) -> str:
        if not s:
            return "unknown"
        return s.lower().replace(" ", "_").replace(".", "")

    def _translate(self, tid: str | None) -> str | None:
        if not tid:
            return None
        self.USED_TIDS.add(tid)
        return self.translation_data.get(tid, {}).get("EN")

    def _parse_upgrade_time(self, level_data: dict) -> int:
        upgrade_time_seconds = (level_data.get("BuildTimeD") or level_data.get("UpgradeTimeD", 0)) * 24 * 60 * 60
        upgrade_time_seconds += (level_data.get("BuildTimeH") or level_data.get("UpgradeTimeH", 0)) * 60 * 60
        upgrade_time_seconds += (level_data.get("BuildTimeM") or level_data.get("UpgradeTimeM", 0)) * 60
        upgrade_time_seconds += level_data.get("BuildTimeS") or level_data.get("UpgradeTimeS", 0)
        return upgrade_time_seconds

    def _parse_resource(self, resource: str | None) -> str | None:
        if not resource:
            return None
        resource_TID = self.full_resource_data.get(resource, {}).get("TID")
        return self._translate(resource_TID)

    def _parse_translation_data(self):
        full_translation_data = self.open_file("localization/texts.json")
        other_translations = []
        for path in sorted(Path("localization").glob("*.json")):
            if "text" in path.stem.lower():
                continue
            with open(path, "r", encoding="utf-8") as f:
                other_translations.append((path.stem, json.load(f)))

        new_translation_data = {}
        for translation_key, translation_data in full_translation_data.items():
            new_translation_data[translation_key] = {"EN": translation_data.get("EN")}
            for lang, language_data in other_translations:
                entry = language_data.get(translation_key)
                if not isinstance(entry, dict):
                    continue
                new_translation_data[translation_key][lang.upper()] = entry.get(lang.upper())

        self.translation_data = new_translation_data
        return new_translation_data

    def _parse_achievement_data(self):
        self.full_achievement_data = self.open_file("logic/achievements.json")
        new_achievement_data = {}
        for achievement_name, achievement_data in self.full_achievement_data.items():
            tid = achievement_data.get("TID")
            group_map = {0: "home", 1: "builderBase", 2: "clanCapital"}
            if tid not in new_achievement_data:
                new_achievement_data[tid] = {
                    "name": self._translate(achievement_data.get("TID")),
                    "info": self._translate(achievement_data.get("InfoTID")),
                    "completed_message": self._translate(achievement_data.get("CompletedTID")),
                    "TID": {
                        "name": achievement_data.get("TID"),
                        "info": achievement_data.get("InfoTID"),
                        "completed_message": achievement_data.get("CompletedTID"),
                    },
                    "village": group_map.get(achievement_data["UIGroup"]),
                    "ui_priority": achievement_data.get("UIPriority", 0),
                    "levels": [
                        {
                            "level": achievement_data.get("Level") + 1,
                            "action_count": achievement_data.get("ActionCount"),
                            "action_data": achievement_data.get("ActionData"),
                            "xp": achievement_data.get("ExpReward", 0),
                            "gems": achievement_data.get("DiamondReward", 0),
                        }
                    ],
                }
            else:
                new_achievement_data[tid]["levels"].append(
                    {
                        "level": achievement_data.get("Level") + 1,
                        "action_count": achievement_data.get("ActionCount"),
                        "action_data": achievement_data.get("ActionData"),
                        "xp": achievement_data.get("ExpReward", 0),
                        "gems": achievement_data.get("DiamondReward", 0),
                    }
                )

        return list(new_achievement_data.values())

    def _parse_building_data(self):
        self.full_building_data = self.open_file("logic/buildings.json")
        self.full_supercharges_data = self.open_file("logic/mini_levels.json")
        self.full_townhall_data = self.open_file("logic/townhall_levels.json")
        full_weapon_data: dict = self.open_file("logic/weapons.json")

        new_building_data = []

        for building_name, building_data in self.full_building_data.items():
            if (
                building_data.get("BuildingClass") in ["Npc", "NonFunctional", "Npc Town Hall"]
                or "Unused" in building_name
            ):
                continue

            village_type = building_data.get("VillageType", 0)
            superchargeable = False

            supercharge_level_data = {}
            for supercharge_data in self.full_supercharges_data.values():
                if supercharge_data.get("TargetBuilding") == building_name:
                    superchargeable = True
                    hold_data = {
                        "upgrade_resource": self._parse_resource(resource=supercharge_data.get("BuildResource")),
                        "levels": [],
                    }
                    for level, level_data in supercharge_data.items():
                        if not isinstance(level_data, dict):
                            continue
                        upgrade_time_seconds = self._parse_upgrade_time(level_data)

                        DPS = level_data.get("DPS", 0)
                        # if the level doesnt have a DPS & there is no hitpoints for this row, that means it is a DPS upgrade
                        # unless it is a resource pump, but we dont handle those anyways
                        if not DPS and not level_data.get("Hitpoints"):
                            DPS = supercharge_data.get("DPS", 0)
                        hold_data["levels"].append(
                            {
                                "level": int(level),
                                "build_cost": level_data.get("BuildCost"),
                                "build_time": upgrade_time_seconds,
                                "hitpoints_buff": level_data.get("Hitpoints", 0),
                                "dps_buff": DPS,
                            }
                        )
                    supercharge_level_data = hold_data
                    break

            # for merged buildings, move the requirement to level 1 since that is when the requirement is actually needed
            if building_data.get("MergeRequirement") is not None:
                building_data["1"]["MergeRequirement"] = building_data.get("MergeRequirement")

            hold_data = {
                "_id": building_data.get("GlobalID"),
                "name": self._translate(tid=building_data.get("TID")),
                "info": self._translate(tid=building_data.get("InfoTID")),
                "TID": {"name": building_data.get("TID"), "info": building_data.get("InfoTID")},
                "type": building_data.get("BuildingClass"),
                # we are going to do this purely for builder hut purposes bc lv 1 is gems
                "upgrade_resource": self._parse_resource(
                    resource=building_data.get("BuildResource")
                    or (building_data.get("2", {}).get("BuildResource") if isinstance(building_data.get("2"), dict) else None)
                ),
                "village": "home" if not village_type else "builderBase",
                "width": building_data.get("Width", 1),  # walls are null for some reason, so let's make it 1
                "superchargeable": superchargeable,
                "levels": [],
            }

            # put seasonal defense onto the crafting station
            if building_data.get("GlobalID") == 1000097:
                # seasonal defenses are a max townhall thing
                seasonal_def_data = self._parse_seasonal_defense_data()
                hold_data["seasonal_defenses"] = seasonal_def_data

            if building_data.get("GearUpLevelRequirement"):
                gear_up_building = self.full_building_data.get(building_data.get("GearUpBuilding"))
                hold_data["gear_up"] = {
                    "level_required": building_data.get("GearUpLevelRequirement"),
                    "resource": self._parse_resource(resource=building_data.get("GearUpResource")),
                    "building_id": gear_up_building.get("GlobalID") if isinstance(gear_up_building, dict) else None,
                }

            for level, level_data in building_data.items():
                if not isinstance(level_data, dict):
                    continue

                upgrade_time_seconds = self._parse_upgrade_time(level_data)
                hold_level_data = {
                    "level": level_data.get("BuildingLevel"),
                    "build_cost": level_data.get("BuildCost", 0),
                    "build_time": upgrade_time_seconds,
                    "required_townhall": level_data.get("TownHallLevel"),
                    "hitpoints": level_data.get("Hitpoints", 0),
                    "dps": level_data.get("DPS", 0) or level_data.get("Damage", 0),
                }

                if "StrengthWeight" in level_data:
                    hold_level_data["strength_weight"] = level_data["StrengthWeight"]

                if "AltBuildResource" in level_data:
                    # a wall specific thing since they can use gold + elixir at certain levels
                    hold_level_data["alt_upgrade_resource"] = self._parse_resource(
                        resource=level_data["AltBuildResource"]
                    )

                if merge_requirement := level_data.get("MergeRequirement"):
                    merge_list = []
                    buildings = merge_requirement.split(";")
                    for building in buildings:
                        name, level, geared_up = building.split(":")
                        merge_building_data = self.full_building_data.get(name)
                        if not isinstance(merge_building_data, dict):
                            continue
                        merge_list.append(
                            {
                                "name": self._translate(tid=merge_building_data.get("TID")),
                                "_id": merge_building_data.get("GlobalID"),
                                "geared_up": True if geared_up == "1" else False,
                                "level": int(level),
                            }
                        )

                    hold_level_data["merge_requirement"] = merge_list

                if (weapon_name := level_data.get("Weapon")) is not None:
                    weapon_data: dict = full_weapon_data[weapon_name]

                    # if the townhall only has 1 level of weapon, then it is inherently part of the base level,
                    # so just set the dps and continue
                    if weapon_data.get("1") is None:
                        hold_level_data["dps"] = weapon_data.get("DPS")
                    else:
                        hold_weapon_data = {
                            "name": self._translate(tid=weapon_data["TID"]),
                            "info": self._translate(tid=weapon_data["InfoTID"]),
                            "TID": {"name": weapon_data.get("TID"), "info": weapon_data.get("InfoTID")},
                            "upgrade_resource": self._parse_resource(resource=building_data.get("BuildResource")),
                            "strength_weight": level_data.get("StrengthWeight", 0),
                            "levels": [],
                        }
                        for weapon_level, weapon_level_data in weapon_data.items():
                            if not isinstance(weapon_level_data, dict):
                                continue

                            upgrade_time_seconds = self._parse_upgrade_time(weapon_level_data)
                            hold_weapon_data["levels"].append(
                                {
                                    "level": weapon_level_data.get("Level"),
                                    "build_cost": level_data.get("BuildCost"),
                                    "build_time": upgrade_time_seconds,
                                    "dps": weapon_level_data.get("DPS"),
                                }
                            )
                        hold_level_data["weapon"] = hold_weapon_data

                hold_data["levels"].append(hold_level_data)

            if superchargeable:
                # supercharges are always on the last available level
                hold_data["levels"][-1]["supercharge"] = supercharge_level_data
            new_building_data.append(hold_data)

        lab_data = next((item for item in new_building_data if item["name"] == "Laboratory")).get("levels")
        lab_to_townhall = {spot: level_data.get("required_townhall") for spot, level_data in enumerate(lab_data, 1)}
        lab_to_townhall[-1] = 1  # there are troops with no lab ...
        lab_to_townhall[0] = 2
        self.lab_to_townhall = lab_to_townhall

        blacksmith_data = next((item for item in new_building_data if item["name"] == "Blacksmith")).get("levels")
        self.smithy_to_townhall = {
            spot: level_data.get("required_townhall") for spot, level_data in enumerate(blacksmith_data, 1)
        }

        pet_house_data = next((item for item in new_building_data if item["name"] == "Pet House")).get("levels")
        self.pethouse_to_townhall = {
            spot: level_data.get("required_townhall") for spot, level_data in enumerate(pet_house_data, 1)
        }

        bb_lab_data = next((item for item in new_building_data if item["name"] == "Star Laboratory")).get("levels")
        self.bb_lab_to_townhall = {
            spot: level_data.get("required_townhall") for spot, level_data in enumerate(bb_lab_data, 1)
        }

        townhall_unlocks, builderhall_unlocks = self._parse_hall_data()

        for building in new_building_data:
            unlocks = []
            if building["_id"] == 1000001:  # townhall id
                unlocks = townhall_unlocks
            elif building["_id"] == 1000034:  # builderhall id
                unlocks = builderhall_unlocks

            for unlock_data in unlocks:
                building["levels"][(unlock_data["level"] - 1)]["unlocks"] = unlock_data["buildings_unlocked"]

        return new_building_data

    def _parse_seasonal_defense_data(self):
        full_seasonal_defenses = self.open_file("logic/seasonal_defense_archetypes.json")
        full_seasonal_modules = self.open_file("logic/seasonal_defense_modules.json")
        full_season_data = self.open_file("logic/seasonal_defense.json")

        seasons = []
        for season_data in full_season_data.values():
            seasons.append(season_data)

        current_season = next((item for item in reversed(seasons) if item.get("TID")), {})
        current_seasonal_defenses = [
            v.get("Archetypes")
            for k, v in current_season.items()
            if k.isdigit() and isinstance(v, dict) and isinstance(v.get("Archetypes"), str)
        ]

        for _id, (n, d) in enumerate(full_seasonal_modules.items(), 102000000):
            d["_id"] = _id

        current_max_townhall = int(list(self.full_townhall_data.keys())[-1])
        new_seasonal_defense_data = []
        for _id, (seasonal_def_name, seasonal_def_data) in enumerate(full_seasonal_defenses.items(), 103000000):
            if seasonal_def_name not in current_seasonal_defenses:
                continue

            season_defense_ability = self.full_abilities_data.get(seasonal_def_data.get("SpecialAbility"))
            if not isinstance(season_defense_ability, dict):
                continue

            name_TID = season_defense_ability.get("OverrideTID")
            info_TID = season_defense_ability.get("OverrideInfoTID")

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=name_TID),
                "info": self._translate(tid=info_TID),
                "TID": {"name": name_TID, "info": info_TID},
                "required_townhall": current_max_townhall,
                "modules": [],
            }
            for count, module in enumerate(seasonal_def_data.get("Modules").split(";"), 1):
                module_data = full_seasonal_modules.get(module)
                if not isinstance(module_data, dict):
                    continue

                module_hold_data = {
                    "_id": module_data.get("_id"),
                    "name": self._translate(tid=module_data.get("TID")),
                    "TID": {"name": module_data.get("TID")},
                    "upgrade_resource": self._parse_resource(module_data.get("BuildResource")),
                    "levels": [],
                }
                for level, level_data in module_data.items():
                    if not isinstance(level_data, dict):
                        continue
                    upgrade_time_seconds = self._parse_upgrade_time(level_data)

                    special_ability = module_data.get("SpecialAbility")
                    if not isinstance(special_ability, str):
                        continue
                    full_ability_data = self.full_abilities_data.get(special_ability)
                    if not isinstance(full_ability_data, dict):
                        continue
                    ability_data = full_ability_data.get(level)
                    if not isinstance(ability_data, dict):
                        continue
                    ability_data.pop("ActivateFromGameSystem", None)
                    ability_data.pop("DeactivateFromGameSystem", None)
                    ability_data.pop("Level", None)

                    module_hold_data["levels"].append(
                        {
                            "level": int(level),
                            "build_cost": level_data.get("BuildCost"),
                            "build_time": upgrade_time_seconds,
                            "ability_data": ability_data,
                        }
                    )

                hold_data["modules"].append(module_hold_data)

            new_seasonal_defense_data.append(hold_data)

        return new_seasonal_defense_data

    def _parse_troop_data(self):
        self.full_troop_data = self.open_file("logic/characters.json")
        full_super_troop_data = self.open_file("logic/super_licences.json")
        full_super_troop_data = {v.get("Replacement"): v for k, v in full_super_troop_data.items()}

        name_to_id = {}
        new_troop_data = []
        for troop_name, troop_data in self.full_troop_data.items():
            if troop_data.get("DisableProduction", False):
                continue
            village_type = troop_data.get("VillageType", 0)
            production_building_data = self.full_building_data.get(troop_data.get("ProductionBuilding"))
            production_building = production_building_data.get("TID") if isinstance(production_building_data, dict) else None

            name_to_id[(troop_name, village_type)] = troop_data.get("GlobalID")

            self.register_sc_asset(
                source_sc=troop_data.get("IconSWF"),
                asset_name=troop_data.get("IconExportName"),
                save_path=f'troops/{self.clean_name(self._translate(tid=troop_data.get("TID")))}/icon',
            )

            # this is where we need to do processing, go to the file & grab that target file
            hold_data = {
                "_id": troop_data.get("GlobalID"),
                "name": self._translate(tid=troop_data.get("TID")),
                "info": self._translate(tid=troop_data.get("InfoTID")),
                "TID": {"name": troop_data.get("TID"), "info": troop_data.get("InfoTID")},
                "production_building": self._translate(tid=production_building),
                "production_building_level": troop_data.get("BarrackLevel"),
                "upgrade_resource": self._parse_resource(resource=troop_data.get("UpgradeResource")),
                "is_flying": troop_data.get("IsFlying"),
                "is_air_targeting": troop_data.get("AirTargets"),
                "is_ground_targeting": troop_data.get("GroundTargets"),
                "movement_speed": troop_data.get("Speed", 0),
                "attack_speed": troop_data.get("AttackSpeed", 0),
                "attack_range": troop_data.get("AttackRange", 0),
                "housing_space": troop_data.get("HousingSpace"),
                "village": "home" if not village_type else "builderBase",
            }

            is_super_troop = troop_data.get("EnabledBySuperLicence", False)
            is_seasonal_troop = troop_data.get("EnabledByCalendar", False)
            super_troop_data = None
            if is_super_troop:
                super_troop_data = full_super_troop_data.get(troop_name)
                if not isinstance(super_troop_data, dict):
                    continue
                original_name = super_troop_data.get("Original")
                min_original_level = super_troop_data.get("MinOriginalLevel")
                if not isinstance(original_name, str) or min_original_level is None:
                    continue
                hold_data["super_troop"] = {
                    "original_id": name_to_id[(original_name, 0)],
                    "original_min_level": min_original_level,
                }
            if is_seasonal_troop:
                hold_data["is_seasonal"] = True
            hold_data["levels"] = []

            max_townhall_converter = self.lab_to_townhall
            if troop_data.get("ProductionBuilding") == "Barrack2":
                max_townhall_converter = self.bb_lab_to_townhall

            for level, level_data in troop_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                if not is_super_troop and not is_seasonal_troop:
                    required_lab_level = level_data.get("LaboratoryLevel")
                    required_townhall = (
                        max_townhall_converter.get(required_lab_level) if isinstance(required_lab_level, int) else None
                    )
                elif is_super_troop:  # for super troops use the original troop's lab level'
                    if not isinstance(super_troop_data, dict):
                        continue
                    original_name = super_troop_data.get("Original")
                    if not isinstance(original_name, str):
                        continue
                    original_troop = self.full_troop_data.get(original_name)
                    if not isinstance(original_troop, dict):
                        continue
                    original_level_data = original_troop.get(level)
                    if not isinstance(original_level_data, dict):
                        continue
                    required_lab_level = original_level_data.get("LaboratoryLevel")
                    required_townhall = (
                        max_townhall_converter.get(required_lab_level) if isinstance(required_lab_level, int) else None
                    )
                elif is_seasonal_troop:
                    required_lab_level = None
                    required_townhall = level_data.get("UpgradeLevelByTH")
                else:
                    continue

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints", 0),
                    "dps": level_data.get("DPS", 0),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_lab_level": required_lab_level,
                    "required_townhall": required_townhall,
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            if not hold_data["levels"]:
                continue
            new_troop_data.append(hold_data)

        return new_troop_data

    def _parse_guardian_data(self):
        full_guardian_data = self.open_file("logic/guardians.json")

        new_guardian_data = []
        for _id, (guardian_name, guardian_data) in enumerate(full_guardian_data.items(), 107000000):
            if guardian_data.get("Deprecated", False):
                continue
            character_data = self.full_troop_data.get(guardian_data.get("CharacterDatas"))
            if not isinstance(character_data, dict):
                continue

            self.register_sc_asset(
                source_sc=guardian_data.get("IconSWF"),
                asset_name=guardian_data.get("IconExportName"),
                save_path=f'guardians/{self.clean_name(self._translate(tid=guardian_data.get("TID")))}/icon'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=guardian_data.get("TID")),
                "info": self._translate(tid=guardian_data.get("InfoTID")),
                "TID": {"name": guardian_data.get("TID"), "info": guardian_data.get("InfoTID")},
                "upgrade_resource": self._parse_resource(resource=character_data.get("UpgradeResource")),
                "is_flying": character_data.get("IsFlying"),
                "is_air_targeting": character_data.get("AirTargets"),
                "is_ground_targeting": character_data.get("GroundTargets"),
                "movement_speed": character_data.get("Speed"),
                "attack_speed": character_data.get("AttackSpeed"),
                "attack_range": character_data.get("AttackRange"),
                "levels": [],
            }

            for level, level_data in character_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                # hard coded for now, didn't find where this is defined, except "HousesGuardians" on the Townhall data
                required_townhall = 18

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints"),
                    "dps": level_data.get("DPS"),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_townhall": required_townhall,
                }
                hold_data["levels"].append(new_level_data)

            new_guardian_data.append(hold_data)

        return new_guardian_data

    def _parse_spell_data(self):
        full_spell_data = self.open_file("logic/spells.json")

        new_spell_data = []
        for spell_name, spell_data in full_spell_data.items():
            if spell_data.get("DisableProduction", False):
                continue

            self.register_sc_asset(
                source_sc=spell_data.get("IconSWF"),
                asset_name=spell_data.get("IconExportName"),
                save_path=f'spells/{self.clean_name(self._translate(spell_data.get("TID")))}',
            )

            production_building_data = self.full_building_data.get(spell_data.get("ProductionBuilding"))
            production_building = production_building_data.get("TID") if isinstance(production_building_data, dict) else None
            hold_data = {
                "_id": spell_data.get("GlobalID"),
                "name": self._translate(spell_data.get("TID")),
                "info": self._translate(spell_data.get("InfoTID")),
                "TID": {"name": spell_data.get("TID"), "info": spell_data.get("InfoTID")},
                "production_building": self._translate(production_building),
                "production_building_level": spell_data.get("SpellForgeLevel"),
                "upgrade_resource": self._parse_resource(resource=spell_data.get("UpgradeResource")),
                "housing_space": spell_data.get("HousingSpace"),
            }
            is_seasonal_spell = spell_data.get("EnabledByCalendar", False)
            if is_seasonal_spell:
                hold_data["is_seasonal"] = is_seasonal_spell
            hold_data["levels"] = []

            for level, level_data in spell_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                duration_ms = level_data.get("NumberOfHits", 0) * level_data.get("TimeBetweenHitsMS", 0)
                radius = spell_data.get("Radius", 0) or level_data.get("Radius", 0)
                lab_level = level_data.get("LaboratoryLevel")
                new_level_data = {
                    "level": int(level),
                    "duration": int(duration_ms / 1000),
                    "radius": round(radius / 100, 1),
                    "damage": level_data.get("Damage", 0) or level_data.get("PoisonDPS", 0),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_lab_level": lab_level,
                    "required_townhall": level_data.get("UpgradeLevelByTH")
                    or (self.lab_to_townhall.get(lab_level) if isinstance(lab_level, int) else None),
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            if not hold_data["levels"]:
                continue
            new_spell_data.append(hold_data)

        return new_spell_data

    def _parse_hero_data(self):
        self.full_hero_data = self.open_file("logic/heroes.json")

        new_hero_data = []
        for _id, (hero_name, hero_data) in enumerate(self.full_hero_data.items(), 28000000):
            village_type = hero_data.get("VillageType", 0)
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=hero_data.get("TID")),
                "info": self._translate(hero_data.get("InfoTID")),
                "TID": {"name": hero_data.get("TID"), "info": hero_data.get("InfoTID")},
                "production_building": self._translate(tid="TID_HERO_TAVERN") if not village_type else None,
                "production_building_level": hero_data.get("1", {}).get("RequiredHeroTavernLevel"),
                "upgrade_resource": self._parse_resource(resource=hero_data.get("UpgradeResource")),
                "is_flying": hero_data.get("IsFlying"),
                "is_air_targeting": hero_data.get("AirTargets"),
                "is_ground_targeting": hero_data.get("GroundTargets"),
                "movement_speed": hero_data.get("Speed"),
                "attack_speed": hero_data.get("AttackSpeed"),
                "attack_range": hero_data.get("AttackRange"),
                "village": "home" if not village_type else "builderBase",
                "levels": [],
            }

            for level, level_data in hero_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints"),
                    "dps": level_data.get("DPS"),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_townhall": level_data.get("RequiredTownHallLevel"),
                    "required_hero_tavern_level": level_data.get("RequiredHeroTavernLevel"),
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            new_hero_data.append(hold_data)

        return new_hero_data

    def _parse_pet_data(self):
        full_pet_data = self.open_file("logic/pets.json")

        new_pet_data = []
        for _id, (pet_name, pet_data) in enumerate(full_pet_data.items(), 73000000):
            if pet_data.get("Deprecated", False) or pet_name in ["Phoenix Egg"]:
                continue

            self.register_sc_asset(
                source_sc=pet_data.get("IconSWF") ,
                asset_name=pet_data.get("IconExportName"),
                save_path=f'pets/{self.clean_name(self._translate(tid=pet_data.get("TID")))}/icon'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=pet_data.get("TID")),
                "info": self._translate(tid=pet_data.get("InfoTID")),
                "TID": {"name": pet_data.get("TID"), "info": pet_data.get("InfoTID")},
                "production_building": self._translate(tid="TID_PET_SHOP"),
                "production_building_level": pet_data.get("1", {}).get("LaboratoryLevel")
                if isinstance(pet_data.get("1"), dict)
                else None,
                "upgrade_resource": self._parse_resource('DarkElixir'),
                "is_flying": pet_data.get("IsFlying"),
                "is_air_targeting": pet_data.get("AirTargets"),
                "is_ground_targeting": pet_data.get("GroundTargets"),
                "movement_speed": pet_data.get("Speed"),
                "attack_speed": pet_data.get("AttackSpeed"),
                "attack_range": pet_data.get("AttackRange"),
                "levels": [],
            }

            for level, level_data in pet_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)
                pet_house_level = level_data.get("LaboratoryLevel")

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints"),
                    "dps": level_data.get("DPS"),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_pet_house_level": pet_house_level,
                    "required_townhall": self.pethouse_to_townhall.get(pet_house_level)
                    if isinstance(pet_house_level, int)
                    else None,
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            new_pet_data.append(hold_data)

        return new_pet_data

    def _parse_equipment_data(self):
        full_equipment_data = self.open_file("logic/character_items.json")

        new_equipment_data = []
        for _id, (equipment_name, equipment_data) in enumerate(full_equipment_data.items(), 90000000):
            if equipment_data.get("Deprecated", False):
                continue

            self.register_sc_asset(
                source_sc=equipment_data.get("IconSWF"),
                asset_name=equipment_data.get("IconExportName"),
                save_path=f'equipment/{self.clean_name(self._translate(tid=equipment_data.get("TID")))}'
            )

            main_abilities = equipment_data.get("MainAbilities").split(";")
            extra_abilities = equipment_data.get("ExtraAbilities", "").split(";")
            allowed_characters = str(equipment_data.get("AllowedCharacters", ""))
            first_allowed_character = allowed_characters.split(";")[0] if allowed_characters else ""
            hero_data = self.full_hero_data.get(first_allowed_character)
            hero_TID = hero_data.get("TID") if isinstance(hero_data, dict) else None
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=equipment_data.get("TID")),
                "info": self._translate(tid=equipment_data.get("InfoTID")),
                "TID": {
                    "name": equipment_data.get("TID"),
                    "info": equipment_data.get("InfoTID"),
                    "production_building": "TID_SMITHY",
                },
                "production_building": self._translate(tid="TID_SMITHY"),
                "production_building_level": equipment_data.get("1", {}).get("RequiredBlacksmithLevel")
                if isinstance(equipment_data.get("1"), dict)
                else None,
                "rarity": equipment_data.get("Rarity", "").title(),
                "hero": self._translate(tid=hero_TID),
                "levels": [],
            }

            for level, level_data in equipment_data.items():
                if not isinstance(level_data, dict):
                    continue

                shiny_ore = 0
                glowy_ore = 0
                starry_ore = 0
                upgrade_resources = level_data.get("UpgradeResources", "").split(";")
                upgrade_costs = str(level_data.get("UpgradeCosts", "")).split(";")

                if upgrade_costs[0] != "":
                    for resource, cost in zip(upgrade_resources, upgrade_costs):
                        resource = resource.strip()
                        cost = int(cost)
                        if resource == "CommonOre":
                            shiny_ore += cost
                        elif resource == "RareOre":
                            glowy_ore += cost
                        elif resource == "EpicOre":
                            starry_ore += cost
                smithy_level = level_data.get("RequiredBlacksmithLevel")

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints", 0),
                    "dps": level_data.get("DPS", 0),
                    "heal_on_activation": level_data.get("HealOnActivation", 0),
                    "required_blacksmith_level": smithy_level,
                    "required_townhall": self.smithy_to_townhall.get(smithy_level)
                    if isinstance(smithy_level, int)
                    else None,
                    "strength_weight": level_data.get("StrengthWeight", 0),
                    "upgrade_cost": {"shiny_ore": shiny_ore, "glowy_ore": glowy_ore, "starry_ore": starry_ore},
                }

                main_ability_levels = str(level_data.get("MainAbilityLevels", "")).split(";")

                if main_ability_levels[0] != "":
                    main_ability_json = []
                    for main_ability, main_ability_level in zip(main_abilities, main_ability_levels):
                        full_ability = self.full_abilities_data.get(main_ability)
                        if not isinstance(full_ability, dict):
                            continue
                        ability = full_ability.get(main_ability_level)
                        if not isinstance(ability, dict):
                            continue
                        ability["name"] = self._translate(tid=full_ability.get("TID"))
                        ability["info"] = self._translate(tid=full_ability.get("InfoTID"))
                        main_ability_json.append(ability)

                    if main_ability_json:
                        new_level_data["abilities"] = main_ability_json

                extra_ability_levels = str(level_data.get("ExtraAbilityLevels", "")).split(";")
                if extra_ability_levels[0] != "":
                    extra_ability_json = []
                    for extra_ability, extra_ability_level in zip(extra_abilities, extra_ability_levels):
                        full_ability = self.full_abilities_data.get(extra_ability)
                        if not isinstance(full_ability, dict):
                            continue
                        ability = full_ability.get(extra_ability_level)
                        if isinstance(ability, dict):
                            ability["name"] = self._translate(tid=full_ability.get("TID"))
                            extra_ability_json.append(ability)

                    if extra_ability_json:
                        new_level_data["abilities"].extend(extra_ability_json)

                hold_data["levels"].append(new_level_data)

            new_equipment_data.append(hold_data)

        return new_equipment_data

    def _parse_trap_data(self):
        full_trap_data = self.open_file("logic/traps.json")

        new_trap_data = []
        for trap_name, trap_data in full_trap_data.items():
            if trap_data.get("Disabled", False) or trap_data.get("EnabledByCalendar", False):
                continue
            village_type = trap_data.get("VillageType", 0)

            hold_data = {
                "_id": trap_data.get("GlobalID"),
                "name": self._translate(tid=trap_data.get("TID")),
                "info": self._translate(tid=trap_data.get("InfoTID")),
                "TID": {"name": trap_data.get("TID"), "info": trap_data.get("InfoTID")},
                "width": trap_data.get("Width"),
                "air_trigger": trap_data.get("AirTrigger", False),
                "ground_trigger": trap_data.get("GroundTrigger", False),
                "damage_radius": trap_data.get("DamageRadius"),
                "trigger_radius": trap_data.get("TriggerRadius"),
                "village": "home" if not village_type else "builderBase",
                "upgrade_resource": self._parse_resource(resource=trap_data.get("BuildResource")),
                "levels": [],
            }
            for level, level_data in trap_data.items():
                if not isinstance(level_data, dict):
                    continue

                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                hold_data["levels"].append(
                    {
                        "level": int(level),
                        "build_cost": level_data.get("BuildCost"),
                        "build_time": upgrade_time_seconds,
                        "required_townhall": level_data.get("TownHallLevel"),
                        "damage": level_data.get("Damage", 0),
                        "strength_weight": level_data.get("StrengthWeight", 0),
                    }
                )

            new_trap_data.append(hold_data)

        return new_trap_data

    def _parse_decoration_data(self):
        full_deco_data = self.open_file("logic/decos.json")
        new_deco_data = []
        for _id, (deco_name, deco_data) in enumerate(full_deco_data.items(), 18000000):
            if deco_data.get("TID") in ["TID_DECORATION_GENERIC", "TID_DECORATION_NATIONAL_FLAG"]:
                continue
            village_type = deco_data.get("VillageType", 0)

            hold_data = {
                "_id": _id,
                "name": self._translate(deco_data.get("TID")),
                "TID": {"name": deco_data.get("TID")},
                "width": deco_data.get("Width"),
                "not_in_shop": deco_data.get("NotInShop", False),
                "pass_reward": deco_data.get("BPReward", False),
                "max_count": deco_data.get("MaxCount", 1),
                "build_resource": self._parse_resource(resource=deco_data.get("BuildResource")),
                "build_cost": deco_data.get("BuildCost"),
                "village": "home" if not village_type else "builderBase",
            }
            new_deco_data.append(hold_data)

        return new_deco_data

    def _parse_capital_part_data(self):
        full_capital_part_data = self.open_file("logic/building_parts.json")
        new_capital_part_data = []
        for _id, (part_name, part_data) in enumerate(full_capital_part_data.items(), 82000000):
            if part_data.get("Deprecated", False):
                continue

            source_sc, asset_name = part_data.get("Sprite").split("#", 1)
            self.register_sc_asset(
                source_sc=source_sc,
                asset_name=asset_name,
                save_path=f'capital_house_parts/{_id}'
            )
            name = part_name.replace("PlayerHouse_", "").replace("_", " ").title()
            nums = 0
            for phrase in ["01", "02", "03", "04", "05", "06", "07", "08", "09", "10"]:
                if phrase in name:
                    name = name.replace(phrase, "")
                    nums = int(phrase)
            name = name.split(" ", 1)
            name = f"{name[1]} {name[0]}"
            if nums:
                name = f"{name} {nums}"

            # make it match the API enums
            type_mapping = {"Deco": "decoration"}
            slot_type = type_mapping.get(part_data.get("LayoutSlot"), part_data.get("LayoutSlot").lower())
            new_capital_part_data.append(
                {
                    "_id": _id,
                    "name": name.title(),
                    "slot_type": slot_type,
                    "pass_reward": part_data.get("BattlePassReward", False),
                }
            )

        return new_capital_part_data

    def _parse_obstacle_data(self):
        full_obstacle_data = self.open_file("logic/obstacles.json")

        new_obstacle_data = []
        for _id, (obstacle_name, obstacle_data) in enumerate(full_obstacle_data.items(), 8000000):
            village_type = obstacle_data.get("VillageType", 0)

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=obstacle_data.get("TID")),
                "TID": {"name": obstacle_data.get("TID")},
                "width": obstacle_data.get("Width"),
                "clear_resource": self._parse_resource(resource=obstacle_data.get("ClearResource")),
                "clear_cost": obstacle_data.get("ClearCost"),
                "loot_resource": self._parse_resource(resource=obstacle_data.get("LootResource")),
                "loot_count": obstacle_data.get("LootCount"),
                "village": "home" if not village_type else "builderBase",
            }
            new_obstacle_data.append(hold_data)

        return new_obstacle_data

    def _parse_scenery_data(self):
        full_scenery_data = self.open_file("logic/village_backgrounds.json")

        new_scenery_data = []
        for _id, (scenery_name, scenery_data) in enumerate(full_scenery_data.items(), 60000000):
            type_map = {"WAR": "war", "BB": "builderBase", "HOME": "home"}
            if scenery_data.get("HomeType") not in type_map:
                continue

            if not self._translate(tid=scenery_data.get("TID")):
                continue

            path_name = self.clean_name(self._translate(tid=scenery_data.get("TID")))
            if "Icon" in scenery_data:
                self.register_sc_asset(
                    source_sc=scenery_data["Icon"],
                    asset_name="",
                    save_path=f"sceneries/{path_name}/icon"
                )
            else:
                self.register_sc_asset(
                    source_sc=scenery_data["IconAnimSWF"],
                    asset_name=scenery_data["IconAnimExportName"],
                    save_path=f"sceneries/{path_name}/icon"
                )
            thumbnail_path = self.register_sc_asset(
                source_sc=scenery_data["Thumbnail"],
                asset_name="",
                save_path=f"sceneries/{path_name}/thumbnail"
            )

            music_path = None
            if "Music" in scenery_data:
                self.register_sc_asset(
                    source_sc=scenery_data["Music"],
                    asset_name="",
                    save_path=f"sceneries/{path_name}/music"
                )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=scenery_data.get("TID")),
                "TID": {"name": scenery_data.get("TID")},
                "type": type_map.get(scenery_data.get("HomeType")),
                "music": music_path,
                "thumbnail": thumbnail_path,
            }
            if scenery_data.get("FreeBackground", False):
                scenery_data["free"] = True
            if scenery_data.get("DefaultBackground", False):
                scenery_data["default"] = True

            new_scenery_data.append(hold_data)

        return new_scenery_data

    def _parse_skin_data(self):
        full_skin_data = self.open_file("logic/skins.json")

        new_skins_data = []
        for _id, (skin_name, skin_data) in enumerate(full_skin_data.items(), 52000000):
            character = skin_data.get("character") or skin_data.get("Character")
            if not skin_data.get("TID") or character not in self.full_hero_data.keys() or not skin_data.get("Tier"):
                continue


            self.register_sc_asset(
                source_sc=skin_data["Icon"],
                asset_name="",
                save_path=f'skins/{self.clean_name(self._translate(tid=skin_data.get("TID")))}/icon'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=skin_data.get("TID")),
                "TID": {"name": skin_data.get("TID")},
                "tier": skin_data.get("Tier").title(),
                "character": character,
            }
            new_skins_data.append(hold_data)

        return new_skins_data

    def _parse_helper_data(self):
        full_helper_data = self.open_file("logic/villager_apprentices.json")

        new_helper_data = []
        for _id, (helper_name, helper_data) in enumerate(full_helper_data.items(), 93000000):

            self.register_sc_asset(
                source_sc=helper_data.get("IconSWF"),
                asset_name=helper_data.get("IconExportNameSelect"),
                save_path=f'helpers/{self.clean_name(self._translate(tid=helper_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=helper_data.get("TID")),
                "info": self._translate(tid=helper_data.get("InfoTID")),
                "TID": {"name": helper_data.get("TID"), "info": helper_data.get("InfoTID")},
                "upgrade_resource": self._parse_resource(resource=helper_data.get("CostResource")),
                "levels": [],
            }
            for level, level_data in helper_data.items():
                if not isinstance(level_data, dict):
                    continue
                hold_data["levels"].append(
                    {
                        "level": int(level),
                        "required_townhall": level_data.get("RequiredTownHallLevel"),
                        "upgrade_cost": level_data.get("Cost"),
                        "boost_time_seconds": level_data.get("BoostTimeSeconds"),
                        "boost_multiplier": level_data.get("BoostMultiplier"),
                    }
                )

            new_helper_data.append(hold_data)

        return new_helper_data

    def _parse_war_league_data(self):
        full_war_league_data = self.open_file("logic/war_leagues.json")

        new_war_league_data = []
        for _id, (war_league_name, war_league_data) in enumerate(full_war_league_data.items(), 48000000):
            if not war_league_data.get("Name"):  # skip Unranked, no data
                continue

            new_war_league_data.append(
                {
                    "_id": _id,
                    "name": self._translate(tid=war_league_data.get("TID")),
                    "TID": {"name": war_league_data.get("TID")},
                    "cwl_medals": {
                        "first_place": war_league_data.get("LeagueWinReward"),
                        "position_medal_diff": war_league_data.get("LeaguePosRewardEffect"),
                        "bonus_reward": war_league_data.get("BonusMedalReward"),
                        "minimum_bonus_amount": war_league_data.get("MinNumMedalBonuses"),
                    },
                    "promotions": war_league_data.get("NumPromotions"),
                    "demotions": war_league_data.get("NumDemotions"),
                    "15v15_only": war_league_data.get("AllowFirstWarSizeOnly"),
                }
            )

        return new_war_league_data

    def _parse_league_tier_data(self):
        full_league_tier_data = self.open_file("logic/league_tiers.json")

        new_league_tier_data = []
        for _id, (league_name, league_data) in enumerate(full_league_tier_data.items(), 105000000):
            league_tier = _id - 105000000
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=league_data.get("TID")),
                "league_tier": league_tier,
                "TID": {"name": league_data.get("TID")},
                "group_size": league_data.get("GroupSizeMax"),
                "demote_percentage": league_data.get("DemotePercentage"),
                "promote_percentage": league_data.get("PromotePercentage"),
                "battle_count": league_data.get("MaxBattleCount"),
                "trophy_start": league_data.get("TrophyFloor"),
                "clan_score": league_data.get("TopClanScore"),
                "townhall_cap": None,
                "rewards": [],
            }
            highest_townhall = 0
            rewards = []
            for tier, level_data in league_data.items():
                if not isinstance(level_data, dict):
                    continue
                if tier == "1":  # always empty idky
                    continue
                townhall_level = level_data.get("TH")
                if not isinstance(townhall_level, int):
                    continue
                townhall_data = self.full_townhall_data.get(str(townhall_level))
                th_min_league_tier = townhall_data.get("LeagueTier", 0) if isinstance(townhall_data, dict) else 0
                if league_tier < th_min_league_tier and league_tier != 0:
                    continue
                highest_townhall = max(townhall_level, highest_townhall)
                rewards.append(
                    {
                        "townhall_level": townhall_level,
                        "resources": {
                            "gold": level_data.get("GoldReward"),
                            "elixir": level_data.get("ElixirReward"),
                            "dark_elixir": level_data.get("DarkElixirReward"),
                        },
                        "star_bonus": {
                            "gold": level_data.get("GoldRewardStarBonus"),
                            "elixir": level_data.get("ElixirRewardStarBonus"),
                            "dark_elixir": level_data.get("DarkElixirRewardStarBonus"),
                            "shiny_ore": level_data.get("CommonOreRewardStarBonus"),
                            "glowy_ore": level_data.get("RareOreRewardStarBonus"),
                            "starry_ore": level_data.get("EpicOreRewardStarBonus"),
                        },
                    }
                )
            hold_data["townhall_cap"] = highest_townhall
            hold_data["rewards"] = rewards
            new_league_tier_data.append(hold_data)

        return new_league_tier_data

    def _parse_builder_league_data(self):
        full_builder_league_data = self.open_file("logic/leagues2.json")

        new_league_data = []
        for _id, (league_name, league_data) in enumerate(full_builder_league_data.items(), 44000000):
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=league_data.get("TID")),
                "TID": {"name": league_data.get("TID")},
                "trophy_start": league_data.get("TrophyLimitLow", 0),
            }
            new_league_data.append(hold_data)

        return new_league_data

    def _parse_capital_league_data(self):
        full_capital_league_data = self.open_file("logic/capital_leagues.json")

        new_league_data = []
        for _id, (league_name, league_data) in enumerate(full_capital_league_data.items(), 85000000):
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=league_data.get("TID")),
                "TID": {"name": league_data.get("TID")},
                "trophy_start": league_data.get("RequiredTrophies", 0),
                "clan_xp": league_data.get("ClanXP")
            }
            new_league_data.append(hold_data)

        return new_league_data

    def _parse_magic_items_data(self):
        full_magic_items_data = self.open_file("logic/boosters.json")

        new_magic_items_data = []
        for name, magic_item_data in full_magic_items_data.items():
            _id = hash_15_digits(s=name)

            self.register_sc_asset(
                source_sc=magic_item_data.get("IconSWF"),
                asset_name=magic_item_data.get("IconExportName"),
                save_path=f'magic_items/{self.clean_name(self._translate(tid=magic_item_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=magic_item_data.get("TID")),
                "info": self._translate(tid=magic_item_data.get("InfoTID")),
                "TID": {"name": magic_item_data.get("TID"), "info": magic_item_data.get("InfoTID")},
                "max_inventory": magic_item_data.get("MaxItems"),
                "gem_value": magic_item_data.get("DiamondValue"),
            }
            new_magic_items_data.append(hold_data)

        return new_magic_items_data

    def _parse_magic_snacks_data(self):
        full_magic_snacks_data = self.open_file("logic/consumables.json")

        new_magic_snacks_data = []
        for name, magic_snack_data in full_magic_snacks_data.items():
            _id = hash_15_digits(s=name)

            self.register_sc_asset(
                source_sc=magic_snack_data.get("IconSWF"),
                asset_name=magic_snack_data.get("IconExportName"),
                save_path=f'magic_snacks/{self.clean_name(self._translate(tid=magic_snack_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=magic_snack_data.get("TID")),
                "info": self._translate(tid=magic_snack_data.get("InfoTID")),
                "TID": {"name": magic_snack_data.get("TID"), "info": magic_snack_data.get("InfoTID")},
                "duration": magic_snack_data.get("DurationSeconds", 0),
            }
            new_magic_snacks_data.append(hold_data)

        return new_magic_snacks_data

    def _parse_capital_districts(self):
        full_district_data = self.open_file("logic/capital_districts.json")

        new_district_data = []
        for _id, (district_name, district_data) in enumerate(full_district_data.items(), 70000000):
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=district_data.get("TID")),
                "TID": {"name": district_data.get("TID")},
            }
            new_district_data.append(hold_data)

        return new_district_data

    def _parse_locale_mapping(self):
        full_locale_data = self.open_file("logic/chat_locales.json")
        locales = []
        for locale_data in full_locale_data.values():
            locales.append({
                "locale": locale_data.get("Name"),
                "name": locale_data.get("DisplayName"),
            })
        return locales

    def _parse_clan_labels(self):
        full_label_data = self.open_file("logic/clan_tags.json")

        new_label_data = []
        for name, label_data in full_label_data.items():
            _id = hash_15_digits(s=name)
            self.register_sc_asset(
                source_sc=label_data.get("IconSWF"),
                asset_name=label_data.get("IconExportName"),
                save_path=f"clan_labels/{self.clean_name(self._translate(tid=label_data.get("TID")))}"
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=label_data.get("TID")),
                "TID": {"name": label_data.get("TID")},

            }
            new_label_data.append(hold_data)

        return new_label_data

    def _parse_player_labels(self):
        full_label_data = self.open_file("logic/player_tags.json")

        new_label_data = []
        for name, label_data in full_label_data.items():
            _id = hash_15_digits(s=name)
            self.register_sc_asset(
                source_sc=label_data.get("IconSWF"),
                asset_name=label_data.get("IconExportName"),
                save_path=f'player_labels/{self.clean_name(self._translate(tid=label_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=label_data.get("TID")),
                "TID": {"name": label_data.get("TID")},
            }
            new_label_data.append(hold_data)

        return new_label_data

    def _parse_chests_data(self):
        full_chest_data = self.open_file("logic/random_reward_pools.json")
        # full_chest_reward_data = self.open_file("logic/random_reward_boxes.json")

        new_chests_data = []
        for name, chest_data in full_chest_data.items():
            _id = hash_15_digits(s=name)
            self.register_sc_asset(
                source_sc=chest_data.get("IconSWF"),
                asset_name=chest_data.get("IconExportName"),
                save_path=f'chests/{self.clean_name(self._translate(tid=chest_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=chest_data.get("TID")),
                "info": self._translate(tid=chest_data.get("InfoTID")),
                "TID": {"name": chest_data.get("TID"), "info": chest_data.get("InfoTID")},
            }
            new_chests_data.append(hold_data)

        return new_chests_data

    def _parse_resource_data(self):
        full_resource_data = self.open_file("logic/resources.json")

        new_resources_data = []
        for name, resource_data in full_resource_data.items():
            _id = hash_15_digits(s=name)
            if not resource_data.get("TID") or not resource_data.get("IconExportName"):
                continue

            self.register_sc_asset(
                source_sc=resource_data.get("IconSWF"),
                asset_name=resource_data.get("IconExportName"),
                save_path=f'resources/{self.clean_name(self._translate(tid=resource_data.get("TID")))}'
            )
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=resource_data.get("TID")),
                "TID": {"name": resource_data.get("TID")},
            }
            new_resources_data.append(hold_data)

        return new_resources_data

    def _parse_hall_data(self):
        builderhall_data = []
        townhall_data = []
        id_quantity_map = {}

        for _id, (hall_level, hall_data) in enumerate(self.full_townhall_data.items(), 1):
            builderhall_unlocks = []
            townhall_unlocks = []
            for field, data in hall_data.items():
                building_data = self.full_building_data.get(field)
                if not building_data:
                    continue
                village_type = building_data.get("VillageType", 0)
                id = building_data.get("GlobalID")
                quantity = data

                current_quantity = id_quantity_map.get(id, 0)
                new_quantity = quantity - current_quantity

                if not new_quantity:
                    continue

                if id not in id_quantity_map:
                    id_quantity_map[id] = new_quantity
                else:
                    id_quantity_map[id] += new_quantity

                if not village_type:  # is home village
                    townhall_unlocks.append(
                        {"name": self._translate(tid=building_data.get("TID")), "_id": id, "quantity": new_quantity}
                    )
                else:
                    builderhall_unlocks.append(
                        {"name": self._translate(tid=building_data.get("TID")), "_id": id, "quantity": new_quantity}
                    )
            townhall_data.append({"level": _id, "buildings_unlocked": townhall_unlocks})
            if builderhall_unlocks:
                builderhall_data.append({"level": _id, "buildings_unlocked": builderhall_unlocks})

        return townhall_data, builderhall_data

    def create_master_json(self):
        self._parse_translation_data()

        self.full_abilities_data = self.open_file("logic/special_abilities.json")
        self.full_resource_data = self.open_file("logic/resources.json")
        self.animations_data = self.open_file("csv/animations.json")

        master_data = {
            "buildings": sorted(self._parse_building_data(), key=lambda x: x["_id"]),
            "traps": self._parse_trap_data(),
            "troops": sorted(self._parse_troop_data(), key=lambda x: x["_id"]),
            "guardians": self._parse_guardian_data(),
            "spells": sorted(self._parse_spell_data(), key=lambda x: x["_id"]),
            "heroes": self._parse_hero_data(),
            "pets": self._parse_pet_data(),
            "equipment": self._parse_equipment_data(),
            "decorations": self._parse_decoration_data(),
            "obstacles": self._parse_obstacle_data(),
            "sceneries": self._parse_scenery_data(),
            "skins": self._parse_skin_data(),
            "capital_house_parts": self._parse_capital_part_data(),
            "helpers": self._parse_helper_data(),
            "war_leagues": self._parse_war_league_data(),
            "league_tiers": self._parse_league_tier_data(),
            "builder_leagues": self._parse_builder_league_data(),
            "capital_leagues": self._parse_capital_league_data(),
            "magic_items": self._parse_magic_items_data(),
            "magic_snacks": self._parse_magic_snacks_data(),
            "player_labels": self._parse_player_labels(),
            "clan_labels": self._parse_clan_labels(),
            "locales": self._parse_locale_mapping(),
            "chests": self._parse_chests_data(),
            "resources": self._parse_resource_data(),
            "achievements": self._parse_achievement_data(),
        }
        with open(f"{self.BASE_PATH}/static_data.json", "w", encoding="utf-8") as jf:
            jf.write(json.dumps(master_data, indent=2))

        if self.PRUNE_TRANSLATIONS:
            for key in list(self.translation_data.keys()):
                if key not in self.USED_TIDS:
                    del self.translation_data[key]

        with open(f"{self.BASE_PATH}/translations.json", "w", encoding="utf-8") as jf:
            jf.write(json.dumps(self.translation_data, indent=2))

        if not self.KEEP_JSON:
            for folder in ("csv", "logic", "localization"):
                root = Path(folder)
                if not root.exists():
                    continue
                for file_path in root.rglob("*.json"):
                    try:
                        file_path.unlink()
                    except OSError as e:
                        logging.warning(f"Could not delete {file_path}: {e}")

    def _check_local_source_files(self) -> list[str]:
        required_paths = [
            "localization/texts.json",
            "logic/buildings.json",
            "logic/characters.json",
            "logic/spells.json",
            "logic/heroes.json",
            "logic/resources.json",
        ]
        missing: list[str] = []
        for path in required_paths:
            if not Path(path).exists():
                missing.append(path)
        return missing

    def _bootstrap_local_json_from_csv(self) -> None:
        csv_root = Path("csv")
        if not csv_root.exists():
            return

        for csv_path in sorted(csv_root.rglob("*.csv")):
            try:
                data = csv_path.read_bytes()
                csv_path_str = csv_path.as_posix()
                if csv_path_str == "csv/animations.csv":
                    self.process_animations_csv(data=data, file_path=csv_path_str)
                else:
                    self.process_csv(data=data, file_path=csv_path_str)
            except Exception as exc:
                logging.warning("Failed to process local CSV file %s: %s", csv_path, exc)

    def _bootstrap_from_local_apk(self) -> bool:
        if not self.APK_PATH:
            return False
        apk_path = Path(self.APK_PATH)
        if not apk_path.exists():
            logging.warning("APK_PATH does not exist: %s", apk_path)
            return False

        processed = 0
        try:
            with zipfile.ZipFile(apk_path, "r") as apk_zip:
                for member in apk_zip.namelist():
                    if not member.lower().endswith(".csv"):
                        continue
                    if not member.startswith("assets/"):
                        continue

                    relative = member[len("assets/") :]
                    if not (
                        relative.startswith("logic/")
                        or relative.startswith("localization/")
                        or relative.startswith("csv/")
                    ):
                        continue

                    data = apk_zip.read(member)
                    if relative == "csv/animations.csv":
                        self.process_animations_csv(data=data, file_path=relative)
                    else:
                        self.process_csv(data=data, file_path=relative)
                    processed += 1
        except (OSError, zipfile.BadZipFile, KeyError) as exc:
            logging.warning("Could not process local APK at %s: %s", apk_path, exc)
            return False

        if processed == 0:
            logging.warning("No matching CSV files found inside %s", apk_path)
            return False

        logging.warning("Processed %s CSV files from local APK: %s", processed, apk_path)
        return True

    async def _bootstrap_from_downloaded_apk(self) -> bool:
        try:
            apk_bytes = await self._get_apk_bytes()
        except (RuntimeError, OSError, TypeError) as exc:
            logging.warning("Could not obtain APK bytes for local fallback: %s", exc)
            return False

        processed = 0
        try:
            with zipfile.ZipFile(io.BytesIO(apk_bytes), "r") as apk_zip:
                for member in apk_zip.namelist():
                    if not member.lower().endswith(".csv"):
                        continue
                    if not member.startswith("assets/"):
                        continue

                    relative = member[len("assets/") :]
                    if not (
                        relative.startswith("logic/")
                        or relative.startswith("localization/")
                        or relative.startswith("csv/")
                    ):
                        continue

                    data = apk_zip.read(member)
                    if relative == "csv/animations.csv":
                        self.process_animations_csv(data=data, file_path=relative)
                    else:
                        self.process_csv(data=data, file_path=relative)
                    processed += 1
        except (OSError, zipfile.BadZipFile, KeyError) as exc:
            logging.warning("Could not process downloaded APK payload: %s", exc)
            return False

        if processed == 0:
            logging.warning("Downloaded APK did not contain matching CSV files under assets/")
            return False

        logging.warning("Processed %s CSV files from downloaded APK fallback", processed)
        return True

    def generate_constants(self):
        static_data = self.open_file("static_data.json")

        troops = static_data["troops"]
        spells = static_data["spells"]
        heroes = static_data["heroes"]
        equipment = static_data["equipment"]
        pets = static_data["pets"]
        buildings = static_data["buildings"]
        achievements = static_data["achievements"]

        lists_to_write = {
            'ELIXIR_TROOP_ORDER': [
                t["name"]
                for t in troops
                if t["production_building"] == "Barracks" and not t.get("is_seasonal", False) and not "super_troop" in t
            ],
            'DARK_ELIXIR_TROOP_ORDER': [
                t["name"]
                for t in troops
                if t["production_building"] == "Dark Barracks"
                and not t.get("is_seasonal", False)
                and not "super_troop" in t
            ],
            'HV_TROOP_ORDER': 'ELIXIR_TROOP_ORDER + DARK_ELIXIR_TROOP_ORDER',
            'SIEGE_MACHINE_ORDER': [t["name"] for t in troops if t["production_building"] == "Workshop"],
            'SUPER_TROOP_ORDER': [t["name"] for t in troops if "super_troop" in t],
            'HOME_TROOP_ORDER': 'HV_TROOP_ORDER + SIEGE_MACHINE_ORDER',
            'SEASONAL_TROOP_ORDER': [t["name"] for t in troops if t.get("is_seasonal", False)],
            'BUILDER_TROOPS_ORDER': [t["name"] for t in troops if t["village"] == "builderBase"],
            'ELIXIR_SPELL_ORDER': [
                s["name"] for s in spells if s["upgrade_resource"] == "Elixir" and not s.get("is_seasonal", False)
            ],
            'DARK_ELIXIR_SPELL_ORDER': [s["name"] for s in spells if s["upgrade_resource"] == "Dark Elixir"],
            'SEASONAL_SPELL_ORDER': [s["name"] for s in spells if s.get("is_seasonal", False)],
            'SPELL_ORDER': 'ELIXIR_SPELL_ORDER + DARK_ELIXIR_SPELL_ORDER',
            'HOME_BASE_HERO_ORDER': [
                h["name"]
                for h in sorted(heroes, key=lambda x: x["levels"][0]["required_townhall"])
                if h["village"] == "home"
            ],
            'BUILDER_BASE_HERO_ORDER': [h["name"] for h in heroes if h["village"] == "builderBase"],
            'HERO_ORDER': 'HOME_BASE_HERO_ORDER + BUILDER_BASE_HERO_ORDER',
            'PETS_ORDER': [p["name"] for p in pets],
            'EQUIPMENT': [e["name"] for e in equipment],
            'HV_BUILDINGS': [b["name"] for b in buildings if b["village"] == "home"],
            'ACHIEVEMENT_ORDER': [
                a["name"]
                for a in sorted(
                    achievements,
                    key=lambda x: (
                        {'home': 0, 'builderBase': 1, 'clanCapital': 2}.get(x["village"], 0),
                        -x["ui_priority"],
                    ),
                )
            ],  # same order as in-game
        }
        constants_path = Path(__file__).parent.parent / "constants.py"

        with open(constants_path, 'w') as f:
            f.write('"""Auto-generated constants from static game data."""\n\n')
            for name, lst in lists_to_write.items():
                if isinstance(lst, str):
                    f.write(f"{name} = {lst}\n\n")
                else:
                    # Manual formatting: each item on its own line
                    f.write(f"{name} = [\n")
                    for item in lst:
                        f.write(f"    {repr(item)},\n")
                    f.write("]\n\n")

        print(f"Constants written to {constants_path}")

    async def download_files(self):
        if not self.FINGERPRINT:
            self.FINGERPRINT = await self._ensure_fingerprint()

        BASE_URL = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"

        try:
            fingerprint_file_raw = await download_file(url=f"{BASE_URL}/fingerprint.json", as_json=True)
        except RuntimeError as exc:
            message = str(exc)
            if "HTTP 403" in message and "AccessDenied" in message:
                logging.warning("Skipping remote static download: access denied for fingerprint.json")
                logging.warning("Continuing with local files to build static_data.json and translations.json")
                got_apk_csv = self._bootstrap_from_local_apk()
                if not got_apk_csv:
                    await self._bootstrap_from_downloaded_apk()
                self._bootstrap_local_json_from_csv()
                missing_files = self._check_local_source_files()
                if missing_files:
                    logging.warning("Local source files are missing: %s", ", ".join(missing_files))
                    logging.warning("Run once with remote access or place the required JSON files locally.")
                    return
                try:
                    self.create_master_json()
                except FileNotFoundError as file_error:
                    logging.warning(
                        "Local source files are missing, so static build was skipped: %s",
                        file_error,
                    )
                return
            raise
        if not isinstance(fingerprint_file_raw, dict):
            raise TypeError("fingerprint.json payload must be an object")
        fingerprint_file = cast(dict[str, Any], fingerprint_file_raw)

        for file_data in fingerprint_file.get("files", []):
            if not isinstance(file_data, dict):
                continue
            file_path: str = file_data["file"]
            if (
                not file_path.startswith("logic/")
                and not file_path.startswith("localization/")
                # and file_path != "csv/animations.csv"
                and not file_path.endswith("csv")
            ):
                continue

            download_url = f"{BASE_URL}/{file_path}"
            print(f"Downloading: {download_url}")
            data = await download_file(url=download_url, show_progress=True)

            Path(file_path).parent.mkdir(parents=True, exist_ok=True)
            with open(file_path, "wb") as f:
                f.write(data)

            print(f"Processing: {file_path}")
            if file_path == "csv/animations.csv":
                self.process_animations_csv(data=data, file_path=file_path)
            else:
                self.process_csv(data=data, file_path=file_path)

        self.create_master_json()
        await self.extract_assets()

    def run(self):
        asyncio.run(self.download_files())


if __name__ == "__main__":
    if os.name == "nt":
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
    StaticUpdater().run()
