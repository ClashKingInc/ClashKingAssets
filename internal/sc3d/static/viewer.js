import * as THREE from "https://esm.sh/three@0.165.0";
import { OrbitControls } from "https://esm.sh/three@0.165.0/examples/jsm/controls/OrbitControls.js";
import { GLTFExporter } from "https://esm.sh/three@0.165.0/examples/jsm/exporters/GLTFExporter.js";
import { GLTFLoader } from "https://esm.sh/three@0.165.0/examples/jsm/loaders/GLTFLoader.js";
import {
  decodeContinuousPackedAnimation as decodeContinuousPackedAnimationData,
  readContinuousPackedBases,
  resolveAliasedRotationTransform,
  resolveAnimationLocalTransform,
  resolveAnimationSourceName,
  requiresAnimationGlobalRemap
} from "./animation-codec.mjs";
import { decodeNormalizedWeightVector } from "./weight-codec.mjs";
import { isPoseAnimationAsset } from "./pose-candidates.mjs";
import { webPAnimationOptions } from "./webp-options.mjs";

const FBT = {
  NULL: 0,
  INT: 1,
  UINT: 2,
  FLOAT: 3,
  KEY: 4,
  STRING: 5,
  INDIRECT_INT: 6,
  INDIRECT_UINT: 7,
  INDIRECT_FLOAT: 8,
  MAP: 9,
  VECTOR: 10,
  VECTOR_INT: 11,
  VECTOR_UINT: 12,
  VECTOR_FLOAT: 13,
  VECTOR_KEY: 14,
  VECTOR_INT2: 16,
  VECTOR_UINT2: 17,
  VECTOR_FLOAT2: 18,
  VECTOR_INT3: 19,
  VECTOR_UINT3: 20,
  VECTOR_FLOAT3: 21,
  VECTOR_INT4: 22,
  VECTOR_UINT4: 23,
  VECTOR_FLOAT4: 24,
  BLOB: 25,
  BOOL: 26,
  VECTOR_BOOL: 36
};

const palette = [
  0xf3b53d, 0x79d0ff, 0xe86f58, 0x77c86f, 0xc68cff, 0xf2e06e, 0xff8db3, 0x98a3ad,
  0x45b7a7, 0xd48a48, 0xa7d36b, 0x8ea5ff, 0xf5f0df, 0xb9bec5, 0x6ed2a0, 0xe06983
];

const DEFAULT_ASSET_ORIGIN = "https://game-assets.clashofclans.com";

function defaultLandingSettings() {
  return {
    slug: "skin",
    model: { scale: 1, offsetY: 0, initialYaw: 0 },
    animation: { speed: 1, loop: true },
    camera: { fov: 36, distance: 2.1, targetY: 0 },
    interaction: {
      allowYaw: true,
      minYaw: -45,
      maxYaw: 45,
      allowPitch: false,
      allowZoom: false,
      allowPan: false,
      dragSensitivity: 0.8,
      autoRotate: false,
      autoRotateSpeed: 0.5
    }
  };
}

const state = {
  config: null,
  assetBaseURL: "",
  files: [],
  assets: [],
  filter: "geo",
  selectedPath: "",
  selectedPosePath: "",
  colorMode: "parts",
  smoothShading: true,
  wireframe: false,
  gridVisible: true,
  animationPlaying: true,
  exportInProgress: false,
  animationStartedAt: 0,
  animationFrame: -1,
  group: null,
  decoded: null,
  activePose: null,
  positionAttribute: null,
  poseGeometryInfos: [],
  poseCache: new Map(),
  textureCache: new Map(),
  landing: defaultLandingSettings(),
  frameInfo: null
};

function viewerConfigFromURL() {
  const params = new URLSearchParams(window.location.search);
  const fingerprint = (params.get("fingerprint") || params.get("fp") || "").trim();
  const baseURL = (params.get("base_url") || params.get("base") || "").trim().replace(/\/+$/, "");
  if (!fingerprint && !baseURL) return null;
  return {
    fingerprint: fingerprint || baseURL.split("/").filter(Boolean).pop() || "",
    base_url: baseURL || `${DEFAULT_ASSET_ORIGIN}/${fingerprint}`
  };
}

async function loadViewerConfig() {
  const urlConfig = viewerConfigFromURL();
  if (urlConfig) return { ...urlConfig, proxy: false };
  const response = await fetch("./config.json");
  if (!response.ok) throw new Error(`failed to load viewer config: ${response.status} ${response.statusText}`);
  return { ...(await response.json()), proxy: true };
}

function remoteURL(path) {
  if (!path || path.includes("..") || path.startsWith("/")) throw new Error(`invalid remote path: ${path}`);
  const encodedPath = path.split("/").map(encodeURIComponent).join("/");
  if (state.config?.proxy) return `./remote/${encodedPath}`;
  if (!state.assetBaseURL) throw new Error("missing asset base URL");
  return `${state.assetBaseURL}/${encodedPath}`;
}

async function fetchRemote(path) {
  let response;
  try {
    response = await fetch(remoteURL(path));
  } catch (error) {
    throw new Error(`failed to fetch ${path}; the asset host must allow browser CORS access (${error.message})`);
  }
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
  return response;
}

const els = {
  viewport: document.getElementById("viewport"),
  fingerprint: document.getElementById("fingerprint"),
  search: document.getElementById("search"),
  poseSelect: document.getElementById("pose-select"),
  colorMode: document.getElementById("color-mode"),
  filters: [...document.querySelectorAll(".filter")],
  assetCount: document.getElementById("asset-count"),
  assetList: document.getElementById("asset-list"),
  status: document.getElementById("status"),
  inspector: document.getElementById("inspector"),
  resetView: document.getElementById("reset-view"),
  nudgeUp: document.getElementById("nudge-up"),
  nudgeDown: document.getElementById("nudge-down"),
  playAnimation: document.getElementById("play-animation"),
  exportWebp: document.getElementById("export-webp"),
  webpLoop: document.getElementById("webp-loop"),
  toggleGrid: document.getElementById("toggle-grid"),
  smoothShading: document.getElementById("smooth-shading"),
  wireframe: document.getElementById("wireframe"),
  landingSlug: document.getElementById("landing-slug"),
  landingScale: document.getElementById("landing-scale"),
  landingOffsetY: document.getElementById("landing-offset-y"),
  landingYaw: document.getElementById("landing-yaw"),
  landingAnimationSpeed: document.getElementById("landing-animation-speed"),
  landingFov: document.getElementById("landing-fov"),
  landingDistance: document.getElementById("landing-distance"),
  landingTargetY: document.getElementById("landing-target-y"),
  landingDragSensitivity: document.getElementById("landing-drag-sensitivity"),
  landingMinYaw: document.getElementById("landing-min-yaw"),
  landingMaxYaw: document.getElementById("landing-max-yaw"),
  landingAllowYaw: document.getElementById("landing-allow-yaw"),
  landingAllowPitch: document.getElementById("landing-allow-pitch"),
  landingAllowZoom: document.getElementById("landing-allow-zoom"),
  landingAllowPan: document.getElementById("landing-allow-pan"),
  landingAutoRotate: document.getElementById("landing-auto-rotate"),
  landingAutoRotateSpeed: document.getElementById("landing-auto-rotate-speed"),
  exportGLB: document.getElementById("export-glb"),
  exportSkinJSON: document.getElementById("export-skin-json"),
  landingJSONPreview: document.getElementById("landing-json-preview")
};

const scene = new THREE.Scene();
scene.background = null;
const camera = new THREE.PerspectiveCamera(36, 1, 0.1, 4000);
camera.position.set(0, 35, 95);
let renderer = null;
let controls = null;
let renderUnavailableMessage = "";

try {
  renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, preserveDrawingBuffer: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.setClearColor(0x000000, 0);
  els.viewport.appendChild(renderer.domElement);
} catch (error) {
  renderUnavailableMessage = webGLUnavailableMessage(error);
  showViewportError(renderUnavailableMessage);
  console.error(error);
}

if (renderer) {
  controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true;
  controls.dampingFactor = 0.06;
  controls.enablePan = true;
  controls.enableRotate = true;
  controls.enableZoom = true;
  controls.screenSpacePanning = true;
  controls.target.set(0, 12, 0);
}

scene.add(new THREE.HemisphereLight(0xffffff, 0x28313a, 2.6));
const keyLight = new THREE.DirectionalLight(0xffffff, 2.2);
keyLight.position.set(45, 70, 55);
scene.add(keyLight);
const textureLoader = new THREE.TextureLoader();
const grid = new THREE.GridHelper(90, 18, 0x36404a, 0x22282e);
grid.position.y = -0.02;
scene.add(grid);

function resize() {
  const rect = els.viewport.getBoundingClientRect();
  if (renderer) renderer.setSize(rect.width, rect.height, true);
  camera.aspect = Math.max(rect.width, 1) / Math.max(rect.height, 1);
  camera.updateProjectionMatrix();
}

window.addEventListener("resize", resize);
resize();

function animate() {
  if (!renderer || !controls) return;
  requestAnimationFrame(animate);
  updateAnimation();
  controls.update();
  renderer.render(scene, camera);
}
animate();

function canRenderScene() {
  return Boolean(renderer && controls);
}

function webGLUnavailableMessage(error) {
  const detail = error && error.message ? ` (${error.message})` : "";
  return `WebGL is unavailable in this browser${detail}. Enable graphics acceleration/WebGL or use another browser to render models.`;
}

function showViewportError(message) {
  const error = document.createElement("div");
  error.className = "viewport-error";
  error.textContent = message;
  els.viewport.appendChild(error);
}

function setStatus(text, isError) {
  isError = isError || false;
  els.status.textContent = text;
  els.status.classList.toggle("error", isError);
}

function setInspector(rows) {
  els.inspector.innerHTML = "";
  for (let i = 0; i < rows.length; i += 1) {
    const label = rows[i][0];
    const value = rows[i][1];
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = String(value);
    els.inspector.append(dt, dd);
  }
}

function clampNumber(value, fallback, minimum, maximum) {
  value = Number(value);
  if (!Number.isFinite(value)) value = fallback;
  return Math.min(maximum, Math.max(minimum, value));
}

function cleanSlug(value) {
  const slug = String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return slug || "skin";
}

function modelSlug(path) {
  const file = String(path || "").split("/").pop() || "skin";
  return cleanSlug(file.replace(/_geo(?:_dl_opt)?\.glb$/i, "").replace(/\.glb$/i, ""));
}

function readLandingSettings() {
  const settings = state.landing;
  settings.slug = cleanSlug(els.landingSlug.value);
  settings.model.scale = clampNumber(els.landingScale.value, 1, 0.01, 100);
  settings.model.offsetY = clampNumber(els.landingOffsetY.value, 0, -10000, 10000);
  settings.model.initialYaw = clampNumber(els.landingYaw.value, 0, -180, 180);
  settings.animation.speed = clampNumber(els.landingAnimationSpeed.value, 1, 0.05, 4);
  settings.camera.fov = clampNumber(els.landingFov.value, 36, 10, 90);
  settings.camera.distance = clampNumber(els.landingDistance.value, 2.1, 0.5, 10);
  settings.camera.targetY = clampNumber(els.landingTargetY.value, 0, -10000, 10000);
  settings.interaction.dragSensitivity = clampNumber(els.landingDragSensitivity.value, 0.8, 0.05, 3);
  settings.interaction.minYaw = clampNumber(els.landingMinYaw.value, -45, -180, 180);
  settings.interaction.maxYaw = clampNumber(els.landingMaxYaw.value, 45, -180, 180);
  if (settings.interaction.minYaw > settings.interaction.maxYaw) {
    [settings.interaction.minYaw, settings.interaction.maxYaw] = [settings.interaction.maxYaw, settings.interaction.minYaw];
  }
  settings.interaction.allowYaw = els.landingAllowYaw.checked;
  settings.interaction.allowPitch = els.landingAllowPitch.checked;
  settings.interaction.allowZoom = els.landingAllowZoom.checked;
  settings.interaction.allowPan = els.landingAllowPan.checked;
  settings.interaction.autoRotate = els.landingAutoRotate.checked;
  settings.interaction.autoRotateSpeed = clampNumber(els.landingAutoRotateSpeed.value, 0.5, -5, 5);
  return settings;
}

function writeLandingSettings() {
  const settings = state.landing;
  els.landingSlug.value = settings.slug;
  els.landingScale.value = String(settings.model.scale);
  els.landingOffsetY.value = String(settings.model.offsetY);
  els.landingYaw.value = String(settings.model.initialYaw);
  els.landingAnimationSpeed.value = String(settings.animation.speed);
  els.landingFov.value = String(settings.camera.fov);
  els.landingDistance.value = String(settings.camera.distance);
  els.landingTargetY.value = String(settings.camera.targetY);
  els.landingDragSensitivity.value = String(settings.interaction.dragSensitivity);
  els.landingMinYaw.value = String(settings.interaction.minYaw);
  els.landingMaxYaw.value = String(settings.interaction.maxYaw);
  els.landingAllowYaw.checked = settings.interaction.allowYaw;
  els.landingAllowPitch.checked = settings.interaction.allowPitch;
  els.landingAllowZoom.checked = settings.interaction.allowZoom;
  els.landingAllowPan.checked = settings.interaction.allowPan;
  els.landingAutoRotate.checked = settings.interaction.autoRotate;
  els.landingAutoRotateSpeed.value = String(settings.interaction.autoRotateSpeed);
}

function landingMetadata() {
  const settings = readLandingSettings();
  return {
    schemaVersion: 1,
    slug: settings.slug,
    model: "model.glb",
    animation: {
      clip: state.selectedPosePath ? poseLabel(state.selectedPosePath) : "",
      speed: settings.animation.speed,
      loop: settings.animation.loop
    },
    transform: {
      scale: settings.model.scale,
      offsetY: settings.model.offsetY,
      initialYaw: settings.model.initialYaw
    },
    camera: { ...settings.camera },
    interaction: { ...settings.interaction }
  };
}

function updateLandingJSONPreview() {
  els.landingJSONPreview.textContent = JSON.stringify(landingMetadata(), null, 2);
}

function applyLandingPreview(resetCamera = false) {
  const settings = readLandingSettings();
  updateLandingJSONPreview();
  if (!canRenderScene()) return;

  if (state.group && state.frameInfo) {
    const { center, maxDim } = state.frameInfo;
    const yaw = THREE.MathUtils.degToRad(settings.model.initialYaw);
    const centered = center.clone().multiplyScalar(settings.model.scale).applyAxisAngle(new THREE.Vector3(0, 1, 0), yaw).negate();
    centered.y += settings.model.offsetY * settings.model.scale;
    state.group.scale.setScalar(settings.model.scale);
    state.group.rotation.set(0, yaw, 0);
    state.group.position.copy(centered);
    if (resetCamera) {
      camera.position.set(0, maxDim * settings.model.scale * 0.55, maxDim * settings.model.scale * settings.camera.distance);
      camera.near = Math.max(maxDim * settings.model.scale / 1000, 0.01);
      camera.far = Math.max(maxDim * settings.model.scale * 20, 100);
    }
  }

  camera.fov = settings.camera.fov;
  camera.updateProjectionMatrix();
  controls.target.set(0, settings.camera.targetY * settings.model.scale, 0);
  controls.enableRotate = settings.interaction.allowYaw || settings.interaction.allowPitch;
  controls.enableZoom = settings.interaction.allowZoom;
  controls.enablePan = settings.interaction.allowPan;
  controls.rotateSpeed = settings.interaction.dragSensitivity;
  controls.autoRotate = settings.interaction.autoRotate;
  controls.autoRotateSpeed = settings.interaction.autoRotateSpeed;
  controls.minAzimuthAngle = settings.interaction.allowYaw ? THREE.MathUtils.degToRad(settings.interaction.minYaw) : 0;
  controls.maxAzimuthAngle = settings.interaction.allowYaw ? THREE.MathUtils.degToRad(settings.interaction.maxYaw) : 0;
  controls.minPolarAngle = settings.interaction.allowPitch ? 0.01 : Math.PI / 2;
  controls.maxPolarAngle = settings.interaction.allowPitch ? Math.PI - 0.01 : Math.PI / 2;
  controls.update();
  if (resetCamera) controls.saveState();
}

class FlatTableReader {
  constructor(bytes) {
    this.bytes = bytes;
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }

  u8(pos) {
    return this.view.getUint8(pos);
  }

  u16(pos) {
    return this.view.getUint16(pos, true);
  }

  i32(pos) {
    return this.view.getInt32(pos, true);
  }

  u32(pos) {
    return this.view.getUint32(pos, true);
  }

  f32(pos) {
    return this.view.getFloat32(pos, true);
  }

  rootTable() {
    return this.u32(0);
  }

  fieldPos(table, field) {
    const vtable = table - this.i32(table);
    const vtableLength = this.u16(vtable);
    const fieldOffsetPos = vtable + 4 + field * 2;
    if (fieldOffsetPos + 2 > vtable + vtableLength) return 0;
    const offset = this.u16(fieldOffsetPos);
    return offset === 0 ? 0 : table + offset;
  }

  u32Field(table, field, fallback = 0) {
    const pos = this.fieldPos(table, field);
    return pos ? this.u32(pos) : fallback;
  }

  offsetDest(table, field) {
    const pos = this.fieldPos(table, field);
    return pos ? pos + this.u32(pos) : 0;
  }

  stringField(table, field) {
    const dest = this.offsetDest(table, field);
    return dest ? this.stringAt(dest) : "";
  }

  bytesField(table, field) {
    const dest = this.offsetDest(table, field);
    if (!dest) return new Uint8Array();
    const length = this.u32(dest);
    return this.bytes.slice(dest + 4, dest + 4 + length);
  }

