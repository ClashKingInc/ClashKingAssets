from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import StreamingResponse, FileResponse
from pathlib import Path
from starlette.middleware import Middleware
from starlette.middleware.cors import CORSMiddleware
from typing import Callable
from PIL import Image
import io
from fastapi.templating import Jinja2Templates
import json
import aiohttp

middleware = [
    Middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
]

app = FastAPI(middleware=middleware)

# Templates live in `templates/`
templates = Jinja2Templates(directory="templates")

BASE_DIR = Path(__file__).resolve().parents[0]
# Use /app/cache in container, local .cache directory otherwise
CACHE_DIR = Path("/app/cache") if Path("/app").exists() and Path("/app").is_dir() else BASE_DIR / ".cache"
CACHE_DIR.mkdir(parents=True, exist_ok=True)
ASSETS_DIR = CACHE_DIR
SUPPORTED_FORMATS = ["jpg", "jpeg", "png", "webp"]
GITHUB_RAW_BASE = "https://raw.githubusercontent.com/ClashKingInc/ClashKingAssets/main/assets"

async def download_from_github(file_path: str) -> bytes:
    url = f"{GITHUB_RAW_BASE}/{file_path}"
    timeout = aiohttp.ClientTimeout(total=30.0)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        async with session.get(url) as response:
            if response.status == 200:
                return await response.read()
            raise HTTPException(status_code=404, detail=f"File not found on GitHub: {file_path}")

async def get_cached_file(file_path: str) -> Path:
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

        # Normalize the target format
        target_format = target_format.lower()
        if target_format == 'jpg':
            target_format = 'jpeg'

        # For JPEG, handle transparency by flattening onto a white background
        if img.mode in ('RGBA', 'LA', 'P') and target_format == 'jpeg':
            # Create a white background image
            background = Image.new('RGB', img.size, (255, 255, 255))
            # Convert image to RGBA if it's not already
            img = img.convert('RGBA')
            # Paste the image onto the white background, using the alpha channel as a mask
            background.paste(img, mask=img.split()[3])  # 3 is the alpha channel
            img = background

        # Convert any remaining modes to RGB for JPEG
        elif target_format == 'jpeg':
            img = img.convert('RGB')

        # Convert 1 mode to L (grayscale) for PNG/WebP
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

# Load metadata from GitHub on startup
images = {}
translations = {}

@app.on_event("startup")
async def load_metadata():
    global images, translations
    
    # Load image_map.json
    image_map_path = await get_cached_file("image_map.json")
    with open(image_map_path, "r", encoding="utf-8") as f:
        images = json.load(f)
    
    # Load translations.json
    translations_path = await get_cached_file("translations.json")
    with open(translations_path, "r", encoding="utf-8") as f:
        translations = json.load(f)

@app.get("/")
async def gallery(request: Request):
    return templates.TemplateResponse(
        "gallery.html", {
            "request": request,
            "sections": sorted(images.keys()),
            "images": images,
            "translations": translations,
        }
    )

@app.get("/{file_path:path}", name="Get a file")
async def serve_file(file_path: str):
    # Prevent path traversal
    if ".." in file_path or file_path.startswith("/"):
        raise HTTPException(status_code=403, detail="Access forbidden")

    try:
        # Try to get the file from cache or download it
        cached_file = await get_cached_file(file_path)
        return FileResponse(cached_file)
    except HTTPException:
        # File doesn't exist in the exact format, try alternative formats
        requested_path = Path(file_path)
        alternative_file = await find_alternative_format(requested_path)
        
        if alternative_file:
            # Convert to the requested format
            converted_image = convert_image(alternative_file, requested_path.suffix.lstrip('.'))
            return StreamingResponse(converted_image, media_type=f"image/{requested_path.suffix.lstrip('.')}")
        
        raise HTTPException(status_code=404, detail="File not found")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(
        "main:app",    # <— module path, not the object
        host="0.0.0.0",
        port=80,
        reload=True,          # enable code-watching
        workers=1             # you can bump this >1 if you like
    )
