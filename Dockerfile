FROM python:3.13.7-slim

LABEL org.opencontainers.image.source=https://github.com/ClashKingInc/ClashKingAssets
LABEL org.opencontainers.image.description="Image for the ClashKing Assets Service"
LABEL org.opencontainers.image.licenses=MIT

# Install uv and system dependencies
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/
RUN apt-get update && apt-get install -y --no-install-recommends \
    libsnappy-dev \
    git \
    curl \
    build-essential \
    gcc \
    python3-dev \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Set the working directory in the container
WORKDIR /app

# Copy pyproject.toml first for better caching
COPY pyproject.toml .

# Install dependencies using uv
RUN uv pip install --system . \
    && apt-get remove -y build-essential gcc python3-dev \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* /root/.cache/pip

# Copy only necessary application files (not assets)
COPY main.py .
COPY templates/ templates/

# Create cache directory for downloaded assets
RUN mkdir -p /app/cache

EXPOSE 6000

CMD ["uv", "run", "gunicorn", "-w", "2", "-k", "uvicorn.workers.UvicornWorker", "main:app", "--bind", "0.0.0.0:8000"]

