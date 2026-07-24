const MANIFEST_URL = "https://assets.clashk.ing/manifest.json";
const CACHE_KEY = "clashking-asset-viewer-manifest-v1";
const PREFERENCES_KEY = "clashking-asset-viewer-preferences-v1";
const CACHE_TTL_MS = 30 * 60 * 1000;
const IMAGE_EXTENSIONS = new Set(["gif", "jpeg", "jpg", "png", "svg", "webp"]);

const els = {
  app: document.getElementById("app"),
  categoryList: document.getElementById("categoryList"),
  mobileCategories: document.getElementById("mobileCategories"),
  gridShell: document.getElementById("gridShell"),
  spacer: document.getElementById("spacer"),
  searchInput: document.getElementById("searchInput"),
  sortSelect: document.getElementById("sortSelect"),
  gridView: document.getElementById("gridView"),
  listView: document.getElementById("listView"),
  summary: document.getElementById("summary"),
  empty: document.getElementById("empty"),
  emptyTitle: document.getElementById("emptyTitle"),
  emptyBody: document.getElementById("emptyBody"),
  emptyAction: document.getElementById("emptyAction"),
  detailsPanel: document.getElementById("detailsPanel"),
  detailsScrim: document.getElementById("detailsScrim"),
  closeDetails: document.getElementById("closeDetails"),
  detailName: document.getElementById("detailName"),
  detailImage: document.getElementById("detailImage"),
  detailFormat: document.getElementById("detailFormat"),
  detailCategory: document.getElementById("detailCategory"),
  detailDimensions: document.getElementById("detailDimensions"),
  detailPath: document.getElementById("detailPath"),
  downloadAsset: document.getElementById("downloadAsset"),
  openAsset: document.getElementById("openAsset"),
  copyUrl: document.getElementById("copyUrl"),
  copyPath: document.getElementById("copyPath"),
  toast: document.getElementById("toast"),
};

let allAssets = [];
let categories = [{ id: "all", label: "all", count: 0 }];
let selectedAsset = null;
let loadingError = null;

const state = {
  category: "all",
  query: "",
  sort: "path-asc",
  view: "grid",
  items: [],
  tileWidth: 160,
  rowHeight: 210,
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
    haystack: `${entry.path} ${entry.display_name} ${entry.category} ${extension}`.toLowerCase(),
  };
}

function manifestSource() {
  const override = new URLSearchParams(window.location.search).get("manifest");
  return override || MANIFEST_URL;
}

function readCachedAssets() {
  if (manifestSource() !== MANIFEST_URL) return null;
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
  if (manifestSource() !== MANIFEST_URL) return;
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify({ saved_at: Date.now(), assets }));
  } catch {
    // The viewer remains fully functional without local storage.
  }
}

function readPreferences() {
  try {
    const preferences = JSON.parse(localStorage.getItem(PREFERENCES_KEY) || "null");
    if (preferences?.sort) state.sort = preferences.sort;
    if (preferences?.view === "grid" || preferences?.view === "list") state.view = preferences.view;
  } catch {
    // Defaults are used when preferences cannot be read.
  }
}

function writePreferences() {
  try {
    localStorage.setItem(PREFERENCES_KEY, JSON.stringify({ sort: state.sort, view: state.view }));
  } catch {
    // Preferences are optional.
  }
}

async function fetchAssets() {
  const cached = readCachedAssets();
  if (cached) return cached;

  const response = await fetch(manifestSource());
  if (!response.ok) {
    throw new Error(`Asset manifest request failed: ${response.status}`);
  }

  const data = await response.json();
  const assets = (data.assets || [])
    .map(assetFromManifestEntry)
    .filter(Boolean);
  writeCachedAssets(assets);
  return assets;
}

function compareAssets(left, right) {
  switch (state.sort) {
    case "path-desc":
      return right.path.localeCompare(left.path);
    case "name-asc":
      return left.name.localeCompare(right.name) || left.path.localeCompare(right.path);
    case "category-asc":
      return left.category.localeCompare(right.category) || left.path.localeCompare(right.path);
    default:
      return left.path.localeCompare(right.path);
  }
}