  floatVectorField(table, field) {
    const dest = this.offsetDest(table, field);
    if (!dest) return [];
    const count = this.u32(dest);
    const values = [];
    for (let i = 0; i < count; i += 1) {
      values.push(this.f32(dest + 4 + i * 4));
    }
    return values;
  }

  stringAt(pos) {
    const length = this.u32(pos);
    const bytes = this.bytes.slice(pos + 4, pos + 4 + length);
    return new TextDecoder().decode(bytes);
  }

  vectorTables(table, field) {
    const dest = this.offsetDest(table, field);
    if (!dest) return [];
    const count = this.u32(dest);
    const out = [];
    for (let i = 0; i < count; i += 1) {
      const elem = dest + 4 + i * 4;
      out.push(elem + this.u32(elem));
    }
    return out;
  }

  vectorU32Field(table, field) {
    const dest = this.offsetDest(table, field);
    if (!dest) return [];
    const count = this.u32(dest);
    const out = [];
    for (let i = 0; i < count; i += 1) {
      out.push(this.u32(dest + 4 + i * 4));
    }
    return out;
  }
}

class FlexReader {
  constructor(bytes) {
    this.bytes = bytes;
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    this.text = new TextDecoder();
  }

  u(pos, width) {
    if (width === 1) return this.view.getUint8(pos);
    if (width === 2) return this.view.getUint16(pos, true);
    if (width === 4) return this.view.getUint32(pos, true);
    return Number(this.view.getBigUint64(pos, true));
  }

  i(pos, width) {
    if (width === 1) return this.view.getInt8(pos);
    if (width === 2) return this.view.getInt16(pos, true);
    if (width === 4) return this.view.getInt32(pos, true);
    return Number(this.view.getBigInt64(pos, true));
  }

  f(pos, width) {
    if (width === 4) return this.view.getFloat32(pos, true);
    if (width === 8) return this.view.getFloat64(pos, true);
    return this.i(pos, width);
  }

  indirect(pos, parentWidth) {
    return pos - this.u(pos, parentWidth);
  }

  root() {
    if (this.bytes.length < 2) return null;
    const parentWidth = this.bytes[this.bytes.length - 1];
    const packedType = this.bytes[this.bytes.length - 2];
    return this.value(this.bytes.length - 2 - parentWidth, parentWidth, packedType);
  }

  value(pos, parentWidth, packedType) {
    const byteWidth = 1 << (packedType & 3);
    const type = packedType >> 2;
    if (type === FBT.NULL) return null;
    if (type === FBT.INT) return this.i(pos, parentWidth);
    if (type === FBT.UINT || type === FBT.BOOL) return type === FBT.BOOL ? this.u(pos, parentWidth) !== 0 : this.u(pos, parentWidth);
    if (type === FBT.FLOAT) return this.f(pos, parentWidth);
    if (type === FBT.INDIRECT_INT) return this.i(this.indirect(pos, parentWidth), byteWidth);
    if (type === FBT.INDIRECT_UINT) return this.u(this.indirect(pos, parentWidth), byteWidth);
    if (type === FBT.INDIRECT_FLOAT) return this.f(this.indirect(pos, parentWidth), byteWidth);
    if (type === FBT.STRING) return this.stringAt(this.indirect(pos, parentWidth), byteWidth);
    if (type === FBT.KEY) return this.keyAt(this.indirect(pos, parentWidth));
    if (type === FBT.MAP) return this.mapAt(this.indirect(pos, parentWidth), byteWidth);
    if (type === FBT.VECTOR) return this.vectorAt(this.indirect(pos, parentWidth), byteWidth);
    if (type >= FBT.VECTOR_INT && type <= FBT.VECTOR_FLOAT) return this.typedVectorAt(this.indirect(pos, parentWidth), byteWidth, type - FBT.VECTOR_INT + FBT.INT);
    if (type >= FBT.VECTOR_INT2 && type <= FBT.VECTOR_FLOAT4) {
      const fixed = type - FBT.VECTOR_INT2;
      const length = Math.floor(fixed / 3) + 2;
      const elementType = (fixed % 3) + FBT.INT;
      return this.fixedVectorAt(this.indirect(pos, parentWidth), byteWidth, elementType, length);
    }
    if (type === FBT.BLOB) return this.blobAt(this.indirect(pos, parentWidth), byteWidth);
    if (type === FBT.VECTOR_BOOL) return this.typedVectorAt(this.indirect(pos, parentWidth), byteWidth, FBT.BOOL);
    return null;
  }

  stringAt(pos, width) {
    const length = this.u(pos - width, width);
    return this.text.decode(this.bytes.slice(pos, pos + length));
  }

  keyAt(pos) {
    let end = pos;
    while (end < this.bytes.length && this.bytes[end] !== 0) end += 1;
    return this.text.decode(this.bytes.slice(pos, end));
  }

  blobAt(pos, width) {
    const length = this.u(pos - width, width);
    return this.bytes.slice(pos, pos + length);
  }

  vectorAt(pos, width) {
    const length = this.u(pos - width, width);
    const out = [];
    const typePos = pos + length * width;
    for (let i = 0; i < length; i += 1) {
      out.push(this.value(pos + i * width, width, this.bytes[typePos + i]));
    }
    return out;
  }

  typedVectorAt(pos, width, elementType) {
    const length = this.u(pos - width, width);
    return this.fixedVectorAt(pos, width, elementType, length);
  }

  fixedVectorAt(pos, width, elementType, length) {
    const out = [];
    const packed = (elementType << 2) | Math.log2(width);
    for (let i = 0; i < length; i += 1) out.push(this.value(pos + i * width, width, packed));
    return out;
  }

  mapAt(pos, width) {
    const length = this.u(pos - width, width);
    const keysOffsetPos = pos - width * 3;
    const keysPos = this.indirect(keysOffsetPos, width);
    const keysWidth = this.u(keysOffsetPos + width, width);
    const out = {};
    const typePos = pos + length * width;
    for (let i = 0; i < length; i += 1) {
      const keyRefPos = keysPos + i * keysWidth;
      const key = this.keyAt(this.indirect(keyRefPos, keysWidth));
      out[key] = this.value(pos + i * width, width, this.bytes[typePos + i]);
    }
    return out;
  }
}

function parseGLB(arrayBuffer) {
  const bytes = new Uint8Array(arrayBuffer);
  const view = new DataView(arrayBuffer);
  if (view.getUint32(0, true) !== 0x46546c67) throw new Error("not a GLB file");
  const version = view.getUint32(4, true);
  if (version !== 2) throw new Error(`unsupported GLB version ${version}`);

  const chunks = {};
  let offset = 12;
  while (offset + 8 <= bytes.length) {
    const length = view.getUint32(offset, true);
    const type = String.fromCharCode(bytes[offset + 4], bytes[offset + 5], bytes[offset + 6], bytes[offset + 7]);
    chunks[type] = bytes.slice(offset + 8, offset + 8 + length);
    offset += 8 + length;
  }
  if (!chunks.FLA2 || !chunks["BIN\u0000"]) throw new Error("expected FLA2 and BIN chunks");
  return decodeFLA2(chunks.FLA2, chunks["BIN\u0000"]);
}

function decodeFLA2(fla2, bin) {
  const fb = new FlatTableReader(fla2);
  const root = fb.rootTable();
  const accessors = fb.vectorTables(root, 0).map((table) => ({
    bufferView: fb.u32Field(table, 0, 0),
    byteOffset: fb.u32Field(table, 1, 0),
    componentType: fb.u32Field(table, 2, 5123),
    count: fb.u32Field(table, 3, 0)
  }));
  const bufferViews = fb.vectorTables(root, 3).map((table) => ({
    buffer: fb.u32Field(table, 0, 0),
    byteLength: fb.u32Field(table, 1, 0),
    byteOffset: fb.u32Field(table, 2, 0)
  }));
  const meshes = fb.vectorTables(root, 12).map((table) => ({
    primitives: fb.vectorTables(table, 3).map((primitive) => ({
      indices: fb.u32Field(primitive, 3, 0),
      material: fb.u32Field(primitive, 4, 0)
    }))
  }));
  const skins = fb.vectorTables(root, 17).map((table) => ({
    inverseBindMatrices: fb.fieldPos(table, 2) ? fb.u32Field(table, 2, null) : null,
    joints: fb.vectorU32Field(table, 3)
  }));
  const nodes = fb.vectorTables(root, 13).map((table, index) => {
    const nodeExtension = decodeNodeExtension(fb.bytesField(table, 2));
    return {
      index,
      parent: nodeExtension && Number.isFinite(nodeExtension.parent) ? nodeExtension.parent : null,
      mesh: fb.fieldPos(table, 5) ? fb.u32Field(table, 5, 0) : null,
      name: fb.stringField(table, 6),
      rotation: vectorOrDefault(fb.floatVectorField(table, 7), [0, 0, 0, 1]),
      scale: vectorOrDefault(fb.floatVectorField(table, 8), [1, 1, 1]),
      skin: fb.fieldPos(table, 9) ? fb.u32Field(table, 9, null) : null,
      translation: vectorOrDefault(fb.floatVectorField(table, 10), [0, 0, 0])
    };
  });

  const extensionBytes = fb.bytesField(root, 6);
  const extensionRoot = extensionBytes.length ? new FlexReader(extensionBytes).root() : null;
  const extension = extensionRoot && extensionRoot.SC_odin_format ? extensionRoot.SC_odin_format : null;
  const materials = extension && extension.materials ? extension.materials : [];
  const extensionSummary = extension ? summarizeOdinExtension(extension) : null;
  if (!extension || !extension.meshDataInfos || !extension.meshDataInfos.length) {
    return {
      positions: new Float32Array(),
      vertexCount: 0,
      renderMeshes: [],
      meshCount: meshes.length,
      nodeCount: nodes.length,
      accessorCount: accessors.length,
      materialCount: extension && extension.materials ? extension.materials.length : 0,
      materials,
      extensionSummary,
      nodes,
      globals: computeGlobalMatrices(nodes),
      globalsByName: globalMatricesByName(nodes),
      nodesByName: nodesByName(nodes),
      inverseBindMatrices: [],
      jointNodes: [],
      skins: [],
      defaultSkin: null,
      weightedCount: 0,
      animation: decodeAnimation(extension && extension.animation, bin, accessors, bufferViews, nodes)
    };
  }
  const streams = extension.meshDataInfos.map((meshInfo, index) => decodeVertexStream(meshInfo, index, bin, bufferViews, extension.bufferView));
  if (!streams.length) throw new Error("missing mesh vertex streams");
  const indexView = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);

  const renderMeshes = [];
  const meshNodes = nodes.filter((node) => node.mesh !== null && node.mesh < meshes.length);
  for (const node of meshNodes) {
    const mesh = meshes[node.mesh];
    for (const primitive of mesh.primitives) {
      const accessor = accessors[primitive.indices];
      if (!accessor) continue;
      const accessorView = bufferViews[accessor.bufferView] || { byteOffset: 0 };
      const indexOffset = accessorView.byteOffset + accessor.byteOffset;
      const indices = readIndices(indexView, indexOffset, accessor);
      const streamIndex = chooseVertexStream(accessor, accessorView, indices, streams);
      renderMeshes.push({
        name: node.name || `mesh_${node.mesh}`,
        nodeIndex: node.index,
        mesh: node.mesh,
        skinIndex: Number.isInteger(node.skin) ? node.skin : null,
        material: primitive.material,
        streamIndex,
        indices,
        renderIndex: renderMeshes.length
      });
    }
  }

  const decodedSkins = skins.map((skin, index) => {
    const jointNodes = skin.joints.map((nodeIndex) => nodes[nodeIndex]).filter(Boolean);
    const inverseBindMatrices = readInverseBindMatrices(bin, accessors, bufferViews, jointNodes.length, skin.inverseBindMatrices);
    return { index, jointNodes, inverseBindMatrices };
  });
  const defaultSkin = decodedSkins.find((skin) => skin.jointNodes.length) || { index: null, jointNodes: [], inverseBindMatrices: [] };
  const jointNodes = defaultSkin.jointNodes;
  const inverseBindMatrices = defaultSkin.inverseBindMatrices;
  const globals = computeGlobalMatrices(nodes);

  const weightedCount = streams.reduce((total, stream) => total + countWeightedVertices(stream.boneWeights), 0);
  const vertexCount = streams.reduce((total, stream) => total + stream.vertexCount, 0);
  const animation = decodeAnimation(extension.animation, bin, accessors, bufferViews, nodes);
  const firstStream = streams[0];

  return {
    positions: firstStream.positions,
    uvs: firstStream.uvs,
    boneIndices: firstStream.boneIndices,
    boneWeights: firstStream.boneWeights,
    streams,
    vertexCount,
    renderMeshes,
    meshCount: meshes.length,
    nodeCount: nodes.length,
    accessorCount: accessors.length,
    materialCount: extension.materials ? extension.materials.length : 0,
    materials,
    extensionSummary,
    nodes,
    globals,
    globalsByName: globalMatricesByName(nodes, globals),
    nodesByName: nodesByName(nodes),
    inverseBindMatrices,
    jointNodes,
    skins: decodedSkins,
    defaultSkin,
    weightedCount,
    animation
  };
}

function decodeNodeExtension(bytes) {
  if (!bytes.length) return null;
  try {
    const root = new FlexReader(bytes).root();
    return root && root.SC_odin_format ? root.SC_odin_format : null;
  } catch (error) {
    return null;
  }
}

function summarizeOdinExtension(extension) {
  return {
    keys: Object.keys(extension),
    bufferView: extension.bufferView,
    materials: (extension.materials || []).map((material, index) => ({
      index,
      keys: Object.keys(material),
      name: material.name || "",
      shader: material.shader || "",
      blendMode: material.blendMode,
      variables: material.variables || null,
      constants: material.constants || null
    })),
    meshDataInfos: (extension.meshDataInfos || []).map((meshInfo, index) => ({
      index,
      keys: Object.keys(meshInfo),
      vertexDescriptorCount: meshInfo.vertexDescriptors ? meshInfo.vertexDescriptors.length : 0,
      vertexDescriptors: (meshInfo.vertexDescriptors || []).map((descriptor) => ({
        keys: Object.keys(descriptor),
        offset: descriptor.offset,
        stride: descriptor.stride,
        attributes: (descriptor.attributes || []).map((attribute) => ({
          keys: Object.keys(attribute),
          name: attribute.name,
          offset: attribute.offset,
          format: attribute.format
        }))
      }))
    }))
  };
}

function vectorOrDefault(values, fallback) {
  return values.length ? values : fallback;
}

function findVertexAttribute(descriptors, name) {
  for (const descriptor of descriptors) {
    const attribute = descriptor.attributes ? descriptor.attributes.find((attr) => attr.name === name) : null;
    if (attribute) return { descriptor, attribute };
  }
  return null;
}

function decodeVertexStream(meshInfo, streamIndex, bin, bufferViews, bufferViewIndex) {
  const descriptors = [...meshInfo.vertexDescriptors].sort((a, b) => a.offset - b.offset);
  const positionSource = findVertexAttribute(descriptors, "a_pos");
  if (!positionSource) throw new Error("missing a_pos vertex descriptor");
  const positionDescriptor = positionSource.descriptor;
  const positionAttribute = positionSource.attribute;
  const nextDescriptor = descriptors.find((descriptor) => descriptor.offset > positionDescriptor.offset);
  const bufferView = bufferViews[bufferViewIndex == null ? 0 : bufferViewIndex] || { byteOffset: 0, byteLength: bin.byteLength };
  const vertexStreamEnd = nextDescriptor ? nextDescriptor.offset : bufferView.byteLength;
  const vertexCount = Math.floor((vertexStreamEnd - positionDescriptor.offset) / positionDescriptor.stride);
  const positions = readPositions(bin, bufferView.byteOffset + positionDescriptor.offset, vertexCount, positionDescriptor.stride, positionAttribute.offset);
  const uvSource = findVertexAttribute(descriptors, "a_uv0");
  const uvs = uvSource
    ? readUVs(bin, bufferView.byteOffset + uvSource.descriptor.offset, vertexCount, uvSource.descriptor.stride, uvSource.attribute.offset)
    : new Float32Array(vertexCount * 2);
  const normalSource = findVertexAttribute(descriptors, "a_normal");
  const normals = normalSource
    ? readNormals(bin, bufferView.byteOffset + normalSource.descriptor.offset, vertexCount, normalSource.descriptor.stride, normalSource.attribute.offset)
    : null;
  const boneSource = findVertexAttribute(descriptors, "a_boneindex");
  const weightSource = findVertexAttribute(descriptors, "a_boneweights");
  const hasNormalizedWeightVector = Boolean(weightSource && weightSource.attribute.format === 36);
  const boneIndices = boneSource
    ? readU8x4(bin, bufferView.byteOffset + boneSource.descriptor.offset, vertexCount, boneSource.descriptor.stride, boneSource.attribute.offset)
    : new Uint8Array(vertexCount * 4);
  const rawBoneWeights = weightSource
    ? readU8x4(bin, bufferView.byteOffset + weightSource.descriptor.offset, vertexCount, weightSource.descriptor.stride, weightSource.attribute.offset)
    : new Uint8Array(vertexCount * 4);
  const normalizedWeights = hasNormalizedWeightVector
    ? readNormalizedWeightVectors(bin, bufferView.byteOffset + weightSource.descriptor.offset, vertexCount, weightSource.descriptor.stride, weightSource.attribute.offset)
    : null;
  const boneWeights = normalizedWeights
    ? normalizedWeights.weights
    : rawBoneWeights.slice();
  const weightRepairStats = normalizedWeights
    ? normalizedWeights.stats
    : repairBoneWeights(boneIndices, boneWeights, Boolean(boneSource && weightSource), false);

  return {
    index: streamIndex,
    minOffset: bufferView.byteOffset + Math.min(...descriptors.map((descriptor) => descriptor.offset)),
    vertexCount,
    positions,
    uvs,
    normals,
    boneIndices,
    rawBoneWeights,
    boneWeights,
    weightRepairStats
  };
}

