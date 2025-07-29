import os, glob, re
from PIL import Image

NEW_NAME   = "air-bombs"
INPUT_DIR  = f"assets/builder-base/buildings/{NEW_NAME}"
OUTPUT_DIR = INPUT_DIR
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

count = 1
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
    canvas.save(os.path.join(OUTPUT_DIR, out_name))
    count += 1
    print(f"Saved {out_name}")

    # remove original
    if DELETE:
        try:
            os.remove(path)
        except OSError as e:
            print(f"Could not delete {path}: {e}")