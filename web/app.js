// Thin glue between the browser libraries and the Go app (wasm/app.wasm).
// All logic — rules, plan lookup, geometry, the side panel's HTML, chat
// answers — lives in Go. This file only:
//   * starts the WASM module,
//   * drives MapLibre (map) and three.js (3D view),
//   * reads IFC (web-ifc) and .3dm (rhino3dm) files into raw triangles,
//   * forwards events to Go and applies the View it returns.

import * as maplibregl from "maplibre-gl";
import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";

const $ = (s) => document.querySelector(s);
const loading = (on, text = "Loading…") => { $("#loading").hidden = !on; $("#loading").textContent = text; };
let toastTimer;
function toast(msg) {
  const t = $("#toast"); t.textContent = msg; t.hidden = false;
  clearTimeout(toastTimer); toastTimer = setTimeout(() => (t.hidden = true), 6000);
}

// ---------------------------------------------------------------- WASM ----
async function startGo() {
  const go = new Go();
  const ready = new Promise((r) => (globalThis.__archregReady = r));
  const res = await WebAssembly.instantiateStreaming(fetch("wasm/app.wasm"), go.importObject);
  go.run(res.instance);
  await ready;
}

async function call(fn, ...args) {
  try {
    const json = await archreg[fn](...args);
    apply(JSON.parse(json));
  } catch (e) {
    console.error(e);
    toast(String(e.message || e));
  }
}

// ----------------------------------------------------------------- map ----
// OpenFreeMap's grey "positron" style matches the design's nedtonad map
// without an API key. Lantmäteriet's map needs credentials (see README).
const map = new maplibregl.Map({
  container: "map",
  style: "https://tiles.openfreemap.org/styles/positron",
  center: [18.078, 59.346],
  zoom: 14,
  attributionControl: { compact: true },
  maxPitch: 75,
});
map.addControl(new maplibregl.NavigationControl({ showCompass: true }), "bottom-right");
const pin = new maplibregl.Marker({ color: "#d32f2f" });
const empty = { type: "FeatureCollection", features: [] };
let mapReady = new Promise((r) => map.on("load", r));

map.on("load", () => {
  map.addSource("plans", { type: "geojson", data: empty });
  map.addSource("regs", { type: "geojson", data: empty });
  map.addSource("footprint", { type: "geojson", data: empty });
  map.addSource("envelope", { type: "geojson", data: empty });
  // Existing buildings with OpenStreetMap heights, for the 3D map. Below
  // the labels so street names stay readable.
  const firstLabel = map.getStyle().layers.find((l) => l.type === "symbol")?.id;
  map.addLayer({ id: "buildings-3d", type: "fill-extrusion", source: "openmaptiles", "source-layer": "building", minzoom: 13,
    filter: ["!=", ["get", "hide_3d"], true], layout: { visibility: "none" },
    // Buildings under the placed model are flattened (feature-state
    // "replaced"): the design replaces them, and they would hide it.
    paint: { "fill-extrusion-color": "#d8d4cf",
             "fill-extrusion-height": ["case", ["boolean", ["feature-state", "replaced"], false], 0, ["coalesce", ["get", "render_height"], 6]],
             "fill-extrusion-base": ["case", ["boolean", ["feature-state", "replaced"], false], 0, ["coalesce", ["get", "render_min_height"], 0]],
             "fill-extrusion-opacity": 0.8 } }, firstLabel);
  map.addLayer({ id: "plans-fill", type: "fill", source: "plans",
    paint: { "fill-color": "#d9a77a", "fill-opacity": ["case", ["get", "selected"], 0.35, 0.08] } });
  map.addLayer({ id: "plans-line", type: "line", source: "plans",
    paint: { "line-color": "#1d1b20", "line-width": ["case", ["get", "selected"], 2.5, 1.2],
             "line-dasharray": ["case", ["get", "pdfOnly"], ["literal", [2, 2]], ["literal", [1, 0]]] } });
  map.addLayer({ id: "regs-fill", type: "fill", source: "regs",
    paint: { "fill-color": ["match", ["get", "kind"], "prickmark", "#c62828", "korsmark", "#ef8f00", "#5c6bc0"], "fill-opacity": 0.22 } });
  map.addLayer({ id: "regs-line", type: "line", source: "regs",
    paint: { "line-color": ["match", ["get", "kind"], "prickmark", "#c62828", "korsmark", "#ef8f00", "#5c6bc0"], "line-width": 1.5, "line-dasharray": [2, 1] } });
  // Green: placement checks pass; red: on prickmark/korsmark/outside the
  // plan/too close to the boundary; orange: can't tell.
  const stateColor = ["match", ["get", "state"], "conflict", "#c62828", "ok", "#2e7d32", "#f57c00"];
  map.addLayer({ id: "footprint-fill", type: "fill", source: "footprint",
    paint: { "fill-color": stateColor, "fill-opacity": ["case", ["get", "highlight"], 0.75, 0.5] } });
  map.addLayer({ id: "footprint-line", type: "line", source: "footprint",
    paint: { "line-color": stateColor, "line-width": ["case", ["get", "highlight"], 3.5, 2] } });
  map.addLayer({ id: "envelope", type: "fill-extrusion", source: "envelope", layout: { visibility: "none" },
    paint: { "fill-extrusion-color": "#5c6bc0", "fill-extrusion-height": ["get", "height"], "fill-extrusion-opacity": 0.22 } });
  // The model goes below the see-through envelope: fill-extrusions write
  // depth, so a model drawn after it would fail the depth test inside it.
  map.addLayer(modelLayer, "envelope");
  map.on("sourcedata", (e) => { if (e.sourceId === "openmaptiles" && e.isSourceLoaded) hideReplacedSoon(); });
  map.on("mouseenter", "footprint-fill", () => (map.getCanvas().style.cursor = "move"));
  map.on("mouseleave", "footprint-fill", () => (map.getCanvas().style.cursor = ""));
});