function readIndices(view, offset, accessor) {
  if (accessor.componentType === 5125) {
    const indices = new Uint32Array(accessor.count);
    for (let i = 0; i < accessor.count; i += 1) {
      indices[i] = view.getUint32(offset + i * 4, true);
    }
    return indices;
  }
  const indices = new Uint16Array(accessor.count);
  for (let i = 0; i < accessor.count; i += 1) {
    indices[i] = view.getUint16(offset + i * 2, true);
  }
  return indices;
}

function chooseVertexStream(accessor, accessorView, indices, streams) {
  const indexElementSize = accessor.componentType === 5125 ? 4 : 2;
  const indexEnd = accessorView.byteOffset + accessor.byteOffset + accessor.count * indexElementSize;
  let maxIndex = 0;
  for (let i = 0; i < indices.length; i += 1) maxIndex = Math.max(maxIndex, indices[i]);

  const byOffset = streams
    .filter((stream) => maxIndex < stream.vertexCount && indexEnd <= stream.minOffset + 16)
    .sort((a, b) => a.minOffset - b.minOffset)[0];
  if (byOffset) return byOffset.index;

  const byRange = streams.find((stream) => maxIndex < stream.vertexCount);
  return byRange ? byRange.index : 0;
}

function readPositions(bin, baseOffset, vertexCount, stride, attributeOffset) {
  const view = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);
  const positions = new Float32Array(vertexCount * 3);
  for (let i = 0; i < vertexCount; i += 1) {
    const src = baseOffset + i * stride + attributeOffset;
    positions[i * 3] = view.getFloat32(src, true);
    positions[i * 3 + 1] = view.getFloat32(src + 4, true);
    positions[i * 3 + 2] = view.getFloat32(src + 8, true);
  }
  return positions;
}

function readUVs(bin, baseOffset, vertexCount, stride, attributeOffset) {
  const view = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);
  const uvs = new Float32Array(vertexCount * 2);
  for (let i = 0; i < vertexCount; i += 1) {
    const src = baseOffset + i * stride + attributeOffset;
    uvs[i * 2] = Math.max(-1, view.getInt16(src, true) / 32767);
    uvs[i * 2 + 1] = Math.max(-1, view.getInt16(src + 2, true) / 32767);
  }
  return uvs;
}

function readNormals(bin, baseOffset, vertexCount, stride, attributeOffset) {
  const normals = new Float32Array(vertexCount * 3);
  const vector = new THREE.Vector3();
  for (let i = 0; i < vertexCount; i += 1) {
    const src = baseOffset + i * stride + attributeOffset;
    vector.set(
      snorm8(bin[src]),
      snorm8(bin[src + 1]),
      snorm8(bin[src + 2])
    ).normalize();
    normals[i * 3] = vector.x;
    normals[i * 3 + 1] = vector.y;
    normals[i * 3 + 2] = vector.z;
  }
  return normals;
}

function snorm8(value) {
  const signed = value > 127 ? value - 256 : value;
  return Math.max(-1, signed / 127);
}

function readU8x4(bin, baseOffset, vertexCount, stride, attributeOffset) {
  const values = new Uint8Array(vertexCount * 4);
  for (let i = 0; i < vertexCount; i += 1) {
    const src = baseOffset + i * stride + attributeOffset;
    values[i * 4] = bin[src];
    values[i * 4 + 1] = bin[src + 1];
    values[i * 4 + 2] = bin[src + 2];
    values[i * 4 + 3] = bin[src + 3];
  }
  return values;
}

function readNormalizedWeightVectors(bin, baseOffset, vertexCount, stride, attributeOffset) {
  const view = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);
  const weights = new Float32Array(vertexCount * 4);
  const stats = {
    vertexCount,
    normalizedWeightVector: vertexCount,
    invalidNormalizedWeightVector: 0
  };

  for (let vertex = 0; vertex < vertexCount; vertex += 1) {
    const src = baseOffset + vertex * stride + attributeOffset;
    const packed = view.getUint32(src, true);
    const offset = vertex * 4;
    if (!decodeNormalizedWeightVector(packed, weights, offset)) stats.invalidNormalizedWeightVector += 1;
  }

  return { weights, stats };
}

function repairBoneWeights(indices, weights, hasSkinAttributes, hasImplicitFirstWeight) {
  const stats = {
    vertexCount: weights.length / 4,
    implicitFirstWeight: 0,
    invalidImplicitFirstWeight: 0,
    zeroWeightFallback: 0
  };
  if (!hasSkinAttributes) return stats;
  for (let vertex = 0; vertex < weights.length / 4; vertex += 1) {
    const offset = vertex * 4;
    if (hasImplicitFirstWeight) {
      const implicitWeight = 255 - weights[offset + 1] - weights[offset + 2] - weights[offset + 3];
      if (implicitWeight >= 0) {
        weights[offset] = implicitWeight;
        stats.implicitFirstWeight += 1;
        continue;
      }
      stats.invalidImplicitFirstWeight += 1;
    }

    const hasWeight = weights[offset] || weights[offset + 1] || weights[offset + 2] || weights[offset + 3];
    if (hasWeight) continue;

    const slot = indices[offset] || indices[offset + 1] || indices[offset + 2] || indices[offset + 3] ? firstNonZeroIndexSlot(indices, offset) : 0;
    weights[offset + slot] = 255;
    stats.zeroWeightFallback += 1;
  }
  return stats;
}

function firstNonZeroIndexSlot(indices, offset) {
  for (let slot = 0; slot < 4; slot += 1) {
    if (indices[offset + slot]) return slot;
  }
  return 0;
}

function readInverseBindMatrices(bin, accessors, bufferViews, maxJoints, accessorIndex) {
  if (!maxJoints) return [];
  if (Number.isInteger(accessorIndex) && accessors[accessorIndex]) {
    const accessor = accessors[accessorIndex];
    const bufferView = bufferViews[accessor.bufferView];
    if (bufferView) return readMatrixAccessor(bin, accessor, bufferView, maxJoints);
  }

  const candidates = accessors
    .map((accessor) => ({ accessor: accessor, bufferView: bufferViews[accessor.bufferView] }))
    .filter((candidate) => {
      const accessor = candidate.accessor;
      const bufferView = candidate.bufferView;
      if (!bufferView || accessor.count < 4 || accessor.count > maxJoints) return false;
      const needed = accessor.byteOffset + accessor.count * 64;
      return bufferView.byteLength >= needed && bufferView.byteLength <= needed + 16;
    })
    .sort((a, b) => b.accessor.count - a.accessor.count);
  if (!candidates.length) return [];

  return readMatrixAccessor(bin, candidates[0].accessor, candidates[0].bufferView, maxJoints);
}

function readMatrixAccessor(bin, accessor, bufferView, maxJoints) {
  const view = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);
  const offset = bufferView.byteOffset + accessor.byteOffset;
  const matrices = [];
  const count = Math.min(accessor.count, maxJoints);
  for (let i = 0; i < count; i += 1) {
    const values = [];
    for (let element = 0; element < 16; element += 1) {
      values.push(view.getFloat32(offset + i * 64 + element * 4, true));
    }
    matrices.push(new THREE.Matrix4().fromArray(values));
  }
  return matrices;
}

function countWeightedVertices(weights) {
  let count = 0;
  for (let i = 0; i < weights.length; i += 4) {
    if (weights[i] || weights[i + 1] || weights[i + 2] || weights[i + 3]) count += 1;
  }
  return count;
}

function localMatrix(node) {
  return new THREE.Matrix4().compose(
    new THREE.Vector3(node.translation[0], node.translation[1], node.translation[2]),
    new THREE.Quaternion(node.rotation[0], node.rotation[1], node.rotation[2], node.rotation[3]),
    new THREE.Vector3(node.scale[0], node.scale[1], node.scale[2])
  );
}

function computeGlobalMatrices(nodes) {
  const globals = new Array(nodes.length);
  const resolving = new Set();

  const resolve = (index) => {
    if (globals[index]) return globals[index];
    if (resolving.has(index)) return localMatrix(nodes[index]);
    resolving.add(index);

    const node = nodes[index];
    const matrix = localMatrix(node);
    if (Number.isInteger(node.parent) && node.parent >= 0 && node.parent < nodes.length && node.parent !== index) {
      matrix.premultiply(resolve(node.parent));
    }

    resolving.delete(index);
    globals[index] = matrix;
    return matrix;
  };

  for (let i = 0; i < nodes.length; i += 1) resolve(i);
  return globals;
}

function globalMatricesByName(nodes, globals) {
  globals = globals || computeGlobalMatrices(nodes);
  const byName = new Map();
  for (const node of nodes) {
    if (node.name) byName.set(node.name, globals[node.index]);
  }
  return byName;
}

function nodesByName(nodes) {
  const byName = new Map();
  for (const node of nodes) {
    if (node.name && !byName.has(node.name)) byName.set(node.name, node);
  }
  return byName;
}

function decodeAnimation(animation, bin, accessors, bufferViews, nodes) {
  const packed = animation && animation.packed;
  if (!packed || !packed.nodes || !Number.isInteger(packed.dataAccessor)) return null;
  if (Number.isInteger(packed.uintAccessor)) {
    return decodeContinuousAnimation(animation, packed, bin, accessors, bufferViews, nodes);
  }
  const dataAccessor = accessors[packed.dataAccessor];
  const dataBufferView = dataAccessor ? bufferViews[dataAccessor.bufferView] : null;
  if (!dataAccessor || !dataBufferView) return null;

  const frameCount = Math.max(1, Math.round(animation.keyframeCount || animation.lastFrame || 1));
  const frameRate = animation.frameRate || 30;
  const view = new DataView(bin.buffer, bin.byteOffset + dataBufferView.byteOffset + dataAccessor.byteOffset, dataBufferView.byteLength - dataAccessor.byteOffset);
  const nodeMeta = readAnimationNodeMeta(bin, accessors, bufferViews, packed.nodeAccessor, packed.nodes.length);
  const nodesByChannelName = new Map();
  let wordOffset = 0;
  let decodedNodeCount = 0;
  let skippedSparseCount = 0;

  for (let packedIndex = 0; packedIndex < packed.nodes.length; packedIndex += 1) {
    const packedNode = packed.nodes[packedIndex];
    const node = nodes[packedNode.nodeIndex];
    const flags = packedNode.flags || 0;
    const rotationWords = flags & 2 ? 4 : 0;
    const translationWords = flags & 4 ? 3 : 0;
    const scaleWords = (flags & 8 ? 1 : 0) + (flags & 16 ? 2 : 0);
    const frameStride = rotationWords + translationWords + scaleWords;
    const availableSamples = frameStride ? Math.floor(Math.max(0, (packedNode.dataSize || 0) - 1) / frameStride) : 0;
    const headerWord = frameStride ? readI16Word(view, wordOffset) : 0;
    const fixedSampleCount = fixedPackedSampleCount(packedNode.dataSize || 0, headerWord, frameStride);
    const sampleCount = fixedSampleCount || availableSamples;

    if (node && node.name && frameStride && sampleCount > 0) {
      const samples = [];
      const meta = nodeMeta[packedIndex] || null;
      let cursor = wordOffset + 1;
      const end = wordOffset + (packedNode.dataSize || 0);
      const frameState = { frame: 0, runLength: fixedSampleCount ? 0 : Math.max(headerWord, 0) };
      while (cursor + frameStride <= end && (!fixedSampleCount || samples.length < fixedSampleCount)) {
        if (!fixedSampleCount) {
          const originalCursor = cursor;
          const previousSample = samples[samples.length - 1] || {
            rotation: node.rotation && node.rotation.length ? node.rotation : null,
            translation: node.translation && node.translation.length ? node.translation : null,
            restReference: true
          };
          const alignment = alignPackedSample(view, cursor, end, rotationWords, translationWords, scaleWords, previousSample, meta, {
            forceImmediate: frameState.runLength > 0
          });
          if (!alignment || alignment.missing) break;
          cursor = alignment.cursor;
          applyPackedControlWords(view, originalCursor, cursor - originalCursor, frameState);
        }
        if (cursor + frameStride > end) break;
        let rotation = null;
        let translation = null;
        let scale = null;
        const sampleFrame = fixedSampleCount ? null : frameState.frame;
        if (rotationWords) {
          rotation = normalizeQuaternion([
            readI16Word(view, cursor) / 32767,
            readI16Word(view, cursor + 1) / 32767,
            readI16Word(view, cursor + 2) / 32767,
            readI16Word(view, cursor + 3) / 32767
          ]);
          cursor += 4;
        }
        if (translationWords) {
          translation = decodeAnimationTranslation(meta, [
            readI16Word(view, cursor),
            readI16Word(view, cursor + 1),
            readI16Word(view, cursor + 2)
          ]);
          cursor += 3;
        }
        if (scaleWords) {
          const rawScale = [];
          for (let i = 0; i < scaleWords; i += 1) rawScale.push(readI16Word(view, cursor + i));
          scale = decodeAnimationScale(meta, rawScale);
          cursor += scaleWords;
        }
        samples.push({ rotation, translation, scale, frame: sampleFrame });
        if (!fixedSampleCount) {
          frameState.frame += 1;
          if (frameState.runLength > 0) frameState.runLength -= 1;
        }
      }
      nodesByChannelName.set(node.name, { samples, sourceNodeIndex: packedNode.nodeIndex });
      decodedNodeCount += 1;
    } else if (frameStride && packedNode.dataSize) {
      skippedSparseCount += 1;
    }
    wordOffset += packedNode.dataSize || 0;
  }

  return {
    frameRate,
    frameCount,
    duration: frameCount / frameRate,
    nodesByName: nodesByChannelName,
    packedNodeCount: packed.nodes.length,
    decodedNodeCount,
    skippedSparseCount
  };
}

function decodeContinuousAnimation(animation, packed, bin, accessors, bufferViews, nodes) {
  if (!Number.isInteger(packed.nodeAccessor)) throw new Error("continuous packed animation is missing its base transform accessor");
  const frameCount = Math.max(1, Math.round(animation.keyframeCount || animation.lastFrame || 1));
  const frameRate = animation.frameRate || 30;
  const dataWords = readAnimationAccessor(bin, accessors, bufferViews, packed.dataAccessor, "i16");
  const nodeBaseData = readAnimationAccessor(bin, accessors, bufferViews, packed.nodeAccessor, "f32");
  const baseRotationWords = readAnimationAccessor(bin, accessors, bufferViews, packed.uintAccessor, "i16");
  const bases = readContinuousPackedBases(packed.nodes.length, nodeBaseData, baseRotationWords);
  const decoded = decodeContinuousPackedAnimationData(packed.nodes, dataWords, bases, frameCount);
  const nodesByChannelName = new Map();
  let skippedSparseCount = 0;

  for (let channelIndex = 0; channelIndex < decoded.channels.length; channelIndex += 1) {
    const channel = decoded.channels[channelIndex];
    const node = nodes[channel.nodeIndex];
    if (!node || !node.name) {
      skippedSparseCount += 1;
      continue;
    }
    nodesByChannelName.set(node.name, {
      samples: channel.samples,
      base: bases[channelIndex],
      sourceNodeIndex: channel.nodeIndex
    });
  }

  return {
    frameRate,
    frameCount,
    duration: frameCount / frameRate,
    nodesByName: nodesByChannelName,
    packedNodeCount: packed.nodes.length,
    decodedNodeCount: nodesByChannelName.size,
    skippedSparseCount,
    packedFormat: "continuous",
    packedStats: decoded.stats
  };
}

function readAnimationAccessor(bin, accessors, bufferViews, accessorIndex, kind) {
  const accessor = accessors[accessorIndex];
  const bufferView = accessor ? bufferViews[accessor.bufferView] : null;
  if (!accessor || !bufferView) throw new Error(`missing packed animation accessor ${accessorIndex}`);
  const itemSize = kind === "f32" ? 4 : 2;
  const count = accessor.count || 0;
  const byteOffset = bufferView.byteOffset + accessor.byteOffset;
  const byteLength = count * itemSize;
  if (byteOffset < 0 || byteOffset + byteLength > bin.byteLength || byteLength > bufferView.byteLength - accessor.byteOffset) {
    throw new Error(`packed animation accessor ${accessorIndex} is truncated`);
  }
  const view = new DataView(bin.buffer, bin.byteOffset + byteOffset, byteLength);
  const values = kind === "f32" ? new Float32Array(count) : new Int16Array(count);
  for (let index = 0; index < count; index += 1) {
    values[index] = kind === "f32" ? view.getFloat32(index * 4, true) : view.getInt16(index * 2, true);
  }
  return values;
}

function applyPackedControlWords(view, cursor, wordCount, frameState) {
  const end = cursor + wordCount;
  while (cursor + 1 < end) {
    const first = readI16Word(view, cursor);
    const second = readI16Word(view, cursor + 1);
    if (first < 0 && second > 0 && Math.abs(first) <= 2048 && second <= 2048) {
      frameState.frame += -first;
      frameState.runLength = second;
      cursor += 2;
      continue;
    }
    cursor += 1;
  }
}

function fixedPackedSampleCount(dataSize, headerWord, frameStride) {
  if (!frameStride || headerWord <= 0) return 0;
  const expectedWords = 1 + headerWord * frameStride;
  const trailingWords = dataSize - expectedWords;
  return trailingWords >= 0 && trailingWords < frameStride ? headerWord : 0;
}

