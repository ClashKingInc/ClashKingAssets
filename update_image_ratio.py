import os, glob, re
from PIL import Image
import json

FIND_NAME   = "Pirate Flag"
NEW_NAME = FIND_NAME.lower().replace(" ", "-").replace(".", "")

type = "decorations"
INPUT_DIR  = f"assets/home-base/{type}/{NEW_NAME}"

OUTPUT_DIR = INPUT_DIR
SAY_ICON = False
DELETE = True

os.makedirs(OUTPUT_DIR, exist_ok=True)

def natural_key(filename):
    # splits “abc123def” → ["abc", "123", "def"], then converts digit parts to int
    parts = re.split(r'(\d+)', filename)
    return [int(p) if p.isdigit() else p.lower() for p in parts]

# grab all PNGs and sort by natural_key of their basename
paths = sorted(
    glob.glob(os.path.join(INPUT_DIR, "*.png")),
    key=lambda p: natural_key(os.path.basename(p))
)

count = 0
dicto = {}
if type == "skins":
    dicto = {
        "poses": {}
    }

for path in paths:
    img   = Image.open(path).convert("RGBA")
    alpha = img.split()[-1]
    bbox  = alpha.getbbox()
    if not bbox:
        continue

    # crop → square → save
    cropped = img.crop(bbox)
    w, h    = cropped.size
    size    = max(w, h)
    canvas  = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    canvas.paste(cropped, ((size - w)//2, (size - h)//2))
    out_name = f"{NEW_NAME}-{count}.png"

    SPOT_INPUT_DIR = INPUT_DIR.replace("/assets", "")
    if count == 0:
        if SAY_ICON:
            out_name = f"{NEW_NAME}-icon.png"
        else:
            out_name = f"{NEW_NAME}.png"

        if type == "decorations":
            SPOT_INPUT_DIR = SPOT_INPUT_DIR.replace(f"/{NEW_NAME}", "")

        SPOT_INPUT_DIR = f"/{SPOT_INPUT_DIR}/{out_name}".replace("/assets", "")
        dicto["icon"] = SPOT_INPUT_DIR
    else:
        out_name = f"{NEW_NAME}-pose-{count}.png"
        SPOT_INPUT_DIR = f"/{INPUT_DIR}/{out_name}"
        dicto["poses"][str(count)] = SPOT_INPUT_DIR

    canvas.save(os.path.join(f"assets{SPOT_INPUT_DIR}"))
    count += 1
    print(f"Saved {out_name}")

    # remove original
    if DELETE:
        try:
            os.remove(path)
        except OSError as e:
            print(f"Could not delete {path}: {e}")


with open("assets/image_map.json", 'r', encoding='utf-8') as f:
    full_data = json.load(f)

type_data = full_data.get(type, {})

entry_id = ""
for key, data in type_data.items():
    if data.get("name") == FIND_NAME:
        entry_id = key
        break

dicto["name"] = FIND_NAME

print(dicto)
full_data[type][entry_id] = dicto

with open("assets/image_map.json", "w", encoding="utf-8") as jf:
    jf.write(json.dumps(full_data, indent=2))