from fastapi import FastAPI, HTTPException, Request, UploadFile, File, Form
from fastapi.responses import StreamingResponse, FileResponse, HTMLResponse, JSONResponse
from pathlib import Path
from starlette.middleware import Middleware
from starlette.middleware.cors import CORSMiddleware
from typing import Callable, AsyncGenerator
from PIL import Image
import io
from fastapi.templating import Jinja2Templates
import json
import aiohttp
import contextlib
import logging
import random

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("assets")

middleware = [
    Middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
]

images = {}
translations = {}

BASE_DIR = Path(__file__).resolve().parents[0]
ASSETS_DIR = BASE_DIR / "assets"
ASSETS_DIR.mkdir(exist_ok=True)
CACHE_DIR = BASE_DIR / ".cache"
CACHE_DIR.mkdir(parents=True, exist_ok=True)
SUPPORTED_FORMATS = ["jpg", "jpeg", "png", "webp"]
GITHUB_RAW_BASE = "https://raw.githubusercontent.com/killshotttttt/ClashAssets/main/assets"

@contextlib.asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    global images, translations
    
    # Load image_map.json
    try:
        image_map_path = await get_cached_file("image_map.json")
        with open(image_map_path, "r", encoding="utf-8") as f:
            images = json.load(f)
        
        # Load translations.json
        translations_path = await get_cached_file("translations.json")
        with open(translations_path, "r", encoding="utf-8") as f:
            translations = json.load(f)
            
        count = sum(len(v) for v in images.values() if isinstance(v, dict))
        logger.info(f"Loaded {count} image entries from {image_map_path}")
    except Exception as e:
        logger.error(f"Failed to load metadata: {e}")

    yield

app = FastAPI(middleware=middleware, lifespan=lifespan)

# Templates live in `templates/`
templates = Jinja2Templates(directory="templates")

async def download_from_github(file_path: str) -> bytes:
    url = f"{GITHUB_RAW_BASE}/{file_path}"
    async with aiohttp.ClientSession() as session:
        async with session.get(url) as response:
            if response.status == 200:
                return await response.read()
            raise HTTPException(status_code=404, detail=f"File not found on GitHub: {file_path}")

async def get_cached_file(file_path: str) -> Path:
    # Check local assets first (strip leading slash to prevent Path joining issues)
    local_path = file_path.lstrip("/")
    local_asset = BASE_DIR / "assets" / local_path
    if local_asset.is_file():
        return local_asset

    cached_file = CACHE_DIR / file_path
    if cached_file.is_file():
        return cached_file
    
    # Download from GitHub and cache
    content = await download_from_github(file_path)
    cached_file.parent.mkdir(parents=True, exist_ok=True)
    cached_file.write_bytes(content)
    return cached_file

async def find_alternative_format(relative_path: Path):
    for ext in SUPPORTED_FORMATS:
        potential_path = f"{relative_path.with_suffix('.' + ext)}"
        try:
            cached_file = await get_cached_file(potential_path)
            return cached_file
        except:
            continue
    return None

def convert_image(image_path: Path, target_format: str) -> io.BytesIO:
    with Image.open(image_path) as img:
        img_io = io.BytesIO()
        target_format = target_format.lower()
        if target_format == 'jpg': target_format = 'jpeg'

        if img.mode in ('RGBA', 'LA', 'P') and target_format == 'jpeg':
            background = Image.new('RGB', img.size, (255, 255, 255))
            img = img.convert('RGBA')
            background.paste(img, mask=img.split()[3])
            img = background
        elif target_format == 'jpeg':
            img = img.convert('RGB')
        elif img.mode == '1' and target_format in ('png', 'webp'):
            img = img.convert('L')

        img.save(img_io, format=target_format.upper())
        img_io.seek(0)
        return img_io

@app.middleware("http")
async def add_cache_control_header(request: Request, call_next: Callable):
    response = await call_next(request)
    response.headers["Cache-Control"] = "public, max-age=2592000"
    return response

# ----------- Asset Lab Methods ------------

def load_image_map():
    map_path = ASSETS_DIR / "image_map.json"
    if not map_path.exists():
        return {}
    with open(map_path, "r", encoding="utf-8") as f:
        return json.load(f)