function readAnimationNodeMeta(bin, accessors, bufferViews, accessorIndex, channelCount) {
  if (!Number.isInteger(accessorIndex) || !accessors[accessorIndex]) return [];
  const accessor = accessors[accessorIndex];
  const bufferView = bufferViews[accessor.bufferView];
  if (!bufferView) return [];
  const view = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);
  const base = bufferView.byteOffset + accessor.byteOffset;
  const count = Math.min(channelCount, Math.floor(accessor.count / 8));
  const metas = [];
  for (let i = 0; i < count; i += 1) {
    const offset = base + i * 8 * 4;
    metas.push({
      translation: [
        view.getFloat32(offset, true),
        view.getFloat32(offset + 4, true),
        view.getFloat32(offset + 8, true)
      ],
      scale: [
        view.getFloat32(offset + 12, true),
        view.getFloat32(offset + 16, true),
        view.getFloat32(offset + 20, true)
      ],
      translationStep: view.getFloat32(offset + 24, true),
      scaleStep: view.getFloat32(offset + 28, true)
    });
  }
  return metas;
}

function decodeAnimationTranslation(meta, raw) {
  if (!meta) return null;
  const step = Number.isFinite(meta.translationStep) ? meta.translationStep : 1 / 32767;
  return [
    meta.translation[0] + raw[0] * step,
    meta.translation[1] + raw[1] * step,
    meta.translation[2] + raw[2] * step
  ];
}

function decodeAnimationScale(meta, raw) {
  if (!meta || !raw.length) return null;
  const step = Number.isFinite(meta.scaleStep) ? meta.scaleStep : 1 / 32767;
  if (raw.length === 1) {
    const value = meta.scale[0] + raw[0] * step;
    return [value, value, value];
  }
  return [
    meta.scale[0] + raw[0] * step,
    meta.scale[1] + (raw[1] || 0) * step,
    meta.scale[2] + (raw[2] || 0) * step
  ];
}

function alignPackedSample(view, cursor, end, rotationWords, translationWords, auxWords, previousSample, meta, options = {}) {
  const stride = rotationWords + translationWords + auxWords;
  const immediate = packedSampleCandidate(view, cursor, rotationWords, translationWords, meta);
  if (options.forceImmediate && immediate) {
    return { cursor, score: scorePackedSample(immediate, 0, previousSample), skip: 0 };
  }
  const controlSkip = packedControlWordSkip(view, cursor, end, stride);
  if (immediate && controlSkip) {
    const skipped = packedSampleCandidate(view, cursor + controlSkip, rotationWords, translationWords, meta);
    if (skipped) {
      const immediateScore = scorePackedSample(immediate, 0, previousSample);
      const skippedScore = scorePackedSample(skipped, controlSkip, previousSample);
      if (skippedScore + 0.5 < immediateScore) return { cursor: cursor + controlSkip, score: skippedScore, skip: controlSkip };
      return { cursor, score: immediateScore, skip: 0 };
    }
  }
  if (immediate) return { cursor, score: scorePackedSample(immediate, 0, previousSample), skip: 0 };

  const maxSkip = Math.min(96, Math.max(0, end - cursor - stride));
  for (let skip = controlSkip || 1; skip <= maxSkip; skip += 1) {
    if (controlSkip && skip < controlSkip) continue;
    const candidate = cursor + skip;
    if (candidate + stride > end) break;
    const sample = packedSampleCandidate(view, candidate, rotationWords, translationWords, meta);
    if (!sample) continue;
    return { cursor: candidate, score: scorePackedSample(sample, skip, previousSample), skip };
  }
  return { cursor, score: Infinity, skip: 0, missing: true };
}

function packedControlWordSkip(view, cursor, end, stride) {
  if (cursor + 2 + stride > end) return 0;
  const first = readI16Word(view, cursor);
  const second = readI16Word(view, cursor + 1);
  return first < 0 && second > 0 && Math.abs(first) <= 2048 && second <= 2048 ? 2 : 0;
}

function isPlausiblePackedSample(view, cursor, rotationWords, translationWords) {
  return Boolean(packedSampleCandidate(view, cursor, rotationWords, translationWords, null));
}

function packedSampleCandidate(view, cursor, rotationWords, translationWords, meta) {
  let rotation = null;
  let rotationLength = 0;
  let nearZeroRotationWords = 0;
  if (rotationWords) {
    const raw = [
      readI16Word(view, cursor),
      readI16Word(view, cursor + 1),
      readI16Word(view, cursor + 2),
      readI16Word(view, cursor + 3)
    ];
    rotationLength = Math.hypot(raw[0], raw[1], raw[2], raw[3]);
    if (rotationLength < 31500 || rotationLength > 34000) return null;
    nearZeroRotationWords = raw.filter((value) => Math.abs(value) < 32).length;
    rotation = normalizeQuaternion(raw.map((value) => value / 32767));
  }
  let translation = null;
  if (translationWords) {
    const base = cursor + rotationWords;
    const raw = [
      readI16Word(view, base),
      readI16Word(view, base + 1),
      readI16Word(view, base + 2)
    ];
    translation = decodeAnimationTranslation(meta, raw);
  }
  return {
    rotation,
    translation,
    rotationLength,
    nearZeroRotationWords,
    normError: rotationWords ? Math.abs(rotationLength - 32767) / 32767 : 0
  };
}

function scorePackedSample(sample, skip, previousSample) {
  let score = skip * 0.2 + sample.normError * 4;
  if (sample.nearZeroRotationWords >= 2) score += 0.15;
  if (previousSample && previousSample.rotation && sample.rotation) {
    const angle = quaternionAngle(previousSample.rotation, sample.rotation);
    if (previousSample.restReference) {
      score += angle / 120;
      if (angle > 150) score += 1.5;
    } else {
      score += angle / 24;
      if (angle > 100) score += 4;
      if (angle > 150) score += 8;
      if (angle > 100 && sample.nearZeroRotationWords >= 2) score += 3;
    }
  }
  if (previousSample && previousSample.translation && sample.translation) {
    const dist = Math.hypot(
      previousSample.translation[0] - sample.translation[0],
      previousSample.translation[1] - sample.translation[1],
      previousSample.translation[2] - sample.translation[2]
    );
    score += Math.min(dist, 50) / (previousSample.restReference ? 20 : 5);
    if (!previousSample.restReference && dist > 12) score += 2;
  }
  return score;
}

function quaternionAngle(a, b) {
  const dot = Math.abs(Math.max(-1, Math.min(1, a[0] * b[0] + a[1] * b[1] + a[2] * b[2] + a[3] * b[3])));
  return 2 * Math.acos(dot) * 180 / Math.PI;
}

function readI16Word(view, wordOffset) {
  return view.getInt16(wordOffset * 2, true);
}

function normalizeQuaternion(values) {
  const length = Math.hypot(values[0], values[1], values[2], values[3]) || 1;
  return [values[0] / length, values[1] / length, values[2] / length, values[3] / length];
}

function vertexStreamForMesh(decoded, meshInfo) {
  if (!decoded.streams || !decoded.streams.length) return decoded;
  const index = meshInfo && Number.isInteger(meshInfo.streamIndex) ? meshInfo.streamIndex : 0;
  return decoded.streams[index] || decoded.streams[0];
}

function posePositions(decoded, pose, frameIndex, meshInfo) {
  const stream = vertexStreamForMesh(decoded, meshInfo);
  if (!pose || !stream.boneIndices || !stream.boneIndices.length || !stream.boneWeights || !stream.boneWeights.length) return stream.positions;
  const skin = skinForMesh(decoded, meshInfo);
  if (!skin || !skin.jointNodes || !skin.jointNodes.length) return stream.positions;

  const posed = new Float32Array(stream.positions.length);
  const poseGlobals = retargetedPoseGlobals(decoded, pose, frameIndex);
  const source = new THREE.Vector3();
  const target = new THREE.Vector3();
  const accum = new THREE.Vector3();
  const skinMatrix = new THREE.Matrix4();
  const fallbackInverse = new THREE.Matrix4();
  const meshMatrix = meshNodePoseDelta(decoded, poseGlobals, meshInfo && meshInfo.nodeIndex);

  for (let vertex = 0; vertex < stream.vertexCount; vertex += 1) {
    const posOffset = vertex * 3;
    source.fromArray(stream.positions, posOffset);
    accum.set(0, 0, 0);

    const influenceOffset = vertex * 4;
    const weightSum =
      stream.boneWeights[influenceOffset] +
      stream.boneWeights[influenceOffset + 1] +
      stream.boneWeights[influenceOffset + 2] +
      stream.boneWeights[influenceOffset + 3];

    if (weightSum === 0) {
      target.copy(source);
      if (meshMatrix) target.applyMatrix4(meshMatrix);
      posed[posOffset] = target.x;
      posed[posOffset + 1] = target.y;
      posed[posOffset + 2] = target.z;
      continue;
    }

    let appliedWeight = 0;
    for (let slot = 0; slot < 4; slot += 1) {
      const rawWeight = stream.boneWeights[influenceOffset + slot];
      if (!rawWeight) continue;

      const jointIndex = stream.boneIndices[influenceOffset + slot];
      const joint = skin.jointNodes[jointIndex];
      if (!joint) continue;

      const poseGlobal = poseGlobals[joint.index] || decoded.globals[joint.index];
      const inverseBind =
        skin.inverseBindMatrices[jointIndex] ||
        fallbackInverse.copy(decoded.globals[joint.index]).invert();

      skinMatrix.multiplyMatrices(poseGlobal, inverseBind);
      target.copy(source).applyMatrix4(skinMatrix).multiplyScalar(rawWeight / weightSum);
      accum.add(target);
      appliedWeight += rawWeight;
    }

    if (appliedWeight === 0) {
      accum.copy(source);
    } else if (appliedWeight < weightSum) {
      accum.addScaledVector(source, (weightSum - appliedWeight) / weightSum);
    }

    posed[posOffset] = accum.x;
    posed[posOffset + 1] = accum.y;
    posed[posOffset + 2] = accum.z;
  }

  return posed;
}

function poseNormals(decoded, pose, frameIndex, meshInfo) {
  const stream = vertexStreamForMesh(decoded, meshInfo);
  if (!stream.normals || !stream.normals.length) return null;
  if (!pose || !stream.boneIndices || !stream.boneIndices.length || !stream.boneWeights || !stream.boneWeights.length) return stream.normals;
  const skin = skinForMesh(decoded, meshInfo);
  if (!skin || !skin.jointNodes || !skin.jointNodes.length) return stream.normals;

  const posed = new Float32Array(stream.normals.length);
  const poseGlobals = retargetedPoseGlobals(decoded, pose, frameIndex);
  const source = new THREE.Vector3();
  const target = new THREE.Vector3();
  const accum = new THREE.Vector3();
  const skinMatrix = new THREE.Matrix4();
  const normalMatrix = new THREE.Matrix3();
  const fallbackInverse = new THREE.Matrix4();
  const meshMatrix = meshNodePoseDelta(decoded, poseGlobals, meshInfo && meshInfo.nodeIndex);
  const meshNormalMatrix = meshMatrix ? new THREE.Matrix3().getNormalMatrix(meshMatrix) : null;

  for (let vertex = 0; vertex < stream.vertexCount; vertex += 1) {
    const normalOffset = vertex * 3;
    source.fromArray(stream.normals, normalOffset);
    accum.set(0, 0, 0);

    const influenceOffset = vertex * 4;
    const weightSum =
      stream.boneWeights[influenceOffset] +
      stream.boneWeights[influenceOffset + 1] +
      stream.boneWeights[influenceOffset + 2] +
      stream.boneWeights[influenceOffset + 3];

    if (weightSum === 0) {
      target.copy(source);
      if (meshNormalMatrix) target.applyMatrix3(meshNormalMatrix);
      target.normalize();
      posed[normalOffset] = target.x;
      posed[normalOffset + 1] = target.y;
      posed[normalOffset + 2] = target.z;
      continue;
    }

    let appliedWeight = 0;
    for (let slot = 0; slot < 4; slot += 1) {
      const rawWeight = stream.boneWeights[influenceOffset + slot];
      if (!rawWeight) continue;

      const jointIndex = stream.boneIndices[influenceOffset + slot];
      const joint = skin.jointNodes[jointIndex];
      if (!joint) continue;

      const poseGlobal = poseGlobals[joint.index] || decoded.globals[joint.index];
      const inverseBind =
        skin.inverseBindMatrices[jointIndex] ||
        fallbackInverse.copy(decoded.globals[joint.index]).invert();

      skinMatrix.multiplyMatrices(poseGlobal, inverseBind);
      normalMatrix.getNormalMatrix(skinMatrix);
      target.copy(source).applyMatrix3(normalMatrix).multiplyScalar(rawWeight / weightSum);
      accum.add(target);
      appliedWeight += rawWeight;
    }

    if (appliedWeight === 0) {
      accum.copy(source);
    } else if (appliedWeight < weightSum) {
      accum.addScaledVector(source, (weightSum - appliedWeight) / weightSum);
    }

    accum.normalize();
    posed[normalOffset] = accum.x;
    posed[normalOffset + 1] = accum.y;
    posed[normalOffset + 2] = accum.z;
  }

  return posed;
}

function shouldUseSmoothShading(meshInfo, pose) {
  if (!state.smoothShading) return false;
  return Boolean(meshInfo || pose);
}

function skinForMesh(decoded, meshInfo) {
  const skinIndex = meshInfo && Number.isInteger(meshInfo.skinIndex) ? meshInfo.skinIndex : null;
  if (Number.isInteger(skinIndex) && decoded.skins && decoded.skins[skinIndex]) return decoded.skins[skinIndex];
  return decoded.defaultSkin || { jointNodes: decoded.jointNodes || [], inverseBindMatrices: decoded.inverseBindMatrices || [] };
}

function meshNodePoseDelta(decoded, poseGlobals, meshNodeIndex) {
  if (!Number.isInteger(meshNodeIndex) || !poseGlobals[meshNodeIndex] || !decoded.globals[meshNodeIndex]) return null;
  return new THREE.Matrix4().multiplyMatrices(
    poseGlobals[meshNodeIndex],
    new THREE.Matrix4().copy(decoded.globals[meshNodeIndex]).invert()
  );
}

function retargetedPoseGlobals(decoded, pose, frameIndex) {
  const frameKey = Number.isInteger(frameIndex) ? frameIndex : -1;
  const cacheKey = `${decoded.sourcePath || ""}:pose-hierarchy:${frameKey}`;
  if (!pose.retargetedFrameCache) pose.retargetedFrameCache = new Map();
  if (pose.retargetedFrameCache.has(cacheKey)) return pose.retargetedFrameCache.get(cacheKey);

  const useAnimationLocals = pose.animation && pose.animation.packedFormat === "continuous";
  const poseGlobalsByName = useAnimationLocals ? null : poseHierarchyGlobalsByName(pose, frameKey);
  let animationGlobals = null;
  const globals = new Array(decoded.nodes.length);
  const resolving = new Set();

  const resolve = (index) => {
    if (globals[index]) return globals[index];
    if (resolving.has(index)) return decoded.globals[index] ? decoded.globals[index].clone() : localMatrix(decoded.nodes[index]);
    resolving.add(index);

    const node = decoded.nodes[index];
    const poseGlobal = !useAnimationLocals && node.name ? poseGlobalsByName.get(node.name) : null;
    const hierarchyGlobal = useAnimationLocals
      ? mismatchedAnimationGlobal(node, decoded, pose, frameKey, () => {
        if (!animationGlobals) animationGlobals = poseHierarchyGlobals(pose, frameKey);
        return animationGlobals;
      })
      : null;
    const matrix = hierarchyGlobal
      ? hierarchyGlobal.clone()
      : useAnimationLocals
        ? animationLocalMatrix(node, pose, frameKey)
        : poseGlobal ? poseGlobal.clone() : localMatrix(node);
    if (!hierarchyGlobal && (useAnimationLocals || !poseGlobal) && Number.isInteger(node.parent) && node.parent >= 0 && node.parent < decoded.nodes.length && node.parent !== index) {
      matrix.premultiply(resolve(node.parent));
    }

    globals[index] = matrix;
    resolving.delete(index);
    return matrix;
  };

  for (let i = 0; i < decoded.nodes.length; i += 1) resolve(i);
  pose.retargetedFrameCache.set(cacheKey, globals);
  return globals;
}

function mismatchedAnimationGlobal(modelNode, decoded, pose, frameIndex, animationGlobals) {
  const channel = animationChannelForModelNode(modelNode, pose);
  if (!channel || !Number.isInteger(channel.sourceNodeIndex)) return null;
  const sourceNode = pose.nodes[channel.sourceNodeIndex];
  if (!sourceNode) return null;
  const modelParent = Number.isInteger(modelNode.parent) ? decoded.nodes[modelNode.parent] : null;
  const sourceParent = Number.isInteger(sourceNode.parent) ? pose.nodes[sourceNode.parent] : null;
  if (!requiresAnimationGlobalRemap(modelParent && modelParent.name, sourceParent && sourceParent.name)) return null;
  const animatedGlobal = animationGlobals()[sourceNode.index];
  const modelRestGlobal = decoded.globals[modelNode.index];
  if (!animatedGlobal || !modelRestGlobal) return null;

  // The animation hierarchy is authoritative for the attachment's world-space
  // position and rotation. Its root may use a different unit scale (Dark Days'
  // gun animates between 0.01 and 1), so the geometry bind scale stays authoritative.
  const position = new THREE.Vector3();
  const rotation = new THREE.Quaternion();
  const modelRestScale = new THREE.Vector3();
  animatedGlobal.decompose(position, rotation, new THREE.Vector3());
  modelRestGlobal.decompose(new THREE.Vector3(), new THREE.Quaternion(), modelRestScale);
  return new THREE.Matrix4().compose(position, rotation, modelRestScale);
}