function setAssets(assets) {
  allAssets = assets.map((asset, index) => ({ ...asset, id: index }));
  const categoryCounts = allAssets.reduce((counts, asset) => {
    counts.set(asset.category, (counts.get(asset.category) || 0) + 1);
    return counts;
  }, new Map());

  categories = [
    { id: "all", label: "all assets", count: allAssets.length },
    ...Array.from(categoryCounts, ([id, count]) => ({ id, label: id, count }))
      .sort((a, b) => a.label.localeCompare(b.label)),
  ];

  renderCategoryButtons();
  applyFilters();
  openDeepLinkedAsset();
}

function renderCategoryButtons() {
  els.categoryList.textContent = "";
  els.mobileCategories.textContent = "";

  for (const category of categories) {
    const label = labelForCategory(category.label);
    const button = document.createElement("button");
    button.className = "category-button";
    button.type = "button";
    button.dataset.category = category.id;
    button.setAttribute("aria-current", category.id === state.category ? "true" : "false");

    const name = document.createElement("span");
    name.className = "category-name";
    name.textContent = label;
    const count = document.createElement("span");
    count.className = "category-count";
    count.textContent = category.count.toLocaleString();
    button.append(name, count);
    button.addEventListener("click", () => setCategory(category.id));
    els.categoryList.appendChild(button);

    const chip = document.createElement("button");
    chip.className = "chip";
    chip.type = "button";
    chip.dataset.category = category.id;
    chip.setAttribute("aria-current", category.id === state.category ? "true" : "false");
    chip.textContent = `${label} ${category.count.toLocaleString()}`;
    chip.addEventListener("click", () => setCategory(category.id));
    els.mobileCategories.appendChild(chip);
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
  state.items = allAssets
    .filter((asset) => {
      const inCategory = state.category === "all" || asset.category === state.category;
      return inCategory && (!query || asset.haystack.includes(query));
    })
    .sort(compareAssets);

  state.lastRangeKey = "";
  els.gridShell.scrollTop = 0;
  updateLayout();
  renderVisible();
}

function updateLayout() {
  const shellStyles = getComputedStyle(els.gridShell);
  const horizontalPadding = parseFloat(shellStyles.paddingLeft) + parseFloat(shellStyles.paddingRight);
  const width = Math.max(0, els.gridShell.clientWidth - horizontalPadding - 2);

  if (state.view === "list") {
    state.columns = 1;
    state.tileWidth = width;
    state.gap = 8;
    state.rowHeight = 88;
  } else {
    const compact = width < 620;
    const minWidth = compact ? 132 : 164;
    const maxWidth = compact ? 180 : 228;
    state.gap = compact ? 10 : 12;
    state.columns = Math.max(1, Math.floor((width + state.gap) / (minWidth + state.gap)));
    state.tileWidth = Math.min(maxWidth, Math.floor((width - state.gap * (state.columns - 1)) / state.columns));
    state.rowHeight = Math.round(state.tileWidth + 66);
  }

  const rows = Math.ceil(state.items.length / state.columns);
  els.spacer.style.height = `${Math.max(0, rows * state.rowHeight)}px`;
  els.empty.hidden = state.items.length > 0 && !loadingError;

  els.summary.textContent = state.query
    ? `${state.items.length.toLocaleString()} matches for “${state.query.trim()}”`
    : `${state.items.length.toLocaleString()} published images`;
}

function renderVisible() {
  updateLayout();
  const scrollTop = els.gridShell.scrollTop;
  const viewportHeight = els.gridShell.clientHeight;
  const overscanRows = 4;
  const startRow = Math.max(0, Math.floor(scrollTop / state.rowHeight) - overscanRows);
  const endRow = Math.min(
    Math.ceil(state.items.length / state.columns),
    Math.ceil((scrollTop + viewportHeight) / state.rowHeight) + overscanRows,
  );
  const startIndex = startRow * state.columns;
  const endIndex = Math.min(state.items.length, endRow * state.columns);
  const rangeKey = `${startIndex}:${endIndex}:${state.columns}:${state.tileWidth}:${state.rowHeight}:${selectedAsset?.path || ""}`;
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
    tile.className = `tile${selectedAsset?.path === asset.path ? " selected" : ""}`;
    tile.style.width = `${state.tileWidth}px`;
    tile.style.height = `${state.rowHeight - state.gap}px`;
    tile.style.transform = `translate3d(${left}px, ${top}px, 0)`;
    tile.dataset.assetPath = asset.path;

    const main = document.createElement("button");
    main.className = "tile-main";
    main.type = "button";
    main.title = `Preview ${asset.path}`;
    main.setAttribute("aria-label", `Preview ${asset.name}, ${asset.path}`);
    main.addEventListener("click", () => openDetails(asset));

    const thumb = document.createElement("span");
    thumb.className = "thumb";
    const img = document.createElement("img");
    img.src = asset.url;
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    thumb.appendChild(img);

    const meta = document.createElement("span");
    meta.className = "tile-meta";
    const name = document.createElement("span");
    name.className = "tile-name";
    name.textContent = asset.name;
    const path = document.createElement("span");
    path.className = "tile-path";
    path.textContent = asset.path;
    meta.append(name, path);
    main.append(thumb, meta);

    const copy = document.createElement("button");
    copy.className = "tile-copy";
    copy.type = "button";
    copy.textContent = "Copy URL";
    copy.setAttribute("aria-label", `Copy URL for ${asset.name}`);
    copy.addEventListener("click", () => copyValue(asset.url, "Copied asset URL"));

    tile.append(main, copy);
    fragment.appendChild(tile);
  }

  els.spacer.replaceChildren(fragment);
}

