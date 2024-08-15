from fastapi import FastAPI, HTTPException, Request, Response
from fastapi.responses import StreamingResponse, FileResponse
from pathlib import Path
from starlette.middleware import Middleware
from starlette.middleware.cors import CORSMiddleware
from typing import Callable
from PIL import Image
import io

middleware = [
    Middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
]

app = FastAPI(middleware=middleware)

BASE_DIR = Path(__file__).resolve().parents[0]
ASSETS_DIR = BASE_DIR / "assets"
SUPPORTED_FORMATS = ["jpg", "jpeg", "png", "webp"]


def find_alternative_format(file_stem: str):
    for ext in SUPPORTED_FORMATS:
        potential_file = ASSETS_DIR / f"{file_stem}.{ext}"
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

        # Convert RGBA to RGB if saving as JPEG
        if img.mode == 'RGBA' and target_format == 'jpeg':
            img = img.convert('RGB')

        img.save(img_io, format=target_format.upper())
        img_io.seek(0)
        return img_io


@app.middleware("http")
async def add_cache_control_header(request: Request, call_next: Callable):
    response = await call_next(request)
    response.headers["Cache-Control"] = "public, max-age=2592000"
    return response

@app.get("/{file_path:path}", name="Get a file")
async def serve_file(file_path: str):
    requested_path = (ASSETS_DIR / file_path).resolve()

    print(requested_path)
    # Ensure the requested file is within the assets directory
    if not str(requested_path).startswith(str(ASSETS_DIR)):
        raise HTTPException(status_code=403, detail="Access forbidden")

    # Extract the file name and extension
    file_stem = requested_path.stem
    file_extension = requested_path.suffix.lower().lstrip('.')

    # If the requested file exists, serve it
    if requested_path.is_file():
        return FileResponse(requested_path)

    # Check if the file exists in another format
    if file_extension in SUPPORTED_FORMATS:
        alternative_file = find_alternative_format(file_stem)
        if alternative_file:
            # Convert to the requested format
            converted_image = convert_image(alternative_file, file_extension)
            return StreamingResponse(converted_image, media_type=f"image/{file_extension}")

    raise HTTPException(status_code=404, detail="File not found")