function animationChannelForModelNode(modelNode, pose) {
  if (!modelNode.name || !pose.animation) return null;
  return pose.animation.nodesByName.get(resolveAnimationSourceName(modelNode.name));
}

function animationLocalMatrix(modelNode, pose, frameIndex) {
  const channel = animationChannelForModelNode(modelNode, pose);
  if (!channel) return localMatrix(modelNode);
  const sample = sampleAnimationChannel(channel, pose.animation, frameIndex);
  const modelRest = {
    translation: modelNode.translation || [0, 0, 0],
    rotation: modelNode.rotation || [0, 0, 0, 1],
    scale: modelNode.scale || [1, 1, 1]
  };
  const sourceNode = Number.isInteger(channel.sourceNodeIndex) ? pose.nodes[channel.sourceNodeIndex] : null;
  const transform = sourceNode && sourceNode.name !== modelNode.name
    ? resolveAliasedRotationTransform(modelRest, {
      translation: sourceNode.translation || [0, 0, 0],
      rotation: sourceNode.rotation || [0, 0, 0, 1],
      scale: sourceNode.scale || [1, 1, 1]
    }, sample)
    : resolveAnimationLocalTransform(modelRest, sample);
  return new THREE.Matrix4().compose(
    new THREE.Vector3().fromArray(transform.translation),
    new THREE.Quaternion().fromArray(transform.rotation),
    new THREE.Vector3().fromArray(transform.scale)
  );
}

function poseHierarchyGlobalsByName(pose, frameIndex) {
  const globals = poseHierarchyGlobals(pose, frameIndex);
  const byName = new Map();
  for (const node of pose.nodes) {
    if (node.name && !byName.has(node.name)) byName.set(node.name, globals[node.index]);
  }
  return byName;
}

function poseDeltaGlobalsByName(decoded, pose, frameIndex) {
  if (!pose || !pose.nodes || !pose.nodes.length) return null;
  const poseRestGlobals = pose.restGlobals || (pose.restGlobals = computeGlobalMatrices(pose.nodes));
  const poseAnimatedGlobals = poseHierarchyGlobals(pose, frameIndex);
  const byName = new Map();
  const inverseRest = new THREE.Matrix4();

  for (const node of decoded.nodes) {
    const poseNode = node.name && pose.nodesByName ? pose.nodesByName.get(node.name) : null;
    if (!poseNode || !decoded.globals[node.index] || !poseRestGlobals[poseNode.index] || !poseAnimatedGlobals[poseNode.index]) continue;
    inverseRest.copy(poseRestGlobals[poseNode.index]).invert();
    byName.set(
      node.name,
      new THREE.Matrix4()
        .multiplyMatrices(poseAnimatedGlobals[poseNode.index], inverseRest)
        .multiply(decoded.globals[node.index])
    );
  }

  return byName;
}

function poseHierarchyGlobals(pose, frameIndex) {
  const globals = new Array(pose.nodes.length);
  const resolving = new Set();

  const resolve = (index) => {
    if (globals[index]) return globals[index];
    if (resolving.has(index)) return localMatrix(pose.nodes[index]);
    resolving.add(index);

    const node = pose.nodes[index];
    const matrix = poseLocalMatrix(node, pose, frameIndex);
    if (Number.isInteger(node.parent) && node.parent >= 0 && node.parent < pose.nodes.length && node.parent !== index) {
      matrix.premultiply(resolve(node.parent));
    }

    globals[index] = matrix;
    resolving.delete(index);
    return matrix;
  };

  for (let i = 0; i < pose.nodes.length; i += 1) resolve(i);
  return globals;
}

function poseLocalMatrix(source, pose, frameIndex) {
  let rotation = source.rotation;
  let translation = source.translation;
  let scale = source.scale;
  const channel = source.name && pose.animation && pose.animation.nodesByName.get(source.name);
  const channelTargetsSource = channel && (
    !Number.isInteger(channel.sourceNodeIndex) || channel.sourceNodeIndex === source.index
  );
  if (channelTargetsSource && frameIndex >= 0) {
    const sample = sampleAnimationChannel(channel, pose.animation, frameIndex);
    if (sample && sample.rotation) rotation = sample.rotation;
    if (sample && sample.translation) translation = sample.translation;
    if (sample && sample.scale) scale = sample.scale;
  }
  return new THREE.Matrix4().compose(
    new THREE.Vector3(translation[0], translation[1], translation[2]),
    new THREE.Quaternion(rotation[0], rotation[1], rotation[2], rotation[3]),
    new THREE.Vector3(scale[0], scale[1], scale[2])
  );
}

function sampleAnimationChannel(channel, animation, frameIndex) {
  const samples = channel.samples || [];
  if (!samples.length) return null;
  if (samples.length === 1 || animation.frameCount <= 1) return samples[0];

  const wrappedFrame = ((frameIndex % animation.frameCount) + animation.frameCount) % animation.frameCount;
  if (Number.isFinite(samples[0].frame)) {
    const first = samples[0];
    const last = samples[samples.length - 1];
    const firstFrame = Number.isFinite(first.frame) ? first.frame : 0;
    const lastFrame = Number.isFinite(last.frame) ? last.frame : firstFrame;
    if (wrappedFrame <= firstFrame) return first;

    let low = 0;
    let high = samples.length;
    while (low < high) {
      const mid = (low + high) >> 1;
      if ((samples[mid].frame || 0) <= wrappedFrame) low = mid + 1;
      else high = mid;
    }
    const leftIndex = Math.max(0, low - 1);
    const rightIndex = low < samples.length ? low : 0;
    const left = samples[leftIndex];
    const right = samples[rightIndex];
    if (!right || left === right) return left;

    const leftFrame = Number.isFinite(left.frame) ? left.frame : 0;
    let rightFrame = Number.isFinite(right.frame) ? right.frame : animation.frameCount;
    let frame = wrappedFrame;
    if (rightIndex === 0) rightFrame += animation.frameCount;
    if (frame < leftFrame) frame += animation.frameCount;
    if (shouldHoldSparseAnimationSample(channel, animation, left, right, leftIndex, rightIndex)) {
      return left;
    }

    const span = Math.max(rightFrame - leftFrame, 1);
    const t = THREE.MathUtils.clamp((frame - leftFrame) / span, 0, 1);
    return interpolateAnimationSamples(left, right, t);
  }

  const samplePosition = wrappedFrame * (samples.length - 1) / Math.max(animation.frameCount - 1, 1);
  const leftIndex = Math.floor(samplePosition);
  const rightIndex = Math.min(samples.length - 1, leftIndex + 1);
  const t = samplePosition - leftIndex;
  const left = samples[leftIndex];
  const right = samples[rightIndex];
  if (!right || t === 0) return left;

  return interpolateAnimationSamples(left, right, t);
}

function shouldHoldSparseAnimationSample(channel, animation, left, right, leftIndex, rightIndex) {
  const samples = channel.samples || [];
  if (samples.length < 2 || samples.length > 4 || animation.frameCount < 2) return false;
  if (!Number.isFinite(left.frame) || !Number.isFinite(right.frame)) return false;
  const last = samples[samples.length - 1];
  const first = samples[0];
  if (!samplesNearlyEqual(first, last)) return false;
  const frameSpan = rightIndex === 0
    ? right.frame + animation.frameCount - left.frame
    : right.frame - left.frame;
  return frameSpan > Math.max(8, animation.frameCount * 0.2) || leftIndex === 0 || rightIndex === 0;
}

function samplesNearlyEqual(a, b) {
  if (!a || !b) return false;
  return vectorNearlyEqual(a.rotation, b.rotation, 1e-4) &&
    vectorNearlyEqual(a.translation, b.translation, 1e-4) &&
    vectorNearlyEqual(a.scale, b.scale, 1e-4);
}

function vectorNearlyEqual(a, b, tolerance) {
  if (!a && !b) return true;
  if (!a || !b || a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (Math.abs(a[i] - b[i]) > tolerance) return false;
  }
  return true;
}

function interpolateAnimationSamples(left, right, t) {
  if (!right || t === 0) return left;
  const out = {};
  if (left.rotation && right.rotation) {
    const q = new THREE.Quaternion(left.rotation[0], left.rotation[1], left.rotation[2], left.rotation[3]);
    q.slerp(new THREE.Quaternion(right.rotation[0], right.rotation[1], right.rotation[2], right.rotation[3]), t);
    out.rotation = [q.x, q.y, q.z, q.w];
  } else {
    out.rotation = left.rotation || right.rotation;
  }
  if (left.translation && right.translation) {
    out.translation = [
      THREE.MathUtils.lerp(left.translation[0], right.translation[0], t),
      THREE.MathUtils.lerp(left.translation[1], right.translation[1], t),
      THREE.MathUtils.lerp(left.translation[2], right.translation[2], t)
    ];
  } else {
    out.translation = left.translation || right.translation;
  }
  if (left.scale && right.scale) {
    out.scale = [
      THREE.MathUtils.lerp(left.scale[0], right.scale[0], t),
      THREE.MathUtils.lerp(left.scale[1], right.scale[1], t),
      THREE.MathUtils.lerp(left.scale[2], right.scale[2], t)
    ];
  } else {
    out.scale = left.scale || right.scale;
  }
  return out;
}

function renderDecoded(decoded, path, pose) {
  pose = pose || null;
  if (state.group) scene.remove(state.group);
  state.group = new THREE.Group();
  state.activePose = pose;
  state.poseGeometryInfos = [];
  state.animationFrame = -1;
  state.animationStartedAt = performance.now();
  state.positionAttribute = null;

  if (!canRenderScene()) {
    updateAnimationControl();
    setStatus(`${renderUnavailableMessage || webGLUnavailableMessage()} Decoded ${path}, but cannot render it.`, true);
    setInspector([
      ["File", path],
      ["Pose", pose ? poseLabel(pose.path) : "Rest"],
      ["Vertices", decoded.vertexCount.toLocaleString()],
      ["Skinned vertices", decoded.weightedCount.toLocaleString()],
      ["Joints", decoded.jointNodes.length],
      ["Meshes", decoded.meshCount],
      ["Primitives", decoded.renderMeshes.length],
      ["Texture", decoded.texturePath || "None"],
      ["Renderer", "WebGL unavailable"]
    ]);
    return;
  }

  for (const meshInfo of decoded.renderMeshes) {
    const stream = vertexStreamForMesh(decoded, meshInfo);
    const useTexture = state.colorMode === "texture" && decoded.texture && stream.uvs && stream.uvs.length === stream.vertexCount * 2;
    const geometry = new THREE.BufferGeometry();
    const positions = posePositions(decoded, pose, pose && pose.animation ? 0 : null, meshInfo);
    const positionAttribute = new THREE.BufferAttribute(positions, 3);
    geometry.setAttribute("position", positionAttribute);
    const smoothNormals = shouldUseSmoothShading(meshInfo, pose);
    const normals = smoothNormals ? null : poseNormals(decoded, pose, pose && pose.animation ? 0 : null, meshInfo);
    const normalAttribute = normals ? new THREE.BufferAttribute(normals, 3) : null;
    if (normalAttribute) geometry.setAttribute("normal", normalAttribute);
    if (useTexture) geometry.setAttribute("uv", new THREE.BufferAttribute(stream.uvs, 2));
    geometry.setIndex(new THREE.BufferAttribute(meshInfo.indices, 1));
    if (!normalAttribute) geometry.computeVertexNormals();
    const material = materialForMesh(decoded, meshInfo, useTexture);
    const mesh = new THREE.Mesh(geometry, material);
    mesh.name = meshInfo.name;
    state.poseGeometryInfos.push({ geometry, meshInfo, positionAttribute, normalAttribute, smoothNormals });
    state.group.add(mesh);
  }

  scene.add(state.group);
  updateAnimationControl();
  frameGroup(state.group, pose && pose.animation ? sampledPoseBounds(decoded, pose) : null);
  setStatus(pose ? `Decoded ${path} with ${poseLabel(pose.path)} ${pose.animation ? "animation" : "pose"}` : `Decoded ${path}`);
  setInspector([
    ["File", path],
    ["Pose", pose ? poseLabel(pose.path) : "Rest"],
    ["Vertices", decoded.vertexCount.toLocaleString()],
    ["Skinned vertices", decoded.weightedCount.toLocaleString()],
    ["Joints", decoded.jointNodes.length],
    ["Bind matrices", decoded.inverseBindMatrices.length],
    ["Meshes", decoded.meshCount],
    ["Primitives", decoded.renderMeshes.length],
    ["Nodes", decoded.nodeCount],
    ["Accessors", decoded.accessorCount],
    ["Materials", decoded.materialCount],
    ["Material view", materialModeLabel()],
    ["Shading", state.smoothShading ? "Smooth normals" : "Authored normals"],
    ["Texture", decoded.texturePath || "None"],
    ["Texture status", decoded.texture ? "Applied" : decoded.textureError || "No matching .sctx"],
    ["Pose joints matched", pose && pose.compatibility ? `${pose.compatibility.matched}/${pose.compatibility.total}` : "Rest"],
    ["Animation", pose && pose.animation ? `${pose.animation.frameCount} frames @ ${pose.animation.frameRate.toFixed(1)} fps` : "None"],
    ["Animated nodes", pose && pose.animation ? `${pose.animation.decodedNodeCount}/${pose.animation.packedNodeCount}` : "0"],
    ["Decoder", "FLA2 + SC_odin_format"],
    ["Animations", "Pose GLB skinning"]
  ]);
  publishDiagnosticSnapshot();
}

function updateAnimation() {
  const pose = state.activePose;
  if (!state.decoded || !pose || !pose.animation || !state.poseGeometryInfos.length || !state.animationPlaying) return;
  const elapsed = (performance.now() - state.animationStartedAt) / 1000;
  const frame = Math.floor(elapsed * pose.animation.frameRate * state.landing.animation.speed) % pose.animation.frameCount;
  applyAnimationFrame(frame);
}

function applyAnimationFrame(frame) {
  const pose = state.activePose;
  if (!state.decoded || !pose || !pose.animation || !state.poseGeometryInfos.length) return false;
  const frameCount = Math.max(1, pose.animation.frameCount || 1);
  frame = ((Math.floor(frame) % frameCount) + frameCount) % frameCount;
  if (frame === state.animationFrame) return true;
  state.animationFrame = frame;
  for (const info of state.poseGeometryInfos) {
    const positions = posePositions(state.decoded, pose, frame, info.meshInfo);
    info.positionAttribute.array.set(positions);
    info.positionAttribute.needsUpdate = true;
    if (info.normalAttribute) {
      const normals = poseNormals(state.decoded, pose, frame, info.meshInfo);
      info.normalAttribute.array.set(normals);
      info.normalAttribute.needsUpdate = true;
    } else if (info.smoothNormals) {
      info.geometry.computeVertexNormals();
    }
    info.geometry.computeBoundingBox();
    info.geometry.computeBoundingSphere();
  }
  return true;
}

function updateAnimationControl() {
  const hasAnimation = Boolean(canRenderScene() && state.activePose && state.activePose.animation);
  const canExportLanding = Boolean(hasAnimation && state.decoded && state.decoded.texture);
  els.playAnimation.disabled = !hasAnimation || state.exportInProgress;
  els.exportWebp.disabled = !hasAnimation || state.exportInProgress;
  els.exportGLB.disabled = !canExportLanding || state.exportInProgress;
  els.exportSkinJSON.disabled = !canExportLanding || state.exportInProgress;
  els.playAnimation.textContent = hasAnimation ? (state.animationPlaying ? "Pause Anim" : "Play Anim") : "No Anim";
  els.exportWebp.textContent = state.exportInProgress ? "Exporting" : "Export WebP";
  els.exportGLB.textContent = state.exportInProgress ? "Exporting" : "Export model.glb";
  els.playAnimation.setAttribute("aria-pressed", String(hasAnimation && state.animationPlaying));
}

function materialForMesh(decoded, meshInfo, useTexture) {
  const materialInfo = decoded.materials && decoded.materials[meshInfo.material] ? decoded.materials[meshInfo.material] : null;
  if (isGlassMaterial(materialInfo)) {
    return new THREE.MeshPhysicalMaterial({
      color: 0x8bdcff,
      roughness: 0.04,
      metalness: 0.0,
      transmission: 0.28,
      thickness: 0.24,
      opacity: 0.36,
      transparent: true,
      depthWrite: false,
      side: THREE.DoubleSide,
      emissive: 0x0b3148,
      emissiveIntensity: 0.2,
      wireframe: state.wireframe
    });
  }

  return new THREE.MeshStandardMaterial({
    color: useTexture ? 0xffffff : meshColor(meshInfo),
    map: useTexture ? decoded.texture : null,
    roughness: 0.72,
    metalness: 0.04,
    // SC3D diffuse alpha is often packed material data, not cutout opacity.
    alphaTest: 0,
    transparent: false,
    depthWrite: true,
    side: THREE.FrontSide,
    wireframe: state.wireframe
  });
}

function isGlassMaterial(materialInfo) {
  const name = materialInfo && materialInfo.name ? materialInfo.name.toLowerCase() : "";
  const shader = materialInfo && materialInfo.shader ? materialInfo.shader.toLowerCase() : "";
  return name.includes("glass") || shader.includes("glass");
}

function materialModeLabel() {
  if (state.colorMode === "texture") return "Texture";
  if (state.colorMode === "materials") return "Materials";
  return "Parts";
}

