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
});
map.addControl(new maplibregl.NavigationControl({ showCompass: true }), "bottom-right");
const pin = new maplibregl.Marker({ color: "#d32f2f" });
const empty = { type: "FeatureCollection", features: [] };
let mapReady = new Promise((r) => map.on("load", r));

map.on("load", () => {
  map.addSource("plans", { type: "geojson", data: empty });
  map.addSource("regs", { type: "geojson", data: empty });
  map.addSource("footprint", { type: "geojson", data: empty });
  map.addLayer({ id: "plans-fill", type: "fill", source: "plans",
    paint: { "fill-color": "#d9a77a", "fill-opacity": ["case", ["get", "selected"], 0.35, 0.08] } });
  map.addLayer({ id: "plans-line", type: "line", source: "plans",
    paint: { "line-color": "#1d1b20", "line-width": ["case", ["get", "selected"], 2.5, 1.2],
             "line-dasharray": ["case", ["get", "pdfOnly"], ["literal", [2, 2]], ["literal", [1, 0]]] } });
  map.addLayer({ id: "regs-fill", type: "fill", source: "regs",
    paint: { "fill-color": ["match", ["get", "kind"], "prickmark", "#c62828", "korsmark", "#ef8f00", "#5c6bc0"], "fill-opacity": 0.22 } });
  map.addLayer({ id: "regs-line", type: "line", source: "regs",
    paint: { "line-color": ["match", ["get", "kind"], "prickmark", "#c62828", "korsmark", "#ef8f00", "#5c6bc0"], "line-width": 1.5, "line-dasharray": [2, 1] } });
  map.addLayer({ id: "footprint-fill", type: "fill", source: "footprint",
    paint: { "fill-color": ["case", ["get", "highlight"], "#d32f2f", "#f57c00"], "fill-opacity": 0.6 } });
  map.addLayer({ id: "footprint-line", type: "line", source: "footprint", paint: { "line-color": "#7a3a00", "line-width": 2 } });
  map.on("mouseenter", "footprint-fill", () => (map.getCanvas().style.cursor = "move"));
  map.on("mouseleave", "footprint-fill", () => (map.getCanvas().style.cursor = ""));
});

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
  if (!lastGeometry) return;
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

function show(view) {
  const is3d = view === "3d";
  $("#map").hidden = is3d; $("#viewer").hidden = !is3d;
  for (const b of document.querySelectorAll(".seg button")) b.setAttribute("aria-pressed", String(b.dataset.view === view));
  if (is3d) { initViewer(); resizeViewer(); } else map.resize();
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
  lastFootprint = v.map.footprint;
  if (v.map.pin) pin.setLngLat(v.map.pin).addTo(map); else pin.remove();
  if (v.map.flyTo) map.flyTo({ center: [v.map.flyTo[0], v.map.flyTo[1]], zoom: v.map.flyTo[2] });

  if (v.model) {
    if (v.model.clear) { lastGeometry = null; buildMeshes(); }
    if (v.model.rebuild) buildMeshes();
    if (v.model.fit) fitViewer();
    if (viewer.renderer) highlight(v.model.highlight || []);
  }
  if (v.focus) show(v.focus);
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
    await mapReady; map.getSource("footprint").setData(v.map.footprint); lastFootprint = v.map.footprint;
  } finally { liveBusy = false; }
});

$("#search").addEventListener("submit", (e) => {
  e.preventDefault();
  const q = e.target.q.value.trim(); if (q) call("search", q);
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