// ------------------------------------------------------ model on the map ----
// The loaded model drawn into the map with three.js, sharing the map's
// WebGL context. Go sends where it stands (v.map.place); the matrix maps
// raw loader positions straight to MapLibre's Mercator space.
const onMap = { group: null, matrix: null, visible: false };
const modelLayer = {
  id: "model-3d", type: "custom", renderingMode: "3d",
  onAdd(m, gl) {
    this.camera = new THREE.Camera();
    this.scene = new THREE.Scene();
    this.scene.add(new THREE.HemisphereLight(0xffffff, 0x888899, 2.2));
    const sun = new THREE.DirectionalLight(0xffffff, 1.5); sun.position.set(30, -40, 60); this.scene.add(sun);
    this.renderer = new THREE.WebGLRenderer({ canvas: m.getCanvas(), context: gl, antialias: true });
    this.renderer.autoClear = false;
  },
  render(gl, args) {
    if (!onMap.visible || !onMap.group || !onMap.matrix) return;
    if (onMap.group.parent !== this.scene) this.scene.add(onMap.group);
    const proj = new THREE.Matrix4().fromArray(args.defaultProjectionData.mainMatrix);
    this.camera.projectionMatrix = proj.multiply(onMap.matrix);
    this.renderer.resetState();
    this.renderer.render(this.scene, this.camera);
  },
};

function placeMatrix(p) {
  const o = maplibregl.MercatorCoordinate.fromLngLat(p.origin, 0);
  const e = maplibregl.MercatorCoordinate.fromLngLat(p.east, 0);
  const n = maplibregl.MercatorCoordinate.fromLngLat(p.north, 0);
  const up = o.meterInMercatorCoordinateUnits();
  const world = new THREE.Matrix4().set(
    e.x - o.x, n.x - o.x, 0, o.x,
    e.y - o.y, n.y - o.y, 0, o.y,
    0, 0, up, 0,
    0, 0, 0, 1);
  const k = p.scale;
  const local = p.yUp // raw (x, y up, z south) → local (x, -z, y)
    ? new THREE.Matrix4().set(k, 0, 0, -p.cx, 0, 0, -k, -p.cy, 0, k, 0, -p.groundZ, 0, 0, 0, 1)
    : new THREE.Matrix4().set(k, 0, 0, -p.cx, 0, k, 0, -p.cy, 0, 0, k, -p.groundZ, 0, 0, 0, 1);
  return world.multiply(local);
}

