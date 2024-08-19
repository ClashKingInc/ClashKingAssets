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
    response.headers["Cache-Control"] = "public, max-age=7200"
    return response

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

    uvicorn.run(app, host="0.0.0.0", port=80)