def save_image_map(data):
    map_path = ASSETS_DIR / "image_map.json"
    with open(map_path, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2)

def process_and_save_image(image_bytes: bytes, asset_type: str, asset_name: str, slug: str, level: str = None, key: str = None):
    # PIL Processing
    img = Image.open(io.BytesIO(image_bytes)).convert("RGBA")
    alpha = img.split()[-1]
    bbox = alpha.getbbox()
    if not bbox:
        raise ValueError("Empty image")

    cropped = img.crop(bbox)
    w, h = cropped.size
    size = max(w, h)
    
    # Make square
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    canvas.paste(cropped, ((size - w)//2, (size - h)//2))

    # Determine paths
    image_map = load_image_map()
    
    # Try to find existing folder from current entries
    existing_folder = None
    type_data = image_map.get(asset_type, {})
    entry_id = None
    for eid, info in type_data.items():
        if info.get("name") == asset_name:
            entry_id = eid
            sample_path = None
            if "levels" in info and info["levels"]:
                sample_path = list(info["levels"].values())[0]
            elif "icon" in info:
                sample_path = info["icon"]
            elif "poses" in info and info["poses"]:
                sample_path = list(info["poses"].values())[0]
            
            if sample_path:
                existing_folder = "/".join(sample_path.lstrip("/").split("/")[:-1])
            break

    if existing_folder:
        rel_dir = existing_folder
    else:
        # Standardize folder name if creating new
        base_type = "home-base"
        if asset_type in ["builder-base", "capital-base"]:
            base_type = asset_type
        folder_slug = asset_name.lower().replace(" ", "-").replace(".", "")
        rel_dir = f"{base_type}/{asset_type}/{folder_slug}"
    
    out_filename = f"{slug}.png"
    rel_path = f"/{rel_dir}/{out_filename}"
    
    abs_path = ASSETS_DIR / rel_dir
    abs_path.mkdir(parents=True, exist_ok=True)
    canvas.save(abs_path / out_filename)
    
    # Update Map
    if not entry_id:
        entry_id = str(random.randint(2000000, 2999999))
        type_data[entry_id] = {"name": asset_name}
        image_map[asset_type] = type_data
    
    entry = type_data[entry_id]
    if level:
        if "levels" not in entry:
            entry["levels"] = {}
        entry["levels"][str(level)] = rel_path
    elif key:
        if key.startswith("poses."):
            pose_num = key.split(".")[1]
            if "poses" not in entry: entry["poses"] = {}
            entry["poses"][pose_num] = rel_path
        else:
            entry[key] = rel_path
    else:
        entry["icon"] = rel_path

    save_image_map(image_map)
    return rel_path

# ----------- Routes ------------

@app.get("/")
async def gallery(request: Request):
    global images, translations
    
    # Reload from local disk to catch new uploads
    try:
        image_map_path = ASSETS_DIR / "image_map.json"
        if image_map_path.exists():
            with open(image_map_path, "r", encoding="utf-8") as f:
                images = json.load(f)
        
        trans_path = ASSETS_DIR / "translations.json"
        if trans_path.exists():
            with open(trans_path, "r", encoding="utf-8") as f:
                translations = json.load(f)
    except Exception as e:
        logger.error(f"Failed to reload data for gallery: {e}")

    return templates.TemplateResponse(
        "gallery.html", {
            "request": request,
            "sections": sorted(images.keys()),
            "images": images,
            "translations": translations,
        }
    )

@app.get("/lab", response_class=HTMLResponse)
async def lab(request: Request):
    image_map = load_image_map()
    static_data = {}
    static_path = ASSETS_DIR / "static_data.json"
    if static_path.exists():
        with open(static_path, "r", encoding="utf-8") as f:
            static_data = json.load(f)
            
    # Group by village based on static data
    villages = {"home": {}, "builder": {}, "capital": {}}
    for section, items in image_map.items():
        for eid, meta in items.items():
            village_key = "home" # Default
            asset_name = meta.get("name")
            
            # Find the item in ANY section of static_data
            found_village = None
            for s_key, s_items in static_data.items():
                if isinstance(s_items, list):
                    # Try matching by eid first (since eid in image_map corresponds to _id in static_data)
                    static_item = next((i for i in s_items if str(i.get("_id")) == str(eid)), None)
                    # Fallback to name match
                    if not static_item:
                        static_item = next((i for i in s_items if i.get("name") == asset_name), None)
                    
                    if static_item:
                        found_village = static_item.get("village")
                        break
            
            if found_village == "builderBase":
                village_key = "builder"
            elif found_village == "clanCapital":
                village_key = "capital"
            else:
                village_key = "home"
            
            if village_key not in villages: villages[village_key] = {}
            if section not in villages[village_key]: villages[village_key][section] = {}
            villages[village_key][section][eid] = meta

    return templates.TemplateResponse("upload.html", {
        "request": request, 
        "villages": villages,
        "static_data": static_data
    })

@app.get("/lab_data")
async def lab_data():
    image_map = load_image_map()
    static_data = {}
    static_path = ASSETS_DIR / "static_data.json"
    if static_path.exists():
        with open(static_path, "r", encoding="utf-8") as f:
            static_data = json.load(f)
            
    # Group by village based on static data
    villages = {"home": {}, "builder": {}, "capital": {}}
    for section, items in image_map.items():
        for eid, meta in items.items():
            village_key = "home"
            asset_name = meta.get("name")
            
            found_village = None
            for s_key, s_items in static_data.items():
                if isinstance(s_items, list):
                    static_item = next((i for i in s_items if str(i.get("_id")) == str(eid)), None)
                    if not static_item:
                        static_item = next((i for i in s_items if i.get("name") == asset_name), None)
                    if static_item:
                        found_village = static_item.get("village")
                        break
            
            if found_village == "builderBase":
                village_key = "builder"
            elif found_village == "clanCapital":
                village_key = "capital"
            else:
                village_key = "home"
            
            if village_key not in villages: villages[village_key] = {}
            if section not in villages[village_key]: villages[village_key][section] = {}
            villages[village_key][section][eid] = meta

    return {"villages": villages}

@app.post("/upload")
async def handle_upload(
    file: UploadFile = File(...),
    asset_type: str = Form(...),
    asset_name: str = Form(...),
    slug: str = Form(...),
    level: str = Form(None),
    key: str = Form(None)
):
    try:
        content = await file.read()
        rel_path = process_and_save_image(content, asset_type, asset_name, slug, level, key)
        return JSONResponse({"status": "success", "path": rel_path})
    except Exception as e:
        logger.error(f"Upload failed: {e}")
        return JSONResponse({"status": "error", "message": str(e)}, status_code=400)

@app.post("/delete-asset")
async def delete_asset(request: Request):
    data = await request.json()
    asset_type = data.get("asset_type")
    asset_name = data.get("asset_name")
    level = data.get("level")
    key = data.get("key")
    
    image_map = load_image_map()
    if asset_type in image_map:
        for eid, info in image_map[asset_type].items():
            if info.get("name") == asset_name:
                changed = False
                if level is not None and "levels" in info and str(level) in info["levels"]:
                    del info["levels"][str(level)]
                    changed = True
                elif key is not None:
                    if key.startswith("poses."):
                        p_num = key.split(".")[1]
                        if "poses" in info and p_num in info["poses"]:
                            del info["poses"][p_num]
                            changed = True
                    elif key in info:
                        del info[key]
                        changed = True
                
                if changed:
                    save_image_map(image_map)
                    return JSONResponse({"status": "success"})
    return JSONResponse({"status": "error", "message": "Not found"}, status_code=404)


@app.get("/{file_path:path}", name="Get a file")
async def serve_file(file_path: str):
    file_path = file_path.lstrip("/")
    if ".." in file_path:
        raise HTTPException(status_code=403, detail="Access forbidden")

    try:
        cached_file = await get_cached_file(file_path)
        return FileResponse(cached_file)
    except HTTPException:
        requested_path = Path(file_path)
        alternative_file = await find_alternative_format(requested_path)
        if alternative_file:
            converted_image = convert_image(alternative_file, requested_path.suffix.lstrip('.'))
            return StreamingResponse(converted_image, media_type=f"image/{requested_path.suffix.lstrip('.')}")
        raise HTTPException(status_code=404, detail="File not found")

if __name__ == "__main__":
    import uvicorn
    # Disable reload because on Windows it can crash when image_map.json is updated.
    # We already have manual reloading logic in the gallery route.
    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=False, workers=1)