// Rotate handle: a round knob just outside the building, on its local +y
// axis. Dragging it around the centre turns the building.
const handleEl = document.createElement("div");
handleEl.className = "rot-handle"; handleEl.title = "Drag to rotate the building";
const handle = new maplibregl.Marker({ element: handleEl, draggable: true });
const rot = { place: null, start: null, busy: false, next: null, dragging: false };
function handlePos(p) {
  const r = p.radius + 6; // metres
  return [p.origin[0] + (p.north[0] - p.origin[0]) * r, p.origin[1] + (p.north[1] - p.origin[1]) * r];
}
function angleAt(p, ll) {
  const k = Math.cos(p.origin[1] * Math.PI / 180);
  return Math.atan2(ll.lat - p.origin[1], (ll.lng - p.origin[0]) * k) * 180 / Math.PI;
}
handle.on("dragstart", () => {
  rot.dragging = true;
  rot.start = { rotation: rot.place.rotation, angle: angleAt(rot.place, handle.getLngLat()), place: rot.place };
});
handle.on("drag", () => {
  const s = rot.start; if (!s) return;
  let deg = s.rotation + angleAt(s.place, handle.getLngLat()) - s.angle;
  deg = ((deg + 540) % 360) - 180;
  sendRotate(Math.round(deg));
});
handle.on("dragend", () => { rot.dragging = false; rot.start = null; if (rot.place) handle.setLngLat(handlePos(rot.place)); });
async function sendRotate(deg) {
  if (rot.busy) { rot.next = deg; return; }
  rot.busy = true;
  await call("action", "rotate", String(deg), "");
  rot.busy = false;
  if (rot.next !== null) { const d = rot.next; rot.next = null; sendRotate(d); }
}
// Flatten existing OSM buildings that overlap the placed footprint.
let replaced = new Set(), replaceTimer;
function hideReplacedSoon() { clearTimeout(replaceTimer); replaceTimer = setTimeout(hideReplaced, 150); }
function hideReplaced() {
  if (!map.getSource("openmaptiles")) return;
  const ring = lastFootprint?.features?.[0]?.geometry?.coordinates?.[0];
  const now = new Set();
  if (ring && onMap.visible) {
    for (const f of map.querySourceFeatures("openmaptiles", { sourceLayer: "building" })) {
      if (f.id == null || now.has(f.id)) continue;
      const g = f.geometry;
      const polys = g.type === "Polygon" ? [g.coordinates] : g.type === "MultiPolygon" ? g.coordinates : [];
      if (polys.some((p) => overlaps(p[0], ring))) now.add(f.id);
    }
  }
  const key = (id) => ({ source: "openmaptiles", sourceLayer: "building", id });
  for (const id of replaced) if (!now.has(id)) map.setFeatureState(key(id), { replaced: false });
  for (const id of now) map.setFeatureState(key(id), { replaced: true });
  replaced = now;
}
function inRing([x, y], ring) {
  let inside = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const [xi, yi] = ring[i], [xj, yj] = ring[j];
    if ((yi > y) !== (yj > y) && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) inside = !inside;
  }
  return inside;
}
function overlaps(a, b) { return a.some((p) => inRing(p, b)) || b.some((p) => inRing(p, a)); }

let hinted = false;
function applyPlace(p) {
  rot.place = p;
  onMap.matrix = p ? placeMatrix(p) : null;
  if (p) {
    if (!rot.dragging) handle.setLngLat(handlePos(p)).addTo(map);
    if (!hinted) { hinted = true; toast("Drag the building to move it, and the round handle to rotate it. Switch to 3D map to see it among its neighbours."); }
  } else handle.remove();
  hideReplacedSoon();
  map.triggerRepaint();
}

let lastFootprint = null;
map.on("click", (e) => {
  if (dragging.moved) return;
  if (map.queryRenderedFeatures(e.point, { layers: ["footprint-fill"] }).length) return;
  call("dropPin", e.lngLat.lng, e.lngLat.lat);
});
let vpTimer;
map.on("moveend", () => {
  clearTimeout(vpTimer);
  vpTimer = setTimeout(() => {
    const b = map.getBounds();
    call("viewport", b.getWest(), b.getSouth(), b.getEast(), b.getNorth(), map.getZoom());
  }, 200);
});

// Drag the building footprint to move it.
const dragging = { on: false, moved: false, start: null, centre: null, busy: false, next: null };
map.on("mousedown", "footprint-fill", (e) => {
  e.preventDefault();
  dragging.on = true; dragging.moved = false; dragging.start = e.lngLat; dragging.centre = centroid(lastFootprint);
  map.dragPan.disable();
});
map.on("mousemove", (e) => {
  if (!dragging.on || !dragging.centre) return;
  dragging.moved = true;
  const lng = dragging.centre[0] + (e.lngLat.lng - dragging.start.lng);
  const lat = dragging.centre[1] + (e.lngLat.lat - dragging.start.lat);
  sendMove(lng, lat);
});
map.on("mouseup", () => {
  if (!dragging.on) return;
  dragging.on = false; map.dragPan.enable();
  setTimeout(() => (dragging.moved = false), 0);
});
async function sendMove(lng, lat) {
  if (dragging.busy) { dragging.next = [lng, lat]; return; }
  dragging.busy = true;
  await call("moveModel", lng, lat);
  dragging.busy = false;
  if (dragging.next) { const n = dragging.next; dragging.next = null; sendMove(...n); }
}
function centroid(f) {
  const ring = f?.features?.[0]?.geometry?.coordinates?.[0];
  if (!ring) return null;
  let x = 0, y = 0; const n = ring.length - 1;
  for (let i = 0; i < n; i++) { x += ring[i][0]; y += ring[i][1]; }
  return [x / n, y / n];
}

// ------------------------------------------------------------- 3D view ----
const viewer = { scene: null, camera: null, renderer: null, controls: null, meshes: new Map(), group: null, raf: 0 };
let lastGeometry = null; // { raw, positions } of the loaded model

const KIND_COLORS = { wall: 0xe9e4dc, slab: 0xcfc9c0, roof: 0x9b8f84, window: 0x8ec5ff, door: 0xa1724a, space: 0x7e57c2, transport: 0x777777, other: 0xdedad4 };

