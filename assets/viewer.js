const MANIFEST_URL = "https://assets.clashk.ing/manifest.json";
const CACHE_KEY = "clashking-asset-viewer-manifest-v1";
const CACHE_TTL_MS = 30 * 60 * 1000;
const IMAGE_EXTENSIONS = new Set(["gif", "jpeg", "jpg", "png", "svg", "webp"]);

const categoryList = document.getElementById("categoryList");
const mobileCategories = document.getElementById("mobileCategories");
const gridShell = document.getElementById("gridShell");
const spacer = document.getElementById("spacer");
const searchInput = document.getElementById("searchInput");
const summary = document.getElementById("summary");
const empty = document.getElementById("empty");
const toast = document.getElementById("toast");

let allAssets = [];
let categories = [{ id: "all", label: "all", count: 0 }];

const state = {
  category: "all",
  query: "",
  items: [],
  tileWidth: 150,
  rowHeight: 184,
  gap: 12,
  columns: 1,
  lastRangeKey: "",
};

function labelForCategory(category) {
  return category.replaceAll("-", " ").replaceAll("_", " ");
}

function assetFromManifestEntry(entry, index) {
  if (!entry || typeof entry.path !== "string" || entry.path.startsWith("bot/")) {
    return null;
  }

  const extension = entry.extension?.toLowerCase();
  if (
    !extension
    || !IMAGE_EXTENSIONS.has(extension)
    || typeof entry.category !== "string"
    || typeof entry.display_name !== "string"
    || typeof entry.url !== "string"
  ) {
    return null;
  }

  return {
    id: index,
    path: entry.path,
    category: entry.category,
    name: entry.display_name,
    ext: extension,
    url: entry.url,
    haystack: `${entry.path} ${entry.display_name}`.toLowerCase(),
  };
}

function readCachedAssets() {
  try {
    const cached = JSON.parse(localStorage.getItem(CACHE_KEY) || "null");
    if (!cached || !Array.isArray(cached.assets) || Date.now() - cached.saved_at > CACHE_TTL_MS) {
      return null;
    }
    if (!cached.assets.some((asset) => asset.path?.startsWith("obstacles/"))) {
      return null;
    }
    return cached.assets;
  } catch {
    return null;
  }
}

function writeCachedAssets(assets) {
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify({ saved_at: Date.now(), assets }));
  } catch {
    // The viewer can still work without cache.
  }
}

async function fetchAssets() {
  const cached = readCachedAssets();
  if (cached) {
    return cached;
  }

  const response = await fetch(MANIFEST_URL);
  if (!response.ok) {
    throw new Error(`Asset manifest request failed: ${response.status}`);
  }

  const data = await response.json();
  const assets = (data.assets || [])
    .map(assetFromManifestEntry)
    .filter(Boolean)
    .sort((a, b) => a.path.localeCompare(b.path));
  writeCachedAssets(assets);
  return assets;
}

function setAssets(assets) {
  allAssets = assets.map((asset, index) => ({ ...asset, id: index }));

  const categoryCounts = allAssets.reduce((counts, asset) => {
    counts.set(asset.category, (counts.get(asset.category) || 0) + 1);
    return counts;
  }, new Map());

  categories = [
    { id: "all", label: "all", count: allAssets.length },
    ...Array.from(categoryCounts, ([id, count]) => ({ id, label: id, count }))
      .sort((a, b) => a.label.localeCompare(b.label)),
  ];

  state.items = allAssets;
  renderCategoryButtons();
  applyFilters();
}

function renderCategoryButtons() {
  categoryList.textContent = "";
  mobileCategories.textContent = "";

  for (const category of categories) {
    const button = document.createElement("button");
    button.className = "category-button";
    button.type = "button";
    button.dataset.category = category.id;
    button.setAttribute("aria-current", category.id === state.category ? "true" : "false");

    const name = document.createElement("span");
    name.className = "category-name";
    name.textContent = labelForCategory(category.label);
    const count = document.createElement("span");
    count.className = "category-count";
    count.textContent = category.count;
    button.append(name, count);
    button.addEventListener("click", () => setCategory(category.id));
    categoryList.appendChild(button);

    const chip = document.createElement("button");
    chip.className = "chip";
    chip.type = "button";
    chip.dataset.category = category.id;
    chip.setAttribute("aria-current", category.id === state.category ? "true" : "false");
    chip.textContent = `${labelForCategory(category.label)} ${category.count}`;
    chip.addEventListener("click", () => setCategory(category.id));
    mobileCategories.appendChild(chip);
  }
}

function setCategory(category) {
  if (state.category === category) return;
  state.category = category;
  updateAriaCurrent();
  applyFilters();
}

function updateAriaCurrent() {
  document.querySelectorAll("[data-category]").forEach((button) => {
    button.setAttribute("aria-current", button.dataset.category === state.category ? "true" : "false");
  });
}

