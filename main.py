from fastapi import FastAPI, HTTPException, Request, Response
from fastapi.responses import StreamingResponse, FileResponse
from pathlib import Path
from starlette.middleware import Middleware
from starlette.middleware.cors import CORSMiddleware
from typing import Callable
from PIL import Image
import io
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
import json
import zipfile
import os

middleware = [
    Middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
]

app = FastAPI(middleware=middleware)

# Mount `assets/` under `/static`
app.mount("/static", StaticFiles(directory="assets"), name="static")
# Templates live in `templates/`
templates = Jinja2Templates(directory="templates")

BASE_DIR = Path(__file__).resolve().parents[0]
ASSETS_DIR = BASE_DIR / "assets"
SUPPORTED_FORMATS = ["jpg", "jpeg", "png", "webp"]

def find_alternative_format(relative_path: Path):
    for ext in SUPPORTED_FORMATS:
        potential_file = ASSETS_DIR / f"{relative_path.with_suffix('.' + ext)}"
        if potential_file.is_file():
            return potential_file
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

# Load your metadata once
with open("assets/image_map.json", "r", encoding="utf-8") as f:
    images = json.load(f)

with open("assets/translations.json", encoding="utf-8") as f:
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

@app.get("/download/{section}/{item_id}/zip")
async def download_item_zip(section: str, item_id: str):
    sec = images.get(section)
    if not sec or item_id not in sec:
        raise HTTPException(404, "Item not found")
    meta = sec[item_id]
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, mode="w") as zf:
        for key in ("full", "icon"):  # individual images
            path = meta.get(key)
            if path:
                src = os.path.join("assets", path.lstrip("/"))
                if os.path.isfile(src): zf.write(src, arcname=os.path.basename(src))
        for lvl in (meta.get("levels") or {}).values():
            if lvl:
                src = os.path.join("assets", lvl.lstrip("/"))
                if os.path.isfile(src): zf.write(src, arcname=os.path.basename(src))
        if section == "sceneries" and meta.get("music"):
            src = os.path.join("assets", meta["music"].lstrip("/"))
            if os.path.isfile(src): zf.write(src, arcname=os.path.basename(src))
    buf.seek(0)
    return StreamingResponse(
        buf,
        media_type="application/x-zip-compressed",
        headers={"Content-Disposition": f"attachment; filename={item_id}.zip"}
    )


@app.get("/{file_path:path}", name="Get a file")
async def serve_file(file_path: str):
    requested_path = (ASSETS_DIR / file_path).resolve()

    print(requested_path)
    # Ensure the requested file is within the assets directory
    if not str(requested_path).startswith(str(ASSETS_DIR)):
        raise HTTPException(status_code=403, detail="Access forbidden")

    relative_path = requested_path.relative_to(ASSETS_DIR)

    # If the requested file exists, serve it
    if requested_path.is_file():
        return FileResponse(requested_path)

    # Check if the file exists in another format
    alternative_file = find_alternative_format(relative_path)
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