function initViewer() {
  if (viewer.renderer) return;
  const el = $("#viewer");
  viewer.scene = new THREE.Scene();
  viewer.camera = new THREE.PerspectiveCamera(45, 1, 0.1, 5000);
  viewer.camera.up.set(0, 0, 1);
  viewer.renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
  viewer.renderer.setPixelRatio(devicePixelRatio);
  el.appendChild(viewer.renderer.domElement);
  viewer.controls = new OrbitControls(viewer.camera, viewer.renderer.domElement);
  viewer.scene.add(new THREE.HemisphereLight(0xffffff, 0x888899, 2.2));
  const sun = new THREE.DirectionalLight(0xffffff, 1.5); sun.position.set(30, -40, 60); viewer.scene.add(sun);
  new ResizeObserver(resizeViewer).observe(el);
  const loop = () => { viewer.raf = requestAnimationFrame(loop); if (!el.hidden) { viewer.controls.update(); viewer.renderer.render(viewer.scene, viewer.camera); } };
  loop();
}
function resizeViewer() {
  const el = $("#viewer"); const w = el.clientWidth, h = el.clientHeight;
  if (!w || !h) return;
  viewer.renderer.setSize(w, h); viewer.camera.aspect = w / h; viewer.camera.updateProjectionMatrix();
}

function buildMeshes() {
  initViewer();
  if (viewer.group) { viewer.scene.remove(viewer.group); viewer.group.traverse((o) => o.geometry?.dispose()); }
  viewer.meshes.clear();
  viewer.group = new THREE.Group();
  if (!lastGeometry) { onMap.group?.removeFromParent(); onMap.group = null; map.triggerRepaint(); return; }
  const { raw, positions } = lastGeometry;
  for (const el of raw.elements) {
    if (!el.len || el.kind === "storey" || el.kind === "site") continue;
    const g = new THREE.BufferGeometry();
    g.setAttribute("position", new THREE.BufferAttribute(positions.slice(el.off, el.off + el.len), 3));
    g.computeVertexNormals();
    const space = el.kind === "space";
    const mat = new THREE.MeshLambertMaterial({
      color: KIND_COLORS[el.kind] ?? KIND_COLORS.other, transparent: true,
      opacity: space ? 0.12 : el.kind === "window" ? 0.7 : 1, depthWrite: !space, side: THREE.DoubleSide,
    });
    const m = new THREE.Mesh(g, mat);
    m.userData = { kind: el.kind, base: mat.clone() };
    if (raw.source === "3dm") m.scale.setScalar(1); // viewer doesn't need metres
    viewer.group.add(m);
    viewer.meshes.set(el.id, m);
  }
  viewer.scene.add(viewer.group);
  // Same geometry and materials (so highlights show on the map too).
  if (onMap.group) onMap.group.removeFromParent();
  onMap.group = viewer.group.clone();
  map.triggerRepaint();
}

function fitViewer() {
  if (!viewer.group) return;
  const box = new THREE.Box3().setFromObject(viewer.group);
  if (box.isEmpty()) return;
  const c = box.getCenter(new THREE.Vector3()), s = box.getSize(new THREE.Vector3()).length();
  viewer.controls.target.copy(c);
  viewer.camera.position.set(c.x + s * 0.6, c.y - s * 0.9, c.z + s * 0.6);
  viewer.camera.near = s / 1000; viewer.camera.far = s * 20; viewer.camera.updateProjectionMatrix();
}

function highlight(ids) {
  const on = new Set(ids);
  for (const [id, m] of viewer.meshes) {
    const base = m.userData.base;
    m.material.color.copy(on.has(id) ? new THREE.Color(0xd32f2f) : base.color);
    m.material.opacity = on.size === 0 ? base.opacity : on.has(id) ? 0.95 : Math.min(base.opacity, 0.12);
    m.material.depthWrite = on.size === 0 ? base.depthWrite : on.has(id);
  }
  if (on.size) {
    const box = new THREE.Box3();
    for (const id of on) { const m = viewer.meshes.get(id); if (m) box.expandByObject(m); }
    if (!box.isEmpty()) {
      const c = box.getCenter(new THREE.Vector3()), s = Math.max(box.getSize(new THREE.Vector3()).length(), 3);
      viewer.controls.target.copy(c);
      viewer.camera.position.set(c.x + s * 1.5, c.y - s * 2.5, c.z + s * 1.2);
    }
  }
}