function applyFilters() {
  const query = state.query.trim().toLowerCase();
  state.items = allAssets.filter((asset) => {
    const inCategory = state.category === "all" || asset.category === state.category;
    return inCategory && (!query || asset.haystack.includes(query));
  });
  state.lastRangeKey = "";
  gridShell.scrollTop = 0;
  updateLayout();
  renderVisible();
}

function updateLayout() {
  const shellStyles = getComputedStyle(gridShell);
  const horizontalPadding = parseFloat(shellStyles.paddingLeft) + parseFloat(shellStyles.paddingRight);
  const width = Math.max(0, gridShell.clientWidth - horizontalPadding - 2);
  const compact = width < 620;
  const minWidth = compact ? 118 : 146;
  const maxWidth = compact ? 154 : 204;
  state.gap = compact ? 10 : 12;
  state.columns = Math.max(1, Math.floor((width + state.gap) / (minWidth + state.gap)));
  state.tileWidth = Math.min(maxWidth, Math.floor((width - state.gap * (state.columns - 1)) / state.columns));
  state.rowHeight = Math.round(state.tileWidth + 58);

  const rows = Math.ceil(state.items.length / state.columns);
  spacer.style.height = `${Math.max(0, rows * state.rowHeight)}px`;
  empty.style.display = state.items.length ? "none" : "grid";
  summary.textContent = `${state.items.length.toLocaleString()} images in ${labelForCategory(state.category)}`;
}

function renderVisible() {
  updateLayout();
  const scrollTop = gridShell.scrollTop;
  const viewportHeight = gridShell.clientHeight;
  const overscanRows = 4;
  const startRow = Math.max(0, Math.floor(scrollTop / state.rowHeight) - overscanRows);
  const endRow = Math.min(
    Math.ceil(state.items.length / state.columns),
    Math.ceil((scrollTop + viewportHeight) / state.rowHeight) + overscanRows,
  );
  const startIndex = startRow * state.columns;
  const endIndex = Math.min(state.items.length, endRow * state.columns);
  const rangeKey = `${startIndex}:${endIndex}:${state.columns}:${state.tileWidth}:${state.rowHeight}`;
  if (rangeKey === state.lastRangeKey) return;
  state.lastRangeKey = rangeKey;

  const fragment = document.createDocumentFragment();
  for (let index = startIndex; index < endIndex; index += 1) {
    const asset = state.items[index];
    if (!asset) continue;
    const row = Math.floor(index / state.columns);
    const column = index % state.columns;
    const left = column * (state.tileWidth + state.gap);
    const top = row * state.rowHeight;

    const tile = document.createElement("article");
    tile.className = "tile";
    tile.style.width = `${state.tileWidth}px`;
    tile.style.height = `${state.rowHeight - state.gap}px`;
    tile.style.transform = `translate3d(${left}px, ${top}px, 0)`;
    tile.title = asset.url;
    tile.tabIndex = 0;
    tile.addEventListener("click", () => copyUrl(asset.url));
    tile.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        copyUrl(asset.url);
      }
    });

    const thumb = document.createElement("div");
    thumb.className = "thumb";
    const img = document.createElement("img");
    img.src = asset.url;
    img.alt = asset.path;
    img.loading = "lazy";
    img.decoding = "async";
    thumb.appendChild(img);

    const path = document.createElement("div");
    path.className = "path";
    path.textContent = asset.path;

    tile.append(thumb, path);
    fragment.appendChild(tile);
  }

  spacer.replaceChildren(fragment);
}

async function copyUrl(url) {
  try {
    await navigator.clipboard.writeText(url);
    showToast("Copied URL");
  } catch {
    window.open(url, "_blank", "noopener");
  }
}

let toastTimer = 0;
function showToast(message) {
  toast.textContent = message;
  toast.classList.add("visible");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove("visible"), 1200);
}

let scrollFrame = 0;
gridShell.addEventListener("scroll", () => {
  if (scrollFrame) return;
  scrollFrame = requestAnimationFrame(() => {
    scrollFrame = 0;
    renderVisible();
  });
}, { passive: true });

let resizeFrame = 0;
window.addEventListener("resize", () => {
  cancelAnimationFrame(resizeFrame);
  resizeFrame = requestAnimationFrame(() => {
    state.lastRangeKey = "";
    renderVisible();
  });
});

searchInput.addEventListener("input", () => {
  state.query = searchInput.value;
  applyFilters();
});

async function initialize() {
  summary.textContent = "Loading assets...";
  updateLayout();

  try {
    setAssets(await fetchAssets());
  } catch (error) {
    console.error(error);
    summary.textContent = "Could not load asset manifest";
    empty.textContent = "Could not load assets.";
    empty.style.display = "grid";
  }
}

initialize();