function meshColor(meshInfo) {
  if (state.colorMode === "materials") {
    return palette[meshInfo.material % palette.length];
  }
  return palette[meshInfo.renderIndex % palette.length];
}

function sampledPoseBounds(decoded, pose) {
  const cacheKey = `${decoded.sourcePath || ""}:${pose.path || ""}:sampled-bounds`;
  if (!pose.boundsCache) pose.boundsCache = new Map();
  if (pose.boundsCache.has(cacheKey)) return pose.boundsCache.get(cacheKey);

  const box = new THREE.Box3();
  const point = new THREE.Vector3();
  const frameCount = Math.max(1, pose.animation.frameCount || 1);
  const step = frameCount <= 360 ? 1 : Math.ceil(frameCount / 180);
  for (let frame = 0; frame < frameCount; frame += step) {
    expandPoseBounds(box, point, decoded, pose, frame);
  }
  if ((frameCount - 1) % step !== 0) {
    expandPoseBounds(box, point, decoded, pose, frameCount - 1);
  }

  pose.boundsCache.set(cacheKey, box);
  return box;
}

function expandPoseBounds(box, point, decoded, pose, frame) {
  const meshBox = new THREE.Box3();
  const meshCenter = new THREE.Vector3();
  const meshSize = new THREE.Vector3();
  for (const meshInfo of decoded.renderMeshes) {
    const positions = posePositions(decoded, pose, frame, meshInfo);
    meshBox.makeEmpty();
    for (let i = 0; i < meshInfo.indices.length; i += 1) {
      const vertex = meshInfo.indices[i] * 3;
      point.set(positions[vertex], positions[vertex + 1], positions[vertex + 2]);
      meshBox.expandByPoint(point);
    }
    if (shouldSkipFrameBoundsMesh(meshBox, meshCenter, meshSize)) continue;
    box.union(meshBox);
  }
}

function shouldSkipFrameBoundsMesh(meshBox, center, size) {
  if (!meshBox || meshBox.isEmpty()) return true;
  meshBox.getCenter(center);
  meshBox.getSize(size);
  const maxSize = Math.max(size.x, size.y, size.z);
  return maxSize < 0.25 && center.length() > 40;
}

function frameGroup(group, bounds) {
  if (!canRenderScene()) return;
  group.position.set(0, 0, 0);
  group.rotation.set(0, 0, 0);
  group.scale.set(1, 1, 1);
  group.traverse((object) => {
    if (!object.geometry) return;
    object.geometry.computeBoundingBox();
    object.geometry.computeBoundingSphere();
  });
  group.updateWorldMatrix(true, true);
  const box = bounds && !bounds.isEmpty() ? bounds.clone() : new THREE.Box3().setFromObject(group);
  if (box.isEmpty()) return;
  const center = box.getCenter(new THREE.Vector3());
  const size = box.getSize(new THREE.Vector3());
  const maxDim = Math.max(size.x, size.y, size.z, 1);
  state.frameInfo = { center, size, maxDim };
  applyLandingPreview(true);
}

async function loadAsset(path) {
  state.selectedPath = path;
  state.selectedPosePath = "";
  markSelected();
  setStatus(`Fetching ${path}`);
  try {
    const response = await fetchRemote(path);
    const decoded = parseGLB(await response.arrayBuffer());
    if (!decoded.renderMeshes.length) {
      throw new Error("this looks like a pose-only GLB; choose a matching _geo model first");
    }
    decoded.sourcePath = path;
    decoded.texturePath = texturePathForModel(path);
    decoded.texture = null;
    decoded.textureError = "";
    if (decoded.texturePath) {
      try {
        decoded.texture = await loadTexture(decoded.texturePath);
        state.colorMode = "texture";
        els.colorMode.value = "texture";
      } catch (error) {
        decoded.textureError = error.message;
      }
    }
    state.decoded = decoded;
    state.landing = defaultLandingSettings();
    state.landing.slug = modelSlug(path);
    writeLandingSettings();
    updateLandingJSONPreview();
    setupPoseSelect(path);
    renderDecoded(decoded, path);
  } catch (error) {
    console.error(error);
    setStatus(`Failed to load ${path}: ${error.message}`, true);
  }
}

async function loadPose(path) {
  if (!state.decoded) return;
  state.selectedPosePath = path;
  if (!path) {
    renderDecoded(state.decoded, state.selectedPath);
    return;
  }

  setStatus(`Fetching pose ${path}`);
  try {
    let pose = state.poseCache.get(path);
    if (!pose) {
      const response = await fetchRemote(path);
      pose = parseGLB(await response.arrayBuffer());
      pose.path = path;
      state.poseCache.set(path, pose);
    }
    pose.compatibility = poseCompatibility(state.decoded, pose);
    if (!pose.compatibility.compatible) {
      throw new Error(`pose skeleton mismatch (${pose.compatibility.matched}/${pose.compatibility.total} joints matched)`);
    }
    state.animationPlaying = Boolean(pose.animation);
    renderDecoded(state.decoded, state.selectedPath, pose);
  } catch (error) {
    console.error(error);
    setStatus(`Failed to load pose ${path}: ${error.message}`, true);
  }
}

function setupPoseSelect(path) {
  const poses = poseCandidates(path);
  els.poseSelect.innerHTML = "";
  const rest = document.createElement("option");
  rest.value = "";
  rest.textContent = "Rest pose";
  els.poseSelect.appendChild(rest);

  for (const pose of poses) {
    const option = document.createElement("option");
    option.value = pose;
    option.textContent = poseLabel(pose);
    els.poseSelect.appendChild(option);
  }

  els.poseSelect.disabled = poses.length === 0;
  els.poseSelect.value = "";
}

function poseCandidates(path) {
  const file = path.split("/").pop();
  const dir = path.slice(0, path.length - file.length);
  const prefixes = posePrefixes(dir, file);
  if (!prefixes.primary.length && !prefixes.fallback.length) return [];
  const primary = matchingPoseCandidates(path, prefixes.primary);
  if (primary.length) return primary;
  return matchingPoseCandidates(path, prefixes.fallback);
}

function matchingPoseCandidates(path, prefixes) {
  if (!prefixes.length) return [];
  return state.assets
    .filter((asset) => prefixes.some((prefix) => asset.startsWith(prefix)))
    .filter((asset) => isPoseAnimationAsset(asset, path))
    .filter((asset, index, assets) => assets.indexOf(asset) === index)
    .sort((a, b) => posePrefixRank(a, prefixes) - posePrefixRank(b, prefixes) || poseSortKey(a).localeCompare(poseSortKey(b)))
    .slice(0, 80);
}

function posePrefixRank(path, prefixes) {
  const index = prefixes.findIndex((prefix) => path.startsWith(prefix));
  return index === -1 ? prefixes.length : index;
}

function posePrefixes(dir, file) {
  const stem = file.replace(/_dl_opt\.glb$/, "").replace(/\.glb$/, "");
  const primary = new Set();
  const fallback = new Set();
  const suffixes = [
    "_default_geo",
    "_geo",
    "default_geo",
    "geo"
  ];

  for (const suffix of suffixes) {
    if (!stem.endsWith(suffix)) continue;
    let base = stem.slice(0, stem.length - suffix.length);
    if (!base.endsWith("_") && base.length > 0) base += "_";
    if (base.length > 0) primary.add(dir + base);

    const skinless = base.replace(/(?:skin|costume|default)_[a-z0-9]+_$/i, "");
    if (skinless && skinless !== base) fallback.add(dir + skinless);
  }

  const animeIndex = stem.indexOf("_anime_");
  if (animeIndex > 0) primary.add(dir + stem.slice(0, animeIndex + "_anime_".length));

  const firstToken = stem.split("_")[0];
  const animationAliases = {
    archerqueen: "archerqueen_anime_",
    barbarianking: "barbking_anime_",
    barbking: "barbking_anime_",
    grandwarden: "grandwarden_anime_",
    royalchampion: "royalchampion_anime_"
  };
  if (animationAliases[firstToken]) fallback.add(dir + animationAliases[firstToken]);

  return {
    primary: [...primary].sort((a, b) => b.length - a.length),
    fallback: [...fallback].sort((a, b) => b.length - a.length)
  };
}

function poseSortKey(path) {
  const label = poseLabel(path);
  if (label === "idle1" || label.endsWith("_idle1")) return `00_${label}`;
  if (label === "menu1" || label.endsWith("_menu1")) return `01_${label}`;
  if (label.startsWith("walk") || label.includes("_walk")) return `02_${label}`;
  if (label.startsWith("attack") || label.includes("_attack")) return `03_${label}`;
  return `10_${label}`;
}

function poseLabel(path) {
  const file = path.split("/").pop().replace(/_dl_opt\.glb$/, "").replace(/\.glb$/, "");
  return file.replace(/^[a-z0-9]+(?:_[a-z0-9]+)*?_anime_/i, "");
}

function poseCompatibility(decoded, pose) {
  let total = 0;
  let matched = 0;
  if (!decoded.jointNodes.length || !pose.globalsByName) {
    return { compatible: false, total: decoded.jointNodes.length, matched: 0 };
  }
  for (const joint of decoded.jointNodes) {
    if (!joint || !joint.name) continue;
    total += 1;
    if (pose.globalsByName.has(joint.name)) matched += 1;
  }
  const ratio = total ? matched / total : 0;
  return { compatible: (total > 0 && matched === total) || (matched >= 24 && ratio >= 0.5), total, matched };
}

function texturePathForModel(path) {
  const candidates = textureCandidates(path);
  return candidates.length ? candidates[0] : "";
}

function textureCandidates(path) {
  const file = path.split("/").pop();
  const dir = path.slice(0, path.length - file.length);
  const stem = file
    .replace(/_geo_dl_opt\.glb$/, "")
    .replace(/_default_geo_dl_opt\.glb$/, "_default")
    .replace(/_geo\.glb$/, "")
    .replace(/_default_geo\.glb$/, "_default")
    .replace(/\.glb$/, "");
  const bases = [stem];
  if (stem === "barbking_default") bases.push("barbking_default2");

  const suffixes = [
    "_a_dl_opt.sctx",
    "_01_dl_opt.sctx",
    "_material_dl_opt.sctx",
    "_diffuse_dl_opt.sctx",
    "_a.sctx",
    "_01.sctx",
    ".sctx"
  ];
  const candidates = [];
  for (const base of bases) {
    for (const suffix of suffixes) {
      const candidate = dir + base + suffix;
      if (state.files.includes(candidate) && !candidates.includes(candidate)) candidates.push(candidate);
    }
  }
  for (const filePath of state.files) {
    if (!filePath.startsWith(dir + stem) || !filePath.endsWith(".sctx")) continue;
    const lower = filePath.toLowerCase();
    if (lower.includes("_normal") || /_n(?:_|\.|$)/.test(lower) || /_m(?:_|\.|$)/.test(lower) || lower.includes("_mask")) continue;
    if (!candidates.includes(filePath)) candidates.push(filePath);
  }
  return candidates;
}

async function loadTexture(path) {
  if (!path) return null;
  if (!state.config?.proxy) {
    throw new Error("SCTX texture preview requires the Go viewer server");
  }
  if (state.textureCache.has(path)) return state.textureCache.get(path);
  const encodedPath = path.split("/").map(encodeURIComponent).join("/");
  const texture = await textureLoader.loadAsync(`./texture/${encodedPath}`);
  texture.name = path;
  texture.colorSpace = THREE.SRGBColorSpace;
  texture.flipY = false;
  texture.wrapS = THREE.ClampToEdgeWrapping;
  texture.wrapT = THREE.ClampToEdgeWrapping;
  state.textureCache.set(path, texture);
  return texture;
}

function exportNodeName(node, usedNames) {
  const source = String(node.name || `node_${node.index}`).replace(/[^a-zA-Z0-9_-]+/g, "_") || `node_${node.index}`;
  let name = source;
  let suffix = 2;
  while (usedNames.has(name)) name = `${source}_${suffix++}`;
  usedNames.add(name);
  return name;
}

function setObjectTransform(object, node) {
  object.position.fromArray(node.translation || [0, 0, 0]);
  object.quaternion.fromArray(node.rotation || [0, 0, 0, 1]).normalize();
  object.scale.fromArray(node.scale || [1, 1, 1]);
}

function buildExportNodeHierarchy(decoded, root) {
  const usedNames = new Set();
  const objects = decoded.nodes.map((node) => {
    const bone = new THREE.Bone();
    bone.name = exportNodeName(node, usedNames);
    bone.userData.sc3dSourceName = node.name || "";
    bone.userData.sc3dNodeIndex = node.index;
    setObjectTransform(bone, node);
    return bone;
  });

  for (const node of decoded.nodes) {
    const object = objects[node.index];
    const parent = Number.isInteger(node.parent) ? objects[node.parent] : null;
    if (parent && parent !== object) parent.add(object);
    else root.add(object);
  }
  root.updateMatrixWorld(true);
  return objects;
}

function normalizedSkinAttributes(stream, jointCount, staticJointIndex, jointIndexOffset = 0) {
  const indices = new Uint16Array(stream.vertexCount * 4);
  const weights = new Float32Array(stream.vertexCount * 4);
  for (let vertex = 0; vertex < stream.vertexCount; vertex += 1) {
    const offset = vertex * 4;
    let total = 0;
    for (let slot = 0; slot < 4; slot += 1) {
      const joint = stream.boneIndices[offset + slot];
      const weight = stream.boneWeights[offset + slot];
      if (!weight || joint >= jointCount) continue;
      indices[offset + slot] = joint + jointIndexOffset;
      weights[offset + slot] = weight;
      total += weight;
    }
    if (total === 0) {
      indices[offset] = staticJointIndex;
      weights[offset] = 1;
      continue;
    }
    for (let slot = 0; slot < 4; slot += 1) weights[offset + slot] /= total;
  }
  return { indices, weights };
}

function compactVertexStream(stream, sourceIndices) {
  const oldToNew = new Map();
  const referenced = [];
  for (let i = 0; i < sourceIndices.length; i += 1) {
    const sourceIndex = sourceIndices[i];
    if (oldToNew.has(sourceIndex)) continue;
    oldToNew.set(sourceIndex, referenced.length);
    referenced.push(sourceIndex);
  }
  const IndexArray = referenced.length > 65535 ? Uint32Array : Uint16Array;
  const indices = new IndexArray(sourceIndices.length);
  for (let i = 0; i < sourceIndices.length; i += 1) indices[i] = oldToNew.get(sourceIndices[i]);

  const copy = (source, itemSize, ArrayType = source && source.constructor) => {
    if (!source || !source.length) return null;
    const output = new ArrayType(referenced.length * itemSize);
    for (let destination = 0; destination < referenced.length; destination += 1) {
      const sourceOffset = referenced[destination] * itemSize;
      output.set(source.subarray(sourceOffset, sourceOffset + itemSize), destination * itemSize);
    }
    return output;
  };

  return {
    vertexCount: referenced.length,
    indices,
    positions: copy(stream.positions, 3, Float32Array),
    uvs: copy(stream.uvs, 2, Float32Array),
    normals: copy(stream.normals, 3, Float32Array),
    boneIndices: copy(stream.boneIndices, 4, Uint8Array),
    boneWeights: copy(stream.boneWeights, 4)
  };
}

function exportGeometry(decoded, meshInfo, skin, jointIndexOffset = 0) {
  const stream = compactVertexStream(vertexStreamForMesh(decoded, meshInfo), meshInfo.indices);
  const geometry = new THREE.BufferGeometry();
  geometry.setAttribute("position", new THREE.BufferAttribute(stream.positions, 3));
  if (stream.uvs && stream.uvs.length === stream.vertexCount * 2) {
    geometry.setAttribute("uv", new THREE.BufferAttribute(stream.uvs, 2));
  }
  if (stream.normals && stream.normals.length === stream.vertexCount * 3 && !state.smoothShading) {
    geometry.setAttribute("normal", new THREE.BufferAttribute(stream.normals, 3));
  }
  geometry.setIndex(new THREE.BufferAttribute(stream.indices, 1));
  if (!geometry.getAttribute("normal")) geometry.computeVertexNormals();

  if (skin && skin.jointNodes.length) {
    const staticJointIndex = jointIndexOffset + skin.jointNodes.length;
    const attributes = normalizedSkinAttributes(stream, skin.jointNodes.length, staticJointIndex, jointIndexOffset);
    geometry.setAttribute("skinIndex", new THREE.BufferAttribute(attributes.indices, 4));
    geometry.setAttribute("skinWeight", new THREE.BufferAttribute(attributes.weights, 4));
  }
  geometry.computeBoundingBox();
  geometry.computeBoundingSphere();
  return geometry;
}

function exportMaterial(decoded, meshInfo) {
  const useTexture = Boolean(decoded.texture);
  const material = materialForMesh(decoded, meshInfo, useTexture);
  material.name = `material_${meshInfo.material}`;
  material.wireframe = false;
  return material;
}

