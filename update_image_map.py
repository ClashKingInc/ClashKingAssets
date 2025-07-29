import json

DATA_TYPES_FULL = ["troops", "heroes", "sceneries", "skins", "pets"]
INCLUDE_LEVELS = ["buildings", "traps"]
IGNORED_TYPES = ["supercharges"]
MODULES = ["seasonal_defenses"]


def add_new_entries():
    with open(f"assets/static_data.json", "r", encoding="utf-8") as f:
        static_data = json.load(f)

    with open(f"assets/image_map.json", "r", encoding="utf-8") as f:
        image_map = json.load(f)

    new_image_map = {}
    for data_type, data_list in static_data.items():
        if data_type in IGNORED_TYPES:
            continue
        for data in data_list:
            name = data["name"]

            image_data_type = image_map.get(data_type, {})
            if data_type not in new_image_map:
                new_image_map[data_type] = {}
            old_data = image_data_type.get(str(data["_id"]), {})

            data_hold = {}
            data_hold["name"] = name
            if old_data.get("icon") is None:
                data_hold["icon"] = ""
            else:
                data_hold["icon"] = old_data.get("icon")

            if data_type in DATA_TYPES_FULL:
                if old_data.get("full") is None:
                    data_hold["full"] = ""
                else:
                    data_hold["full"] = old_data.get("full")

            if data_type in INCLUDE_LEVELS:
                del data_hold["icon"]
                data_hold["levels"] = {}
                for item in data.get("levels", []):
                    old_level_data = old_data.get("levels", {}).get(str(item.get("level")))
                    if old_level_data is None:
                        data_hold["levels"][str(item.get("level"))] = ""
                    else:
                        data_hold["levels"][str(item.get("level"))] = old_level_data

            if data_type in MODULES:
                for tier in ["tier_1", "tier_2", "tier_3"]:
                    old_tier_data = old_data.get(tier)
                    if old_tier_data is None:
                        data_hold[tier] = ""
                    else:
                        data_hold[tier] = old_tier_data

            new_image_map[data_type][data["_id"]] = data_hold


    with open(f"assets/image_map.json", "w", encoding="utf-8") as jf:
        jf.write(json.dumps(new_image_map, indent=2))

add_new_entries()