let currentView = "map";
function show(view) {
  currentView = view;
  const model = view === "3d", tilted = view === "map3d";
  $("#map").hidden = model; $("#viewer").hidden = !model;
  for (const b of document.querySelectorAll(".seg button")) b.setAttribute("aria-pressed", String(b.dataset.view === view));
  if (model) { initViewer(); resizeViewer(); $("#map-legend").hidden = true; return; }
  map.resize();
  onMap.visible = tilted;
  for (const id of ["buildings-3d", "envelope"]) if (map.getLayer(id)) map.setLayoutProperty(id, "visibility", tilted ? "visible" : "none");
  $("#map-legend").hidden = !tilted;
  hideReplacedSoon();
  if (tilted && map.getPitch() < 30) map.easeTo({ pitch: 60, bearing: map.getBearing() || -20, zoom: Math.max(map.getZoom(), 16.5), duration: 900 });
  if (!tilted && map.getPitch() > 0) map.easeTo({ pitch: 0, bearing: 0, duration: 600 });
  map.triggerRepaint();
}
for (const b of document.querySelectorAll(".seg button")) b.addEventListener("click", () => show(b.dataset.view));

// ---------------------------------------------------------- apply view ----
async function apply(v) {
  const body = $("#panel"); const scroll = body.scrollTop;
  body.innerHTML = v.panel; body.scrollTop = scroll;
  const chat = $("#chat"); chat.innerHTML = v.chat; chat.scrollTop = chat.scrollHeight;
  if (v.error) toast(v.error);

  await mapReady;
  map.getSource("plans").setData(v.map.plans);
  map.getSource("regs").setData(v.map.regs);
  map.getSource("footprint").setData(v.map.footprint);
  map.getSource("envelope").setData(v.map.envelope);
  const note = $("#map-note"); note.textContent = v.map.envelopeNote || ""; note.hidden = !v.map.envelopeNote;
  lastFootprint = v.map.footprint;
  applyPlace(v.map.place || null);
  if (v.map.pin) pin.setLngLat(v.map.pin).addTo(map); else pin.remove();
  if (v.map.flyTo) map.flyTo({ center: [v.map.flyTo[0], v.map.flyTo[1]], zoom: v.map.flyTo[2] });

  if (v.model) {
    if (v.model.clear) { lastGeometry = null; buildMeshes(); }
    if (v.model.rebuild) buildMeshes();
    if (v.model.fit) fitViewer();
    if (viewer.renderer) highlight(v.model.highlight || []);
  }
  // Go asks for "map" when a plan result is picked; a tilted map is a map too.
  if (v.focus && !(v.focus === "map" && currentView === "map3d")) show(v.focus);
}

// ------------------------------------------------------- panel events ----
$("#panel").addEventListener("click", (e) => {
  const t = e.target.closest("button[data-action]");
  if (!t) return;
  const a = t.dataset.action;
  if (a === "import") return $("#file").click();
  if (a === "sample") return loadSample();
  call("action", a, t.dataset.value ?? "", t.dataset.id ?? "");
});
$("#panel").addEventListener("change", (e) => {
  const t = e.target;
  if (!t.dataset?.action || t.tagName === "BUTTON") return;
  const value = t.type === "checkbox" ? String(t.checked) : t.value;
  call("action", t.dataset.action, value, t.dataset.id ?? "");
});
let liveBusy = false;
$("#panel").addEventListener("input", async (e) => {
  const t = e.target;
  if (!t.hasAttribute?.("data-live") || liveBusy) return;
  liveBusy = true;
  const out = t.parentElement.querySelector("output"); if (out) out.textContent = t.value + "°";
  // Update map only; keep the slider element alive while dragging.
  try {
    const v = JSON.parse(await archreg.action(t.dataset.action, t.value, ""));
    await mapReady; map.getSource("footprint").setData(v.map.footprint); lastFootprint = v.map.footprint; applyPlace(v.map.place || null);
  } finally { liveBusy = false; }
});

// ------------------------------------------------------ search + suggest ----
// Go does the geocoding (engine/app/search.go); this only draws the list.
const search = { form: $("#search"), input: $("#search input"), list: $("#suggest"),
  places: [], active: -1, seq: 0, timer: null };

function setBusy(on) { search.form.classList.toggle("busy", on); search.input.setAttribute("aria-busy", on); }

function closeSuggest() {
  search.list.hidden = true; search.list.replaceChildren(); search.places = []; search.active = -1;
  search.input.setAttribute("aria-expanded", "false"); search.input.removeAttribute("aria-activedescendant");
}

function renderSuggest(places, q) {
  search.places = places; search.active = -1;
  search.list.replaceChildren();
  if (!places.length) {
    const li = document.createElement("li"); li.className = "none";
    li.textContent = `No matches for “${q}”`;
    search.list.append(li);
  }
  places.forEach((p, i) => {
    const li = document.createElement("li");
    li.id = "sg-" + i; li.setAttribute("role", "option"); li.dataset.i = i;
    const b = document.createElement("b"); b.textContent = p.label;
    const s = document.createElement("span"); s.textContent = p.detail;
    li.append(b, s);
    search.list.append(li);
  });
  search.list.hidden = false; search.input.setAttribute("aria-expanded", "true");
}