function deepLinkedPath() {
  return new URLSearchParams(window.location.hash.slice(1)).get("asset") || "";
}

function openDeepLinkedAsset() {
  const path = deepLinkedPath();
  if (!path) return;
  const asset = allAssets.find((item) => item.path === path);
  if (asset) openDetails(asset, { updateHistory: false, focus: false });
}

function openDetails(asset, options = {}) {
  selectedAsset = asset;
  els.detailName.textContent = asset.name;
  els.detailImage.src = asset.url;
  els.detailImage.alt = `Preview of ${asset.name}`;
  els.detailFormat.textContent = asset.ext;
  els.detailCategory.textContent = labelForCategory(asset.category);
  els.detailDimensions.textContent = "Loading…";
  els.detailPath.textContent = asset.path;
  els.downloadAsset.href = asset.url;
  els.downloadAsset.download = asset.path.split("/").pop();
  els.openAsset.href = asset.url;
  els.app.classList.add("details-open");
  els.detailsPanel.setAttribute("aria-hidden", "false");
  els.detailsScrim.hidden = false;

  els.detailImage.onload = () => {
    els.detailDimensions.textContent = `${els.detailImage.naturalWidth.toLocaleString()} × ${els.detailImage.naturalHeight.toLocaleString()} px`;
  };
  els.detailImage.onerror = () => {
    els.detailDimensions.textContent = "Preview unavailable";
  };

  if (options.updateHistory !== false) {
    const nextHash = new URLSearchParams({ asset: asset.path }).toString();
    history.replaceState(null, "", `${window.location.pathname}${window.location.search}#${nextHash}`);
  }
  state.lastRangeKey = "";
  renderVisible();
  if (options.focus !== false && window.innerWidth <= 860) {
    els.closeDetails.focus({ preventScroll: true });
  }
}

function closeDetails({ updateHistory = true } = {}) {
  if (!selectedAsset) return;
  selectedAsset = null;
  els.app.classList.remove("details-open");
  els.detailsPanel.setAttribute("aria-hidden", "true");
  els.detailsScrim.hidden = true;
  els.detailImage.removeAttribute("src");
  if (updateHistory) {
    history.replaceState(null, "", `${window.location.pathname}${window.location.search}`);
  }
  state.lastRangeKey = "";
  renderVisible();
}