function buildExportScene(decoded, pose) {
  if (!decoded || !pose || !pose.animation) throw new Error("choose an animated pose before exporting");
  if (!decoded.texture) throw new Error("the selected model has no decoded texture to embed");

  const root = new THREE.Scene();
  root.name = "Skin";
  const skeletonRoot = new THREE.Bone();
  skeletonRoot.name = "skeleton_root";
  root.add(skeletonRoot);
  const nodeObjects = buildExportNodeHierarchy(decoded, skeletonRoot);
  const disposable = [];
  let skinnedMeshCount = 0;

  for (const meshInfo of decoded.renderMeshes) {
    const skin = skinForMesh(decoded, meshInfo);
    const hasSkin = Boolean(skin && skin.jointNodes && skin.jointNodes.length);
    const geometry = exportGeometry(decoded, meshInfo, hasSkin ? skin : null, hasSkin ? 1 : 0);
    const material = exportMaterial(decoded, meshInfo);
    disposable.push(geometry, material);

    let mesh;
    if (hasSkin) {
      const staticBone = new THREE.Bone();
      staticBone.name = `static_${meshInfo.renderIndex}`;
      skeletonRoot.add(staticBone);
      const sourceBones = skin.jointNodes.map((node) => nodeObjects[node.index]).filter(Boolean);
      if (sourceBones.length !== skin.jointNodes.length) {
        throw new Error(`skin ${skin.index} references missing skeleton nodes`);
      }
      const sourceInverses = skin.inverseBindMatrices.slice(0, sourceBones.length).map((matrix) => matrix.clone());
      while (sourceInverses.length < sourceBones.length) {
        const bone = sourceBones[sourceInverses.length];
        sourceInverses.push(new THREE.Matrix4().copy(bone.matrixWorld).invert());
      }
      const bones = [skeletonRoot, ...sourceBones, staticBone];
      const inverses = [new THREE.Matrix4(), ...sourceInverses, new THREE.Matrix4()];
      const skeleton = new THREE.Skeleton(bones, inverses);
      mesh = new THREE.SkinnedMesh(geometry, material);
      mesh.bind(skeleton, new THREE.Matrix4());
      mesh.normalizeSkinWeights();
      skinnedMeshCount += 1;
    } else {
      mesh = new THREE.Mesh(geometry, material);
    }
    mesh.name = `mesh_${meshInfo.renderIndex}_${String(meshInfo.name || "part").replace(/[^a-zA-Z0-9_-]+/g, "_")}`;
    root.add(mesh);
  }

  root.updateMatrixWorld(true);
  const clip = buildExportAnimationClip(decoded, pose, nodeObjects);
  return {
    root,
    clip,
    skinnedMeshCount,
    dispose() {
      for (const resource of disposable) resource.dispose();
    }
  };
}

function buildExportAnimationClip(decoded, pose, nodeObjects) {
  const animation = pose.animation;
  const frameCount = Math.max(1, animation.frameCount || 1);
  const frameRate = Math.max(1, animation.frameRate || 30);
  const sampleCount = frameCount + 1;
  const times = new Float32Array(sampleCount);
  const positions = decoded.nodes.map(() => new Float32Array(sampleCount * 3));
  const rotations = decoded.nodes.map(() => new Float32Array(sampleCount * 4));
  const scales = decoded.nodes.map(() => new Float32Array(sampleCount * 3));
  const inverseParent = new THREE.Matrix4();
  const local = new THREE.Matrix4();
  const position = new THREE.Vector3();
  const rotation = new THREE.Quaternion();
  const scale = new THREE.Vector3();
  const previousRotations = decoded.nodes.map(() => null);

  for (let sampleIndex = 0; sampleIndex < sampleCount; sampleIndex += 1) {
    const frame = sampleIndex === frameCount ? 0 : sampleIndex;
    const globals = retargetedPoseGlobals(decoded, pose, frame);
    times[sampleIndex] = sampleIndex / frameRate;
    for (const node of decoded.nodes) {
      local.copy(globals[node.index] || decoded.globals[node.index]);
      if (Number.isInteger(node.parent) && globals[node.parent]) {
        inverseParent.copy(globals[node.parent]).invert();
        local.premultiply(inverseParent);
      }
      local.decompose(position, rotation, scale);
      const previous = previousRotations[node.index];
      if (previous && previous.dot(rotation) < 0) rotation.set(-rotation.x, -rotation.y, -rotation.z, -rotation.w);
      previousRotations[node.index] = rotation.clone();
      position.toArray(positions[node.index], sampleIndex * 3);
      rotation.toArray(rotations[node.index], sampleIndex * 4);
      scale.toArray(scales[node.index], sampleIndex * 3);
    }
  }

  const tracks = [];
  for (const node of decoded.nodes) {
    const target = nodeObjects[node.index].name;
    tracks.push(new THREE.VectorKeyframeTrack(`${target}.position`, times, positions[node.index]).optimize());
    tracks.push(new THREE.QuaternionKeyframeTrack(`${target}.quaternion`, times, rotations[node.index]).optimize());
    tracks.push(new THREE.VectorKeyframeTrack(`${target}.scale`, times, scales[node.index]).optimize());
  }
  return new THREE.AnimationClip(poseLabel(pose.path || "idle"), frameCount / frameRate, tracks);
}

async function buildLandingGLB() {
  const built = buildExportScene(state.decoded, state.activePose);
  try {
    const exporter = new GLTFExporter();
    const output = await exporter.parseAsync(built.root, {
      animations: [built.clip],
      binary: true,
      onlyVisible: true,
      truncateDrawRange: true,
      trs: true
    });
    if (!(output instanceof ArrayBuffer)) throw new Error("GLTFExporter did not return binary GLB data");
    const validation = await validateLandingGLB(output);
    if (
      validation.skinnedMeshes < built.skinnedMeshCount ||
      validation.texturedMeshes < 1 ||
      validation.bones < 1 ||
      validation.animations !== 1 ||
      validation.tracks < 1
    ) {
      throw new Error(
        `standard loader validation failed (${validation.skinnedMeshes} skinned meshes, ${validation.texturedMeshes} textured meshes, ${validation.bones} bones, ${validation.tracks} tracks)`
      );
    }
    return { data: output, validation };
  } finally {
    built.dispose();
  }
}

function validateLandingGLB(data) {
  return new Promise((resolve, reject) => {
    new GLTFLoader().parse(data, "", (gltf) => {
      let meshes = 0;
      let skinnedMeshes = 0;
      let texturedMeshes = 0;
      let bones = 0;
      gltf.scene.traverse((object) => {
        if (object.isBone) bones += 1;
        if (!object.isMesh) return;
        meshes += 1;
        if (object.isSkinnedMesh) skinnedMeshes += 1;
        if (object.material && object.material.map) texturedMeshes += 1;
      });
      const tracks = gltf.animations.reduce((total, animation) => total + animation.tracks.length, 0);
      resolve({ meshes, skinnedMeshes, texturedMeshes, bones, animations: gltf.animations.length, tracks });
    }, reject);
  });
}

async function exportLandingGLB() {
  if (state.exportInProgress) return;
  state.exportInProgress = true;
  updateAnimationControl();
  setStatus("Building standard animated GLB");
  try {
    const { data, validation } = await buildLandingGLB();
    downloadBlob(new Blob([data], { type: "model/gltf-binary" }), "model.glb");
    setStatus(`Exported model.glb (${validation.meshes} meshes, ${validation.animations} animation, ${formatBytes(data.byteLength)})`);
  } catch (error) {
    console.error(error);
    setStatus(`Failed to export GLB: ${error.message}`, true);
  } finally {
    state.exportInProgress = false;
    updateAnimationControl();
  }
}