function setActive(i) {
  const n = search.places.length; if (!n) return;
  search.active = (i + n) % n;
  [...search.list.children].forEach((li, j) => li.setAttribute("aria-selected", j === search.active));
  search.input.setAttribute("aria-activedescendant", "sg-" + search.active);
  search.list.children[search.active].scrollIntoView({ block: "nearest" });
}

async function choose(p) {
  closeSuggest();
  search.input.value = p.detail && p.kind !== "coordinate" ? `${p.label}, ${p.detail}` : p.label;
  search.input.blur();
  await call("goTo", p.lon, p.lat, p.zoom, search.input.value);
}

search.input.addEventListener("input", () => {
  clearTimeout(search.timer);
  const q = search.input.value.trim();
  if (q.length < 2) { search.seq++; setBusy(false); closeSuggest(); return; }
  search.timer = setTimeout(async () => {
    const seq = ++search.seq; setBusy(true);
    try {
      const c = map.getCenter();
      const places = JSON.parse(await archreg.suggest(q, c.lng, c.lat));
      if (seq === search.seq && document.activeElement === search.input) renderSuggest(places, q);
    } catch (e) {
      console.error(e);
    } finally {
      if (seq === search.seq) setBusy(false);
    }
  }, 250);
});

search.input.addEventListener("keydown", (e) => {
  if (e.key === "ArrowDown") { e.preventDefault(); if (search.list.hidden) search.input.dispatchEvent(new Event("input")); else setActive(search.active + 1); }
  else if (e.key === "ArrowUp") { e.preventDefault(); setActive(search.active - 1); }
  else if (e.key === "Escape") { if (!search.list.hidden) { e.preventDefault(); closeSuggest(); } }
});

// mousedown, not click: picking must happen before the input's blur closes the list.
search.list.addEventListener("mousedown", (e) => {
  e.preventDefault();
  const li = e.target.closest("li[data-i]");
  if (li) choose(search.places[+li.dataset.i]);
});
search.list.addEventListener("mousemove", (e) => {
  const li = e.target.closest("li[data-i]");
  if (li && +li.dataset.i !== search.active) setActive(+li.dataset.i);
});
search.input.addEventListener("blur", () => setTimeout(closeSuggest, 100));

search.form.addEventListener("submit", async (e) => {
  e.preventDefault();
  clearTimeout(search.timer); search.seq++;
  const q = search.input.value.trim(); if (!q) return;
  if (search.active >= 0 && search.places[search.active]) return choose(search.places[search.active]);
  closeSuggest(); setBusy(true);
  try {
    const c = map.getCenter();
    await call("search", q, c.lng, c.lat);
  } finally { setBusy(false); }
});
$("#ask").addEventListener("submit", (e) => {
  e.preventDefault();
  const q = e.target.q.value.trim(); if (!q) return;
  e.target.q.value = ""; call("action", "ask", q, "");
});
$("#import").addEventListener("click", () => $("#file").click());
$("#attach").addEventListener("click", () => $("#file").click());
$("#file").addEventListener("change", async (e) => {
  const f = e.target.files[0]; e.target.value = "";
  if (f) await loadFile(f);
});

// ------------------------------------------------------------ loaders ----
async function loadModel(raw, positions) {
  lastGeometry = { raw, positions };
  await call("loadModel", JSON.stringify(raw), new Uint8Array(positions.buffer, positions.byteOffset, positions.byteLength));
}

async function loadSample() {
  const s = archreg.sample();
  const u8 = s.positions;
  const positions = new Float32Array(u8.buffer, u8.byteOffset, u8.byteLength / 4);
  await loadModel(JSON.parse(s.raw), positions);
}

async function loadFile(file) {
  const ext = file.name.toLowerCase().split(".").pop();
  loading(true, `Reading ${file.name}…`);
  try {
    const buf = new Uint8Array(await file.arrayBuffer());
    const { raw, positions } = ext === "3dm" ? await read3dm(buf) : await readIFC(buf);
    raw.fileName = file.name;
    await loadModel(raw, positions);
  } catch (e) {
    console.error(e);
    toast(`Couldn't read ${file.name}: ${e.message || e}`);
  } finally {
    loading(false);
  }
}

// Collects triangles per element into one Float32Array.
class Soup {
  constructor() { this.chunks = []; this.n = 0; }
  begin() { return this.n; }
  push(x, y, z) { this.chunks.push(x, y, z); this.n += 3; }
  build() { return new Float32Array(this.chunks); }
}