async function copyValue(value, message) {
  try {
    await navigator.clipboard.writeText(value);
    showToast(message);
  } catch {
    const textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand("copy");
    textarea.remove();
    showToast(message);
  }
}

let toastTimer = 0;
function showToast(message) {
  els.toast.textContent = message;
  els.toast.classList.add("visible");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => els.toast.classList.remove("visible"), 1400);
}

function setView(view) {
  if (state.view === view) return;
  state.view = view;
  els.gridShell.classList.toggle("list-view", view === "list");
  els.gridView.setAttribute("aria-pressed", String(view === "grid"));
  els.listView.setAttribute("aria-pressed", String(view === "list"));
  state.lastRangeKey = "";
  writePreferences();
  renderVisible();
}

function resetFilters() {
  loadingError = null;
  state.category = "all";
  state.query = "";
  els.searchInput.value = "";
  updateAriaCurrent();
  applyFilters();
  els.searchInput.focus();
}

let scrollFrame = 0;
els.gridShell.addEventListener("scroll", () => {
  if (scrollFrame) return;
  scrollFrame = requestAnimationFrame(() => {
    scrollFrame = 0;
    renderVisible();
  });
}, { passive: true });

const resizeObserver = new ResizeObserver(() => {
  state.lastRangeKey = "";
  renderVisible();
});
resizeObserver.observe(els.gridShell);

els.searchInput.addEventListener("input", () => {
  state.query = els.searchInput.value;
  applyFilters();
});
els.sortSelect.addEventListener("change", () => {
  state.sort = els.sortSelect.value;
  writePreferences();
  applyFilters();
});
els.gridView.addEventListener("click", () => setView("grid"));
els.listView.addEventListener("click", () => setView("list"));
els.emptyAction.addEventListener("click", () => {
  if (loadingError) {
    initialize();
  } else {
    resetFilters();
  }
});
els.closeDetails.addEventListener("click", () => closeDetails());
els.detailsScrim.addEventListener("click", () => closeDetails());
els.copyUrl.addEventListener("click", () => {
  if (selectedAsset) copyValue(selectedAsset.url, "Copied asset URL");
});
els.copyPath.addEventListener("click", () => {
  if (selectedAsset) copyValue(selectedAsset.path, "Copied asset path");
});
window.addEventListener("hashchange", () => {
  const path = deepLinkedPath();
  if (!path) {
    closeDetails({ updateHistory: false });
    return;
  }
  const asset = allAssets.find((item) => item.path === path);
  if (asset) openDetails(asset, { updateHistory: false, focus: false });
});
window.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && selectedAsset) {
    closeDetails();
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    els.searchInput.focus();
    els.searchInput.select();
  }
  if (
    event.key === "/"
    && document.activeElement?.tagName !== "INPUT"
    && document.activeElement?.tagName !== "SELECT"
  ) {
    event.preventDefault();
    els.searchInput.focus();
  }
});

async function initialize() {
  loadingError = null;
  els.empty.hidden = true;
  els.summary.textContent = "Loading asset manifest…";
  els.emptyAction.textContent = "Reset filters";
  try {
    setAssets(await fetchAssets());
  } catch (error) {
    console.error(error);
    loadingError = error;
    allAssets = [];
    state.items = [];
    els.spacer.replaceChildren();
    els.spacer.style.height = "0";
    els.summary.textContent = "Could not load the asset manifest";
    els.emptyTitle.textContent = "Assets are temporarily unavailable";
    els.emptyBody.textContent = "Check your connection, then try again.";
    els.emptyAction.textContent = "Try again";
    els.empty.hidden = false;
  }
}

readPreferences();
els.sortSelect.value = state.sort;
els.gridShell.classList.toggle("list-view", state.view === "list");
els.gridView.setAttribute("aria-pressed", String(state.view === "grid"));
els.listView.setAttribute("aria-pressed", String(state.view === "list"));
initialize();