function exportLandingJSON() {
  const data = JSON.stringify(landingMetadata(), null, 2) + "\n";
  downloadBlob(new Blob([data], { type: "application/json" }), "skin.json");
  setStatus("Exported skin.json");
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`;
}

function panViewportVertical(direction) {
  if (!canRenderScene()) return;
  const up = new THREE.Vector3().setFromMatrixColumn(camera.matrixWorld, 1);
  const distance = Math.max(camera.position.distanceTo(controls.target), 1);
  const amount = distance * 0.08 * direction;
  camera.position.addScaledVector(up, amount);
  controls.target.addScaledVector(up, amount);
  controls.update();
  controls.saveState();
}

async function exportAnimationWebP() {
  const pose = state.activePose;
  if (!canRenderScene() || !state.decoded || !pose || !pose.animation || state.exportInProgress) return;

  state.exportInProgress = true;
  updateAnimationControl();

  const wasPlaying = state.animationPlaying;
  const previousFrame = state.animationFrame;
  const wasGridVisible = grid.visible;
  const frameCount = Math.max(1, pose.animation.frameCount || 1);
  const frameRate = Math.max(1, pose.animation.frameRate || 30);
  const playbackSpeed = Math.max(0.05, state.landing.animation.speed || 1);
  const exportFrameCount = Math.min(frameCount, 180);
  const frameStep = frameCount / exportFrameCount;
  const delayMS = Math.max(10, Math.round(1000 / (frameRate * playbackSpeed) * frameStep));

  try {
    state.animationPlaying = false;
    grid.visible = false;
    setStatus(`Capturing ${exportFrameCount} transparent frames`);

    const frameBlobs = [];
    for (let i = 0; i < exportFrameCount; i += 1) {
      const frame = Math.min(frameCount - 1, Math.floor(i * frameStep));
      applyAnimationFrame(frame);
      controls.update();
      renderer.render(scene, camera);
      frameBlobs.push(await canvasToWebPBlob());
      if (i % 12 === 0 || i === exportFrameCount - 1) {
        setStatus(`Capturing transparent WebP frames ${i + 1}/${exportFrameCount}`);
        await nextFrame();
      }
    }

    setStatus("Packing animated WebP");
    const loop = els.webpLoop.checked;
    const webpBlob = await makeAnimatedWebPBlob(frameBlobs, renderer.domElement.width, renderer.domElement.height, delayMS, loop);
    const copied = await copyBlobToClipboard(webpBlob);
    downloadBlob(webpBlob, exportFileName("webp"));
    setStatus(`Exported ${exportFrameCount} frame transparent WebP (${loop ? "looping" : "play once"})${copied ? " and copied it to clipboard" : ""}`);
  } catch (error) {
    console.error(error);
    setStatus(`Failed to export WebP: ${error.message}`, true);
  } finally {
    grid.visible = wasGridVisible;
    if (Number.isFinite(previousFrame) && previousFrame >= 0) applyAnimationFrame(previousFrame);
    state.animationPlaying = wasPlaying;
    state.animationStartedAt = performance.now() - Math.max(state.animationFrame, 0) / (frameRate * playbackSpeed) * 1000;
    state.exportInProgress = false;
    updateAnimationControl();
  }
}

function nextFrame() {
  return new Promise((resolve) => requestAnimationFrame(resolve));
}

async function canvasToWebPBlob() {
  const blob = await new Promise((resolve, reject) => {
    renderer.domElement.toBlob((blob) => {
      if (!blob) {
        reject(new Error(webpUnsupportedMessage()));
        return;
      }
      resolve(blob);
    }, "image/webp", 0.92);
  });
  const header = new Uint8Array(await blob.slice(0, 12).arrayBuffer());
  if (header.length < 12 || readFourCC(header, 0) !== "RIFF" || readFourCC(header, 8) !== "WEBP") {
    throw new Error(webpUnsupportedMessage());
  }
  return blob;
}

function webpUnsupportedMessage() {
  return "this browser cannot encode WebP frames; try Chrome, Edge, or Firefox";
}

async function makeAnimatedWebPBlob(frameBlobs, width, height, delayMS, loop) {
  const frames = [];
  for (const blob of frameBlobs) {
    frames.push(parseStillWebPFrame(new Uint8Array(await blob.arrayBuffer())));
  }
  return new Blob([buildAnimatedWebP(frames, width, height, delayMS, loop)], { type: "image/webp" });
}

function parseStillWebPFrame(bytes) {
  if (bytes.length < 20 || readFourCC(bytes, 0) !== "RIFF" || readFourCC(bytes, 8) !== "WEBP") {
    throw new Error("browser did not return WebP frame data");
  }
  const chunks = [];
  let offset = 12;
  while (offset + 8 <= bytes.length) {
    const fourCC = readFourCC(bytes, offset);
    const size = readU32(bytes, offset + 4);
    const start = offset + 8;
    const end = start + size;
    if (end > bytes.length) throw new Error("browser returned truncated WebP frame data");
    if (fourCC === "ALPH" || fourCC === "VP8 " || fourCC === "VP8L") {
      chunks.push(makeWebPChunk(fourCC, bytes.slice(start, end)));
    }
    offset = end + (size % 2);
  }
  if (!chunks.some((chunk) => readFourCC(chunk, 0) === "VP8 " || readFourCC(chunk, 0) === "VP8L")) {
    throw new Error("browser WebP frame is missing image data");
  }
  return chunks;
}

function buildAnimatedWebP(frames, width, height, delayMS, loop) {
  if (!frames.length) throw new Error("no frames captured");
  if (width < 1 || height < 1 || width > 16777216 || height > 16777216) {
    throw new Error("viewport is too large for animated WebP");
  }

  const chunks = [];
  const vp8x = new Uint8Array(10);
  vp8x[0] = 0x12; // animation + alpha
  writeU24(vp8x, 4, width - 1);
  writeU24(vp8x, 7, height - 1);
  chunks.push(makeWebPChunk("VP8X", vp8x));

  chunks.push(makeWebPChunk("ANIM", webPAnimationOptions(loop)));

  const duration = Math.max(10, Math.min(16777215, delayMS));
  for (const frameChunks of frames) {
    const header = new Uint8Array(16);
    writeU24(header, 6, width - 1);
    writeU24(header, 9, height - 1);
    writeU24(header, 12, duration);
    header[15] = 0x02; // full-frame replacement, no blend with prior frame
    chunks.push(makeWebPChunk("ANMF", concatBytes([header, ...frameChunks])));
  }

  const riffSize = 4 + chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  if (riffSize > 0xffffffff) throw new Error("animated WebP is too large");
  const output = new Uint8Array(8 + riffSize);
  writeFourCC(output, 0, "RIFF");
  writeU32(output, 4, riffSize);
  writeFourCC(output, 8, "WEBP");
  let offset = 12;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}

function makeWebPChunk(fourCC, data) {
  const chunk = new Uint8Array(8 + data.length + (data.length % 2));
  writeFourCC(chunk, 0, fourCC);
  writeU32(chunk, 4, data.length);
  chunk.set(data, 8);
  return chunk;
}

function concatBytes(chunks) {
  const output = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}

function readFourCC(bytes, offset) {
  return String.fromCharCode(bytes[offset], bytes[offset + 1], bytes[offset + 2], bytes[offset + 3]);
}

function readU32(bytes, offset) {
  return bytes[offset] + bytes[offset + 1] * 256 + bytes[offset + 2] * 65536 + bytes[offset + 3] * 16777216;
}

function writeFourCC(bytes, offset, value) {
  for (let i = 0; i < 4; i += 1) bytes[offset + i] = value.charCodeAt(i);
}

function writeU24(bytes, offset, value) {
  value = Math.max(0, Math.min(16777215, Math.round(value)));
  bytes[offset] = value & 0xff;
  bytes[offset + 1] = (value >> 8) & 0xff;
  bytes[offset + 2] = (value >> 16) & 0xff;
}

function writeU32(bytes, offset, value) {
  value = Math.max(0, Math.floor(value));
  bytes[offset] = value & 0xff;
  bytes[offset + 1] = Math.floor(value / 256) & 0xff;
  bytes[offset + 2] = Math.floor(value / 65536) & 0xff;
  bytes[offset + 3] = Math.floor(value / 16777216) & 0xff;
}

async function copyBlobToClipboard(blob) {
  if (!navigator.clipboard || typeof ClipboardItem === "undefined") return false;
  try {
    await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]);
    return true;
  } catch (error) {
    console.warn("WebP clipboard copy failed", error);
    return false;
  }
}

function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

function exportFileName(extension) {
  const model = state.selectedPath ? state.selectedPath.split("/").pop().replace(/\.glb$/i, "") : "sc3d";
  const pose = state.selectedPosePath ? poseLabel(state.selectedPosePath) : "rest";
  return `${model}_${pose}.${extension}`.replace(/[^a-z0-9._-]+/gi, "_");
}

function diagnosticMetrics(options = {}) {
  const decoded = state.decoded;
  const pose = state.activePose;
  if (!decoded) return { error: "no decoded model" };

  const frames = diagnosticFrames(pose, options.frames);
  const meshPattern = options.meshPattern ? new RegExp(options.meshPattern, "i") : null;
  const meshReports = decoded.renderMeshes
    .filter((meshInfo) => !meshPattern || meshPattern.test(meshInfo.name))
    .map((meshInfo) => diagnosticMeshMetrics(decoded, pose, meshInfo, frames));

  return {
    model: state.selectedPath,
    pose: pose ? pose.path || state.selectedPosePath : "",
    frameCount: pose && pose.animation ? pose.animation.frameCount : 0,
    frames,
    animation: diagnosticAnimationMetrics(pose),
    streams: diagnosticStreamMetrics(decoded),
    joints: diagnosticJointMetrics(decoded, pose, frames),
    hairHeadOverlap: diagnosticHairHeadOverlap(decoded, pose, frames),
    meshes: meshReports
  };
}

function diagnosticAnimationMetrics(pose) {
  if (!pose || !pose.animation) return null;
  const animation = pose.animation;
  const channels = [...animation.nodesByName.entries()].map(([name, channel]) => {
    const samples = channel.samples || [];
    const framedSamples = samples.filter((sample) => Number.isFinite(sample.frame));
    const first = samples[0] || null;
    const last = samples[samples.length - 1] || null;
    return {
      name,
      sampleCount: samples.length,
      firstFrame: first && Number.isFinite(first.frame) ? first.frame : null,
      lastFrame: last && Number.isFinite(last.frame) ? last.frame : null,
      hasExplicitFrames: framedSamples.length > 0,
      rotationSamples: samples.filter((sample) => sample.rotation).length,
      translationSamples: samples.filter((sample) => sample.translation).length,
      scaleSamples: samples.filter((sample) => sample.scale).length
    };
  });

  const frameCount = Math.max(1, animation.frameCount || 1);
  const sparseChannels = channels
    .filter((channel) => channel.sampleCount > 0 && channel.sampleCount < Math.max(2, frameCount * 0.25))
    .sort((a, b) => a.sampleCount - b.sampleCount || a.name.localeCompare(b.name))
    .slice(0, 25);
  const earlyEndingChannels = channels
    .filter((channel) => Number.isFinite(channel.lastFrame) && channel.lastFrame < frameCount - 2)
    .sort((a, b) => a.lastFrame - b.lastFrame || a.name.localeCompare(b.name))
    .slice(0, 25);

  return {
    frameRate: animation.frameRate,
    frameCount,
    packedNodeCount: animation.packedNodeCount,
    decodedNodeCount: animation.decodedNodeCount,
    skippedSparseCount: animation.skippedSparseCount,
    channelCount: channels.length,
    minSampleCount: channels.length ? Math.min(...channels.map((channel) => channel.sampleCount)) : 0,
    maxSampleCount: channels.length ? Math.max(...channels.map((channel) => channel.sampleCount)) : 0,
    sparseChannels,
    earlyEndingChannels
  };
}

function diagnosticStreamMetrics(decoded) {
  const streams = decoded.streams && decoded.streams.length ? decoded.streams : [decoded];
  return streams.map((stream, index) => ({
    index: Number.isInteger(stream.index) ? stream.index : index,
    vertexCount: stream.vertexCount || Math.floor((stream.positions || []).length / 3),
    weightRepairStats: stream.weightRepairStats || null
  }));
}

function diagnosticJointMetrics(decoded, pose, frames) {
  const pattern = /head|eye|lid|brow|mouth|jaw|hair/i;
  const origin = new THREE.Vector3();
  const current = new THREE.Vector3();
  const rest = new THREE.Vector3();
  return decoded.nodes
    .filter((node) => node.name && pattern.test(node.name) && decoded.globals[node.index])
    .map((node) => {
      rest.setFromMatrixPosition(decoded.globals[node.index]);
      const frameReports = frames.map((frame) => {
        const globals = pose ? retargetedPoseGlobals(decoded, pose, frame) : decoded.globals;
        current.copy(origin).applyMatrix4(globals[node.index] || decoded.globals[node.index]);
        return {
          frame,
          position: [current.x, current.y, current.z],
          distanceFromRest: current.distanceTo(rest)
        };
      });
      return {
        name: node.name,
        rest: [rest.x, rest.y, rest.z],
        maxDistanceFromRest: Math.max(...frameReports.map((item) => item.distanceFromRest)),
        frames: frameReports
      };
    })
    .sort((a, b) => b.maxDistanceFromRest - a.maxDistanceFromRest);
}

function diagnosticHairHeadOverlap(decoded, pose, frames) {
  const headNode = (decoded.nodesByName && decoded.nodesByName.get("head_s")) ||
    decoded.nodes.find((node) => node.name && /^head/i.test(node.name));
  if (!headNode) return null;

  const bodyMeshes = decoded.renderMeshes.filter((meshInfo) => /body|face|head/i.test(meshInfo.name));
  const hairMeshes = decoded.renderMeshes.filter((meshInfo) => /hair/i.test(meshInfo.name));
  if (!bodyMeshes.length || !hairMeshes.length) return null;

  const headInfluencePattern = /head|eye|lid|brow|mouth|jaw|nose|ear/i;
  const point = new THREE.Vector3();
  const inverseHead = new THREE.Matrix4();

  return frames.map((frame) => {
    const globals = pose ? retargetedPoseGlobals(decoded, pose, frame) : decoded.globals;
    inverseHead.copy(globals[headNode.index] || decoded.globals[headNode.index]).invert();

    const headPoints = [];
    for (const meshInfo of bodyMeshes) {
      const positions = posePositions(decoded, pose, frame, meshInfo);
      const vertices = uniqueMeshVertices(meshInfo);
      for (const vertex of vertices) {
        if (!vertexHasWeightedJoint(decoded, meshInfo, vertex, headInfluencePattern)) continue;
        point.fromArray(positions, vertex * 3).applyMatrix4(inverseHead);
        headPoints.push(point.clone());
      }
    }

    const headBounds = boundsForPoints(headPoints);
    if (!headBounds) {
      return { frame, error: "no head-weighted body vertices" };
    }

    let hairVertexCount = 0;
    let overlappingHairVertices = 0;
    let maxInsideDepth = 0;
    for (const meshInfo of hairMeshes) {
      const positions = posePositions(decoded, pose, frame, meshInfo);
      const vertices = uniqueMeshVertices(meshInfo);
      for (const vertex of vertices) {
        point.fromArray(positions, vertex * 3).applyMatrix4(inverseHead);
        hairVertexCount += 1;
        const depth = pointInsideBoundsDepth(point, headBounds);
        if (depth <= 0) continue;
        overlappingHairVertices += 1;
        maxInsideDepth = Math.max(maxInsideDepth, depth);
      }
    }

    return {
      frame,
      hairVertexCount,
      overlappingHairVertices,
      overlapRatio: hairVertexCount ? overlappingHairVertices / hairVertexCount : 0,
      maxInsideDepth,
      headBounds
    };
  });
}

function uniqueMeshVertices(meshInfo) {
  return [...new Set(meshInfo.indices)];
}

function vertexHasWeightedJoint(decoded, meshInfo, vertex, pattern) {
  const stream = vertexStreamForMesh(decoded, meshInfo);
  const skin = skinForMesh(decoded, meshInfo);
  const offset = vertex * 4;
  for (let slot = 0; slot < 4; slot += 1) {
    const weight = stream.boneWeights[offset + slot];
    if (!weight) continue;
    const joint = skin.jointNodes[stream.boneIndices[offset + slot]];
    if (joint && joint.name && pattern.test(joint.name)) return true;
  }
  return false;
}

function boundsForPoints(points) {
  if (!points.length) return null;
  const min = [Infinity, Infinity, Infinity];
  const max = [-Infinity, -Infinity, -Infinity];
  for (const point of points) {
    min[0] = Math.min(min[0], point.x);
    min[1] = Math.min(min[1], point.y);
    min[2] = Math.min(min[2], point.z);
    max[0] = Math.max(max[0], point.x);
    max[1] = Math.max(max[1], point.y);
    max[2] = Math.max(max[2], point.z);
  }
  return {
    min,
    max,
    size: [max[0] - min[0], max[1] - min[1], max[2] - min[2]],
    center: [(min[0] + max[0]) / 2, (min[1] + max[1]) / 2, (min[2] + max[2]) / 2]
  };
}

function pointInsideBoundsDepth(point, bounds) {
  if (
    point.x < bounds.min[0] || point.x > bounds.max[0] ||
    point.y < bounds.min[1] || point.y > bounds.max[1] ||
    point.z < bounds.min[2] || point.z > bounds.max[2]
  ) {
    return 0;
  }
  return Math.min(
    point.x - bounds.min[0],
    bounds.max[0] - point.x,
    point.y - bounds.min[1],
    bounds.max[1] - point.y,
    point.z - bounds.min[2],
    bounds.max[2] - point.z
  );
}

function publishDiagnosticSnapshot() {
  let node = document.getElementById("sc3d-diagnostics");
  if (!node) {
    node = document.createElement("script");
    node.id = "sc3d-diagnostics";
    node.type = "application/json";
    document.body.appendChild(node);
  }
  node.textContent = JSON.stringify(diagnosticMetrics({
    meshPattern: "face|head|hair|eye|lash|teeth|tongue|jaw"
  }));
}

function diagnosticFrames(pose, requestedFrames) {
  if (Array.isArray(requestedFrames) && requestedFrames.length) {
    return requestedFrames.map((frame) => Math.max(0, Math.floor(frame)));
  }
  if (!pose || !pose.animation) return [0];
  const last = Math.max(0, pose.animation.frameCount - 1);
  return [...new Set([0, Math.floor(last * 0.25), Math.floor(last * 0.5), Math.floor(last * 0.75), last])];
}

function diagnosticMeshMetrics(decoded, pose, meshInfo, frames) {
  const rest = posePositions(decoded, null, null, meshInfo);
  const restEdges = triangleEdgeLengths(rest, meshInfo.indices);
  const frameReports = frames.map((frame) => {
    const positions = posePositions(decoded, pose, frame, meshInfo);
    const edges = triangleEdgeLengths(positions, meshInfo.indices);
    return {
      frame,
      bounds: boundsForPositions(positions, meshInfo.indices),
      edgeStretch: edgeStretchStats(restEdges, edges),
      worstEdge: worstEdgeStretch(decoded, meshInfo, restEdges, edges)
    };
  });
  return {
    name: meshInfo.name,
    material: meshInfo.material,
    streamIndex: meshInfo.streamIndex,
    triangles: Math.floor(meshInfo.indices.length / 3),
    restBounds: boundsForPositions(rest, meshInfo.indices),
    frames: frameReports
  };
}

function triangleEdgeLengths(positions, indices) {
  const edges = [];
  for (let i = 0; i + 2 < indices.length; i += 3) {
    const a = indices[i] * 3;
    const b = indices[i + 1] * 3;
    const c = indices[i + 2] * 3;
    edges.push(
      pointDistance(positions, a, b),
      pointDistance(positions, b, c),
      pointDistance(positions, c, a)
    );
  }
  return edges;
}

function pointDistance(positions, a, b) {
  return Math.hypot(
    positions[a] - positions[b],
    positions[a + 1] - positions[b + 1],
    positions[a + 2] - positions[b + 2]
  );
}

function edgeStretchStats(restEdges, edges) {
  let maxRatio = 1;
  let sumRatio = 0;
  let count = 0;
  for (let i = 0; i < restEdges.length && i < edges.length; i += 1) {
    const rest = restEdges[i];
    const edge = edges[i];
    if (rest <= 1e-6) continue;
    const ratio = edge / rest;
    maxRatio = Math.max(maxRatio, ratio, rest / Math.max(edge, 1e-6));
    sumRatio += Math.abs(Math.log(Math.max(ratio, 1e-6)));
    count += 1;
  }
  return {
    maxRatio,
    meanLogRatio: count ? sumRatio / count : 0
  };
}

function worstEdgeStretch(decoded, meshInfo, restEdges, edges) {
  let best = null;
  for (let edgeIndex = 0; edgeIndex < restEdges.length && edgeIndex < edges.length; edgeIndex += 1) {
    const rest = restEdges[edgeIndex];
    const edge = edges[edgeIndex];
    if (rest <= 1e-6 || edge <= 1e-6) continue;
    const ratio = Math.max(edge / rest, rest / edge);
    if (!best || ratio > best.ratio) {
      const triangleOffset = Math.floor(edgeIndex / 3) * 3;
      const edgeSlot = edgeIndex % 3;
      const a = meshInfo.indices[triangleOffset + edgeSlot];
      const b = meshInfo.indices[triangleOffset + ((edgeSlot + 1) % 3)];
      best = {
        ratio,
        rest,
        edge,
        vertices: [a, b],
        influences: [
          diagnosticVertexInfluences(decoded, meshInfo, a),
          diagnosticVertexInfluences(decoded, meshInfo, b)
        ]
      };
    }
  }
  return best;
}

function diagnosticVertexInfluences(decoded, meshInfo, vertex) {
  const stream = vertexStreamForMesh(decoded, meshInfo);
  const skin = skinForMesh(decoded, meshInfo);
  const offset = vertex * 4;
  const out = [];
  for (let slot = 0; slot < 4; slot += 1) {
    const weight = stream.boneWeights[offset + slot];
    if (!weight) continue;
    const jointIndex = stream.boneIndices[offset + slot];
    const joint = skin.jointNodes[jointIndex];
    out.push({
      slot,
      jointIndex,
      joint: joint ? joint.name : "",
      weight
    });
  }
  return out;
}

function boundsForPositions(positions, indices) {
  const min = [Infinity, Infinity, Infinity];
  const max = [-Infinity, -Infinity, -Infinity];
  for (let i = 0; i < indices.length; i += 1) {
    const offset = indices[i] * 3;
    for (let axis = 0; axis < 3; axis += 1) {
      const value = positions[offset + axis];
      min[axis] = Math.min(min[axis], value);
      max[axis] = Math.max(max[axis], value);
    }
  }
  return {
    min,
    max,
    size: [max[0] - min[0], max[1] - min[1], max[2] - min[2]],
    center: [(min[0] + max[0]) / 2, (min[1] + max[1]) / 2, (min[2] + max[2]) / 2]
  };
}

function filteredAssets() {
  const query = els.search.value.trim().toLowerCase();
  return state.assets.filter((path) => {
    const lower = path.toLowerCase();
    if (state.filter === "geo" && !(lower.includes("_geo") || lower.includes("_default_geo"))) return false;
    return !query || lower.includes(query);
  });
}

function renderAssetList() {
  const assets = filteredAssets().slice(0, 300);
  els.assetCount.textContent = `${filteredAssets().length.toLocaleString()} matching assets`;
  els.assetList.innerHTML = "";
  for (const path of assets) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `asset-row${path === state.selectedPath ? " active" : ""}`;
    const name = path.split("/").pop();
    button.innerHTML = `${name}<small>${path}</small>`;
    button.addEventListener("click", () => loadAsset(path));
    els.assetList.appendChild(button);
  }
}

function markSelected() {
  for (const row of els.assetList.querySelectorAll(".asset-row")) {
    row.classList.toggle("active", row.textContent.includes(state.selectedPath));
  }
}

async function init() {
  state.config = await loadViewerConfig();
  state.assetBaseURL = (state.config.base_url || state.config.baseURL || "").replace(/\/+$/, "");
  if (!state.assetBaseURL && state.config.fingerprint) {
    state.assetBaseURL = `${DEFAULT_ASSET_ORIGIN}/${state.config.fingerprint}`;
  }
  els.colorMode.value = state.colorMode;
  els.fingerprint.textContent = state.config.fingerprint ? state.config.fingerprint.slice(0, 12) : state.assetBaseURL;
  const fingerprint = await fetchRemote("fingerprint.json").then((res) => res.json());
  state.files = fingerprint.files
    .map((entry) => entry.file)
    .sort();
  state.assets = state.files.filter((path) => path.startsWith("sc3d/") && path.endsWith(".glb"));
  renderAssetList();
  const first = state.assets.find((path) => path.includes("archerqueen_anime_geo")) || state.assets.find((path) => path.includes("_geo"));
  if (first) loadAsset(first);
}

els.search.addEventListener("input", renderAssetList);
els.poseSelect.addEventListener("change", () => loadPose(els.poseSelect.value));
els.colorMode.addEventListener("change", () => {
  state.colorMode = els.colorMode.value;
  if (!state.decoded) return;
  const pose = state.selectedPosePath ? state.poseCache.get(state.selectedPosePath) : null;
  renderDecoded(state.decoded, state.selectedPath, pose);
});
for (const button of els.filters) {
  button.addEventListener("click", () => {
    state.filter = button.dataset.filter;
    els.filters.forEach((item) => item.classList.toggle("active", item === button));
    renderAssetList();
  });
}
els.resetView.addEventListener("click", () => {
  if (state.group) {
    frameGroup(state.group, state.decoded && state.activePose && state.activePose.animation ? sampledPoseBounds(state.decoded, state.activePose) : null);
  }
});
els.nudgeUp.addEventListener("click", () => panViewportVertical(-1));
els.nudgeDown.addEventListener("click", () => panViewportVertical(1));
els.playAnimation.addEventListener("click", () => {
  if (!state.activePose || !state.activePose.animation) return;
  state.animationPlaying = !state.animationPlaying;
  const rate = state.activePose.animation.frameRate * Math.max(0.05, state.landing.animation.speed || 1);
  state.animationStartedAt = performance.now() - Math.max(state.animationFrame, 0) / rate * 1000;
  updateAnimationControl();
});
els.exportWebp.addEventListener("click", exportAnimationWebP);
els.exportGLB.addEventListener("click", exportLandingGLB);
els.exportSkinJSON.addEventListener("click", exportLandingJSON);
const landingInputs = [
  els.landingSlug,
  els.landingScale,
  els.landingOffsetY,
  els.landingYaw,
  els.landingAnimationSpeed,
  els.landingFov,
  els.landingDistance,
  els.landingTargetY,
  els.landingDragSensitivity,
  els.landingMinYaw,
  els.landingMaxYaw,
  els.landingAllowYaw,
  els.landingAllowPitch,
  els.landingAllowZoom,
  els.landingAllowPan,
  els.landingAutoRotate,
  els.landingAutoRotateSpeed
];
for (const input of landingInputs) {
  input.addEventListener("input", () => applyLandingPreview(true));
  input.addEventListener("change", () => applyLandingPreview(true));
}
els.toggleGrid.addEventListener("click", () => {
  state.gridVisible = !state.gridVisible;
  grid.visible = state.gridVisible;
  els.toggleGrid.setAttribute("aria-pressed", String(state.gridVisible));
});
els.smoothShading.addEventListener("click", () => {
  state.smoothShading = !state.smoothShading;
  els.smoothShading.setAttribute("aria-pressed", String(state.smoothShading));
  if (!state.decoded) return;
  const pose = state.selectedPosePath ? state.poseCache.get(state.selectedPosePath) : null;
  renderDecoded(state.decoded, state.selectedPath, pose);
});
els.wireframe.addEventListener("click", () => {
  state.wireframe = !state.wireframe;
  els.wireframe.setAttribute("aria-pressed", String(state.wireframe));
  if (!state.group) return;
  state.group.traverse((object) => {
    if (object.isMesh) object.material.wireframe = state.wireframe;
  });
});

writeLandingSettings();
updateLandingJSONPreview();
init().catch((error) => setStatus(error.message, true));