let ifcApi = null;
async function readIFC(buf) {
  const W = await import("web-ifc");
  if (!ifcApi) {
    ifcApi = new W.IfcAPI();
    ifcApi.SetWasmPath("https://cdn.jsdelivr.net/npm/web-ifc@0.0.78/", true);
    await ifcApi.Init(undefined, true);
  }
  const api = ifcApi;
  const id = api.OpenModel(buf, { COORDINATE_TO_ORIGIN: false });
  try {
    const kindOf = new Map([
      [W.IFCSPACE, "space"], [W.IFCWINDOW, "window"], [W.IFCDOOR, "door"], [W.IFCROOF, "roof"],
      [W.IFCSLAB, "slab"], [W.IFCWALL, "wall"], [W.IFCWALLSTANDARDCASE, "wall"], [W.IFCCURTAINWALL, "wall"],
      [W.IFCTRANSPORTELEMENT, "transport"], [W.IFCSITE, "site"], [W.IFCGEOGRAPHICELEMENT, "site"],
    ]);
    const val = (x) => (x && typeof x === "object" && "value" in x ? x.value : x);

    // Storey containment.
    const storeyOf = new Map();
    const storeys = [];
    for (const sid of api.GetLineIDsWithType(id, W.IFCBUILDINGSTOREY)) {
      const s = api.GetLine(id, sid);
      storeys.push({ id: "#" + sid, kind: "storey", name: val(s.Name) || "", attrs: { Elevation: val(s.Elevation) }, off: 0, len: 0 });
    }
    const roofParts = new Set();
    const roofs = new Set(api.GetLineIDsWithType(id, W.IFCROOF));
    for (const rel of [W.IFCRELCONTAINEDINSPATIALSTRUCTURE, W.IFCRELAGGREGATES]) {
      for (const rid of api.GetLineIDsWithType(id, rel)) {
        const r = api.GetLine(id, rid);
        const parent = val(r.RelatingStructure) ?? val(r.RelatingObject);
        for (const c of r.RelatedElements ?? r.RelatedObjects ?? []) {
          if (roofs.has(parent)) { roofParts.add(val(c)); if (storeyOf.has(parent)) storeyOf.set(val(c), storeyOf.get(parent)); continue; }
          storeyOf.set(val(c), "#" + parent);
        }
      }
    }

    // Geometry → triangle soup, three.js Y-up → our Z-up.
    const soup = new Soup();
    const byId = new Map();
    const v = new THREE.Vector3(), mtx = new THREE.Matrix4();
    const take = (mesh) => {
      const eid = mesh.expressID;
      if (byId.has(eid)) return;
      const off = soup.begin();
      const geoms = mesh.geometries;
      for (let i = 0; i < geoms.size(); i++) {
        const pg = geoms.get(i);
        const g = api.GetGeometry(id, pg.geometryExpressID);
        const verts = api.GetVertexArray(g.GetVertexData(), g.GetVertexDataSize());
        const idx = api.GetIndexArray(g.GetIndexData(), g.GetIndexDataSize());
        mtx.fromArray(pg.flatTransformation);
        for (let k = 0; k < idx.length; k++) {
          const j = idx[k] * 6;
          v.set(verts[j], verts[j + 1], verts[j + 2]).applyMatrix4(mtx);
          soup.push(v.x, -v.z, v.y);
        }
        g.delete();
      }
      byId.set(eid, { off, len: soup.begin() - off });
    };
    api.StreamAllMeshes(id, take);
    api.StreamAllMeshesWithTypes(id, [W.IFCSPACE], take);

    // Elements with attributes and property sets.
    const elements = [...storeys];
    const wantProps = new Set(["space", "window", "door", "storey"]);
    for (const [eid, range] of byId) {
      const line = api.GetLine(id, eid);
      const typeCode = api.GetLineType(id, eid);
      const kind = roofParts.has(eid) ? "roof" : kindOf.get(typeCode) || "other";
      const attrs = {};
      for (const k of ["OverallWidth", "OverallHeight", "Elevation"]) if (val(line[k]) != null) attrs[k] = val(line[k]);
      if (wantProps.has(kind)) {
        try {
          for (const ps of await api.properties.getPropertySets(id, eid, true)) {
            for (const p of ps.HasProperties ?? ps.Quantities ?? []) {
              const name = val(p.Name); if (!name) continue;
              const value = val(p.NominalValue) ?? val(p.AreaValue) ?? val(p.LengthValue) ?? val(p.VolumeValue);
              if (value != null && attrs[name] == null) attrs[name] = value;
            }
          }
        } catch { /* property sets are optional */ }
      }
      elements.push({
        id: "#" + eid, kind, ifcType: api.GetNameFromTypeCode(typeCode),
        name: val(line.Name) || "", longName: val(line.LongName) || "", objectType: val(line.ObjectType) || "",
        predefined: val(line.PredefinedType) || "", storey: storeyOf.get(eid) || "", attrs, off: range.off, len: range.len,
      });
    }
    // Storey gross floor areas (quantity sets).
    for (const s of storeys) {
      try {
        for (const ps of await api.properties.getPropertySets(id, Number(s.id.slice(1)), true))
          for (const q of ps.Quantities ?? []) if (val(q.Name) === "GrossFloorArea") s.attrs.GrossFloorArea = val(q.AreaValue);
      } catch {}
    }

    // Units and georeference.
    let unitScale = 1;
    for (const uid of api.GetLineIDsWithType(id, W.IFCSIUNIT)) {
      const u = api.GetLine(id, uid);
      if (val(u.UnitType) === "LENGTHUNIT") unitScale = { MILLI: 0.001, CENTI: 0.01, DECI: 0.1, KILO: 1000 }[val(u.Prefix)] ?? 1;
    }
    for (const uid of api.GetLineIDsWithType(id, W.IFCCONVERSIONBASEDUNIT)) {
      const u = api.GetLine(id, uid);
      if (val(u.UnitType) === "LENGTHUNIT") unitScale = /inch/i.test(val(u.Name)) ? 0.0254 : /foot|feet/i.test(val(u.Name)) ? 0.3048 : unitScale;
    }
    let mapConversion;
    for (const mid of api.GetLineIDsWithType(id, W.IFCMAPCONVERSION)) {
      const m = api.GetLine(id, mid, true);
      mapConversion = {
        eastings: val(m.Eastings) * unitScale, northings: val(m.Northings) * unitScale, height: (val(m.OrthogonalHeight) ?? 0) * unitScale,
        xAxisAbscissa: val(m.XAxisAbscissa) ?? 1, xAxisOrdinate: val(m.XAxisOrdinate) ?? 0, scale: val(m.Scale) ?? 1,
        crs: val(m.TargetCRS?.Name) || "",
      };
    }
    return { raw: { source: "ifc", unitScale, geomScale: 1, elements, mapConversion }, positions: soup.build() };
  } finally {
    api.CloseModel(id);
  }
}

let rhinoMod = null;
async function read3dm(buf) {
  if (!rhinoMod) {
    const rhino3dm = (await import("rhino3dm")).default;
    rhinoMod = await rhino3dm({ locateFile: (f) => "https://cdn.jsdelivr.net/npm/rhino3dm@8.35.0/" + f });
  }
  const rhino = rhinoMod;
  const doc = rhino.File3dm.fromByteArray(buf);
  if (!doc) throw new Error("not a valid .3dm file");
  const objs = doc.objects(), layers = doc.layers();
  const soup = new Soup();
  const elements = [];
  const box = (b) => {
    const [x0, y0, z0] = b.min, [x1, y1, z1] = b.max;
    const c = [[x0,y0,z0],[x1,y0,z0],[x1,y1,z0],[x0,y1,z0],[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1]];
    for (const f of [[0,2,1],[0,3,2],[4,5,6],[4,6,7],[0,1,5],[0,5,4],[2,3,7],[2,7,6],[1,2,6],[1,6,5],[3,0,4],[3,4,7]])
      for (const i of f) soup.push(...c[i]);
  };
  const pushMesh = (mesh) => {
    const vs = mesh.vertices(), fs = mesh.faces();
    for (let i = 0; i < fs.count; i++) {
      const f = fs.get(i); // [a, b, c, d]
      const tri = (a, b, c) => { for (const k of [a, b, c]) soup.push(...vs.get(k)); };
      tri(f[0], f[1], f[2]); if (f[3] !== f[2]) tri(f[0], f[2], f[3]);
    }
  };
  for (let i = 0; i < objs.count; i++) {
    const o = objs.get(i), g = o.geometry(), a = o.attributes();
    const layer = layers.get(a.layerIndex)?.name || "Default";
    const off = soup.begin();
    let meshed = false;
    try {
      if (g instanceof rhino.Mesh) { pushMesh(g); meshed = true; }
      else if (g instanceof rhino.Brep) {
        const faces = g.faces();
        for (let j = 0; j < faces.count; j++) { const m = faces.get(j).getMesh(rhino.MeshType.Any); if (m) { pushMesh(m); meshed = true; } }
      } else if (g instanceof rhino.Extrusion) {
        const m = g.getMesh(rhino.MeshType.Any); if (m) { pushMesh(m); meshed = true; }
      }
    } catch { /* fall back to the bounding box */ }
    if (!meshed) {
      const bb = g.getBoundingBox?.();
      if (!bb || !bb.isValid) continue;
      box(bb);
    }
    elements.push({ id: a.id || "obj" + i, kind: "other", name: a.name || "", layer, off, len: soup.begin() - off });
  }
  const us = doc.settings().modelUnitSystem;
  return { raw: { source: "3dm", rhinoUnitSystem: typeof us === "object" ? us.value : us, elements }, positions: soup.build() };
}

// --------------------------------------------------------------- start ----
loading(true, "Starting…");
try {
  await startGo();
  await call("init");
  const b = map.getBounds();
  await mapReady;
  call("viewport", b.getWest(), b.getSouth(), b.getEast(), b.getNorth(), map.getZoom());
} catch (e) {
  console.error(e);
  toast("Couldn't start the app: " + (e.message || e));
} finally {
  loading(false);
}
