// gl.js — minimal raw-WebGL renderer for the cockpit view.
// World mapping: netrek (x, y) -> world (x, z), Y is up, everything lives at y=0.
"use strict";

const TEAM_COLORS = {
  F: [1.0, 0.84, 0.31], R: [0.94, 0.33, 0.31], K: [0.40, 0.73, 0.42],
  O: [0.39, 0.71, 0.96], I: [0.62, 0.62, 0.62],
};

const FOV = 65 * Math.PI / 180;
const EYE_HEIGHT = 160;
const PLANET_RADIUS = 600;        // ORBDIST is 800, so orbits skim the surface
const PLANET_FADE = 18000, PLANET_MAX = 25000;
const SHIP_FADE = 10000, SHIP_MAX = 20000; // matches the minimap radar range
const BOOM_MAX = 30000; // distant battle flashes, but not cross-galaxy
const SHIP_SCALE = 140;

// ---------- matrix helpers (column-major mat4) ----------
function mat4Perspective(fovy, aspect, near, far) {
  const f = 1 / Math.tan(fovy / 2), nf = 1 / (near - far);
  return [f / aspect, 0, 0, 0, 0, f, 0, 0, 0, 0, (far + near) * nf, -1,
          0, 0, 2 * far * near * nf, 0];
}
function mat4Mul(a, b) {
  const o = new Array(16);
  for (let c = 0; c < 4; c++) for (let r = 0; r < 4; r++) {
    o[c * 4 + r] = a[r] * b[c * 4] + a[4 + r] * b[c * 4 + 1] +
                   a[8 + r] * b[c * 4 + 2] + a[12 + r] * b[c * 4 + 3];
  }
  return o;
}
function mat4LookYaw(eye, yaw) {
  // view matrix for camera at eye looking along (cos yaw, 0, sin yaw)
  const fx = Math.cos(yaw), fz = Math.sin(yaw);
  const rx = -fz, rz = fx; // right = forward x up
  return [rx, 0, -fx, 0,
          0, 1, 0, 0,
          rz, 0, -fz, 0,
          -(rx * eye[0] + rz * eye[2]), -eye[1], fx * eye[0] + fz * eye[2], 1];
}
function mat4Model(x, y, z, yaw, s) {
  const c = Math.cos(yaw), n = Math.sin(yaw);
  return [c * s, 0, n * s, 0, 0, s, 0, 0, -n * s, 0, c * s, 0, x, y, z, 1];
}

// ---------- shaders ----------
const MESH_VS = `
attribute vec3 aPos; attribute vec3 aNorm;
uniform mat4 uPV, uModel; varying vec3 vNorm; varying vec3 vWorld;
void main() { vec4 w = uModel * vec4(aPos, 1.0);
  gl_Position = uPV * w; vWorld = w.xyz;
  vNorm = mat3(uModel[0].xyz, uModel[1].xyz, uModel[2].xyz) * aNorm; }`;
const MESH_FS = `
precision mediump float;
uniform vec4 uColor; uniform vec3 uLight; uniform float uEmissive;
uniform vec3 uEye; uniform float uSpec;
uniform vec3 uBoomPos[4]; uniform vec4 uBoomCol[4]; // rgb premultiplied, w = radius
varying vec3 vNorm; varying vec3 vWorld;
void main() {
  vec3 n = normalize(vNorm);
  vec3 c = uColor.rgb * (0.30 + 0.75 * max(dot(n, uLight), 0.0));
  // Blinn-Phong sun glint: white, view-dependent, strength per object type
  vec3 h = normalize(uLight + normalize(uEye - vWorld));
  c += uSpec * pow(max(dot(n, h), 0.0), 32.0);
  for (int i = 0; i < 4; i++) {
    vec3 dv = uBoomPos[i] - vWorld;
    float dist = max(length(dv), 1.0);
    float att = max(1.0 - dist / max(uBoomCol[i].w, 1.0), 0.0);
    c += uColor.rgb * uBoomCol[i].rgb * max(dot(n, dv / dist), 0.0) * att * att;
  }
  gl_FragColor = vec4(mix(c, uColor.rgb, uEmissive), uColor.a);
}`;
const POINT_VS = `
attribute vec3 aPos; attribute vec4 aColor; attribute float aSize;
uniform mat4 uPV; varying vec4 vColor;
void main() { gl_Position = uPV * vec4(aPos, 1.0); vColor = aColor;
  gl_PointSize = aSize; }`;
const POINT_FS = `
precision mediump float; varying vec4 vColor;
void main() { vec2 d = gl_PointCoord - 0.5; float r = length(d) * 2.0;
  if (r > 1.0) discard;
  gl_FragColor = vec4(vColor.rgb, vColor.a * smoothstep(1.0, 0.6, r)); }`;
const LINE_VS = `
attribute vec3 aPos; attribute vec4 aColor; uniform mat4 uPV; varying vec4 vColor;
void main() { gl_Position = uPV * vec4(aPos, 1.0); vColor = aColor; }`;
const LINE_FS = `
precision mediump float; varying vec4 vColor; void main() { gl_FragColor = vColor; }`;

function makeProgram(gl, vsSrc, fsSrc) {
  const mk = (type, src) => {
    const s = gl.createShader(type);
    gl.shaderSource(s, src); gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS))
      throw new Error(gl.getShaderInfoLog(s));
    return s;
  };
  const p = gl.createProgram();
  gl.attachShader(p, mk(gl.VERTEX_SHADER, vsSrc));
  gl.attachShader(p, mk(gl.FRAGMENT_SHADER, fsSrc));
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS))
    throw new Error(gl.getProgramInfoLog(p));
  return p;
}

// ---------- geometry ----------
function makeSphere(lon, lat) {
  const verts = [], idx = [];
  for (let j = 0; j <= lat; j++) {
    const t = j / lat * Math.PI, st = Math.sin(t), ct = Math.cos(t);
    for (let i = 0; i <= lon; i++) {
      const f = i / lon * 2 * Math.PI;
      const x = st * Math.cos(f), y = ct, z = st * Math.sin(f);
      verts.push(x, y, z, x, y, z);
    }
  }
  for (let j = 0; j < lat; j++) for (let i = 0; i < lon; i++) {
    const a = j * (lon + 1) + i, b = a + lon + 1;
    idx.push(a, b, a + 1, b, b + 1, a + 1);
  }
  return { verts: new Float32Array(verts), idx: new Uint16Array(idx) };
}
function makeShipMesh() {
  // flattened dart pointing +X; per-face normals
  const nose = [1.6, 0, 0], top = [-0.6, 0.45, 0], bot = [-0.6, -0.25, 0],
        left = [-1.0, 0, -0.9], right = [-1.0, 0, 0.9];
  const tri = (a, b, c) => {
    const ux = b[0] - a[0], uy = b[1] - a[1], uz = b[2] - a[2];
    const vx = c[0] - a[0], vy = c[1] - a[1], vz = c[2] - a[2];
    let nx = uy * vz - uz * vy, ny = uz * vx - ux * vz, nz = ux * vy - uy * vx;
    const l = Math.hypot(nx, ny, nz) || 1; nx /= l; ny /= l; nz /= l;
    return [...a, nx, ny, nz, ...b, nx, ny, nz, ...c, nx, ny, nz];
  };
  const verts = [
    ...tri(nose, left, top), ...tri(nose, top, right),
    ...tri(nose, bot, left), ...tri(nose, right, bot),
    ...tri(top, left, right), ...tri(bot, right, left),
  ];
  return { verts: new Float32Array(verts), count: verts.length / 6 };
}

// ---------- renderer ----------
function Renderer(canvas) {
  const gl = canvas.getContext("webgl", { antialias: true });
  if (!gl) throw new Error("WebGL unavailable");
  this.gl = gl;
  this.canvas = canvas;

  this.meshProg = makeProgram(gl, MESH_VS, MESH_FS);
  this.pointProg = makeProgram(gl, POINT_VS, POINT_FS);
  this.lineProg = makeProgram(gl, LINE_VS, LINE_FS);
  this.loc = {
    mesh: { aPos: gl.getAttribLocation(this.meshProg, "aPos"),
            aNorm: gl.getAttribLocation(this.meshProg, "aNorm"),
            uPV: gl.getUniformLocation(this.meshProg, "uPV"),
            uModel: gl.getUniformLocation(this.meshProg, "uModel"),
            uColor: gl.getUniformLocation(this.meshProg, "uColor"),
            uLight: gl.getUniformLocation(this.meshProg, "uLight"),
            uEmissive: gl.getUniformLocation(this.meshProg, "uEmissive"),
            uEye: gl.getUniformLocation(this.meshProg, "uEye"),
            uSpec: gl.getUniformLocation(this.meshProg, "uSpec"),
            // uniform arrays must be looked up via their first element
            uBoomPos: gl.getUniformLocation(this.meshProg, "uBoomPos[0]"),
            uBoomCol: gl.getUniformLocation(this.meshProg, "uBoomCol[0]") },
    point: { aPos: gl.getAttribLocation(this.pointProg, "aPos"),
             aColor: gl.getAttribLocation(this.pointProg, "aColor"),
             aSize: gl.getAttribLocation(this.pointProg, "aSize"),
             uPV: gl.getUniformLocation(this.pointProg, "uPV") },
    line: { aPos: gl.getAttribLocation(this.lineProg, "aPos"),
            aColor: gl.getAttribLocation(this.lineProg, "aColor"),
            uPV: gl.getUniformLocation(this.lineProg, "uPV") },
  };

  const sphere = makeSphere(28, 18);
  this.sphereBuf = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, this.sphereBuf);
  gl.bufferData(gl.ARRAY_BUFFER, sphere.verts, gl.STATIC_DRAW);
  this.sphereIdx = gl.createBuffer();
  gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, this.sphereIdx);
  gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, sphere.idx, gl.STATIC_DRAW);
  this.sphereCount = sphere.idx.length;

  const ship = makeShipMesh();
  this.shipBuf = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, this.shipBuf);
  gl.bufferData(gl.ARRAY_BUFFER, ship.verts, gl.STATIC_DRAW);
  this.shipCount = ship.count;

  this.pointBuf = gl.createBuffer();
  this.lineBuf = gl.createBuffer();

  // starfield: fixed random points on a big sphere (slightly biased up so the
  // horizon isn't empty), drawn with rotation only
  const stars = [];
  for (let i = 0; i < 900; i++) {
    const az = Math.random() * 2 * Math.PI;
    const el = (Math.random() - 0.35) * Math.PI * 0.9;
    const r = 40000;
    const b = 0.4 + Math.random() * 0.6, sz = Math.random() < 0.08 ? 3 : 1.6;
    stars.push(r * Math.cos(el) * Math.cos(az), r * Math.sin(el),
               r * Math.cos(el) * Math.sin(az), b, b, b * (0.9 + Math.random() * 0.1),
               1, sz);
  }
  this.starData = new Float32Array(stars);
  this.starBuf = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, this.starBuf);
  gl.bufferData(gl.ARRAY_BUFFER, this.starData, gl.STATIC_DRAW);

  gl.enable(gl.DEPTH_TEST);
  gl.enable(gl.BLEND);
  gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
  gl.clearColor(0.008, 0.008, 0.02, 1);
}

Renderer.prototype.resize = function () {
  const c = this.canvas, dpr = window.devicePixelRatio || 1;
  const w = Math.floor(c.clientWidth * dpr), h = Math.floor(c.clientHeight * dpr);
  if (c.width !== w || c.height !== h) { c.width = w; c.height = h; }
  this.gl.viewport(0, 0, w, h);
};

// camera: netrek pos (x, y), yaw = dir (radians, velocity (cos, sin) in netrek coords)
// boomLights: up to 4 explosion point lights [{x, y, i(ntensity), r(adius)}]
Renderer.prototype.begin = function (cx, cy, yaw, boomLights) {
  const gl = this.gl;
  this.resize();
  this.aspect = this.canvas.width / this.canvas.height;
  this.proj = mat4Perspective(FOV, this.aspect, 20, 120000);
  this.eye = [cx, EYE_HEIGHT, cy];
  this.pv = mat4Mul(this.proj, mat4LookYaw(this.eye, yaw));
  this.pvRot = mat4Mul(this.proj, mat4LookYaw([0, 0, 0], yaw));
  this.pxFactor = (this.canvas.height / 2) / Math.tan(FOV / 2);
  gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
  this.points = [];   // accumulated point sprites: x,y,z, r,g,b,a, size
  this.lines = [];    // accumulated lines: x,y,z, r,g,b,a per vertex

  // upload explosion lights once per frame (unused slots have radius 0)
  const pos = new Float32Array(12), col = new Float32Array(16);
  (boomLights || []).slice(0, 4).forEach((b, i) => {
    pos[i * 3] = b.x; pos[i * 3 + 1] = 0; pos[i * 3 + 2] = b.y;
    col[i * 4] = 1.0 * b.i; col[i * 4 + 1] = 0.6 * b.i; col[i * 4 + 2] = 0.3 * b.i;
    col[i * 4 + 3] = b.r;
  });
  gl.useProgram(this.meshProg);
  gl.uniform3fv(this.loc.mesh.uBoomPos, pos);
  gl.uniform4fv(this.loc.mesh.uBoomCol, col);
  gl.uniform3fv(this.loc.mesh.uEye, this.eye);

  this.drawStars();
};

// world -> screen px; null if behind camera
Renderer.prototype.project = function (x, y, z) {
  const m = this.pv;
  const w = m[3] * x + m[7] * y + m[11] * z + m[15];
  if (w <= 0) return null;
  const sx = (m[0] * x + m[4] * y + m[8] * z + m[12]) / w;
  const sy = (m[1] * x + m[5] * y + m[9] * z + m[13]) / w;
  const dpr = window.devicePixelRatio || 1;
  return [(sx * 0.5 + 0.5) * this.canvas.width / dpr,
          (0.5 - sy * 0.5) * this.canvas.height / dpr];
};

Renderer.prototype.drawStars = function () {
  const gl = this.gl, L = this.loc.point;
  gl.depthMask(false);
  gl.useProgram(this.pointProg);
  gl.uniformMatrix4fv(L.uPV, false, this.pvRot);
  gl.bindBuffer(gl.ARRAY_BUFFER, this.starBuf);
  gl.enableVertexAttribArray(L.aPos);
  gl.enableVertexAttribArray(L.aColor);
  gl.enableVertexAttribArray(L.aSize);
  gl.vertexAttribPointer(L.aPos, 3, gl.FLOAT, false, 32, 0);
  gl.vertexAttribPointer(L.aColor, 4, gl.FLOAT, false, 32, 12);
  gl.vertexAttribPointer(L.aSize, 1, gl.FLOAT, false, 32, 28);
  gl.drawArrays(gl.POINTS, 0, this.starData.length / 8);
  gl.depthMask(true);
};

Renderer.prototype.drawMesh = function (buf, count, indexed, model, color, emissive, spec) {
  const gl = this.gl, L = this.loc.mesh;
  gl.useProgram(this.meshProg);
  gl.uniformMatrix4fv(L.uPV, false, this.pv);
  gl.uniformMatrix4fv(L.uModel, false, model);
  gl.uniform4fv(L.uColor, color);
  gl.uniform3f(L.uLight, 0.45, 0.72, -0.53);
  gl.uniform1f(L.uEmissive, emissive || 0);
  gl.uniform1f(L.uSpec, spec || 0);
  gl.bindBuffer(gl.ARRAY_BUFFER, buf);
  gl.enableVertexAttribArray(L.aPos);
  gl.enableVertexAttribArray(L.aNorm);
  gl.vertexAttribPointer(L.aPos, 3, gl.FLOAT, false, 24, 0);
  gl.vertexAttribPointer(L.aNorm, 3, gl.FLOAT, false, 24, 12);
  if (indexed) {
    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, this.sphereIdx);
    gl.drawElements(gl.TRIANGLES, count, gl.UNSIGNED_SHORT, 0);
  } else {
    gl.drawArrays(gl.TRIANGLES, 0, count);
  }
};

// planet: shrink with distance, become a dot, fade out, disappear
Renderer.prototype.drawPlanet = function (px, py, team, dist) {
  if (dist > PLANET_MAX) return 0;
  const alpha = dist < PLANET_FADE ? 1 :
    1 - (dist - PLANET_FADE) / (PLANET_MAX - PLANET_FADE);
  const color = TEAM_COLORS[team] || TEAM_COLORS.I;
  const pxRadius = PLANET_RADIUS / Math.max(dist, 1) * this.pxFactor;
  if (pxRadius < 2) {
    this.points.push(px, 0, py, color[0], color[1], color[2], alpha,
                     Math.max(2.5, pxRadius * 2));
  } else {
    const spin = 0; // planets don't need to spin; sphere is uniform
    this.drawMesh(this.sphereBuf, this.sphereCount, true,
                  mat4Model(px, 0, py, spin, PLANET_RADIUS),
                  [color[0], color[1], color[2], alpha], 0, 0.1);
  }
  return alpha;
};

Renderer.prototype.drawShip = function (px, py, yaw, team, dist, dim) {
  if (dist > SHIP_MAX) return 0;
  let alpha = dist < SHIP_FADE ? 1 : 1 - (dist - SHIP_FADE) / (SHIP_MAX - SHIP_FADE);
  if (dim) alpha *= 0.35;
  const color = TEAM_COLORS[team] || TEAM_COLORS.I;
  const pxSize = SHIP_SCALE / Math.max(dist, 1) * this.pxFactor;
  if (pxSize < 2) {
    this.points.push(px, 0, py, color[0], color[1], color[2], alpha, 3);
  } else {
    this.drawMesh(this.shipBuf, this.shipCount, false,
                  mat4Model(px, 0, py, yaw, SHIP_SCALE),
                  [color[0], color[1], color[2], alpha], 0, 0.6);
  }
  return alpha;
};

Renderer.prototype.drawTorp = function (px, py, team, dist) {
  if (dist > BOOM_MAX) return;
  const alpha = dist < SHIP_MAX ? 1 : 1 - (dist - SHIP_MAX) / (BOOM_MAX - SHIP_MAX);
  const color = TEAM_COLORS[team] || TEAM_COLORS.I;
  const size = Math.min(10, Math.max(2.5, 90 / Math.max(dist, 1) * this.pxFactor));
  this.points.push(px, 0, py, Math.min(1, color[0] + .3), Math.min(1, color[1] + .3),
                   Math.min(1, color[2] + .3), alpha, size);
};

Renderer.prototype.drawExplosion = function (px, py, age, scale) { // age 0..1
  if (Math.hypot(px - this.eye[0], py - this.eye[2]) > BOOM_MAX) return;
  const r = (100 + age * 900) * (scale || 1);
  this.gl.depthMask(false); // translucent shell must not occlude torps/beams
  this.drawMesh(this.sphereBuf, this.sphereCount, true,
                mat4Model(px, 0, py, 0, r), [1, .6, .15, (1 - age) * .8], 1);
  this.gl.depthMask(true);
};

Renderer.prototype.drawPhaser = function (x1, y1, x2, y2, team, alpha) {
  const d1 = Math.hypot(x1 - this.eye[0], y1 - this.eye[2]);
  const d2 = Math.hypot(x2 - this.eye[0], y2 - this.eye[2]);
  if (Math.min(d1, d2) > BOOM_MAX) return; // beams flash like explosions do
  const c = TEAM_COLORS[team] || TEAM_COLORS.I;
  this.lines.push(x1, EYE_HEIGHT - 60, y1, c[0], c[1], c[2], alpha,
                  x2, 0, y2, c[0], c[1], c[2], alpha);
};

Renderer.prototype.finish = function () {
  const gl = this.gl;
  if (this.lines.length) {
    const L = this.loc.line;
    gl.useProgram(this.lineProg);
    gl.uniformMatrix4fv(L.uPV, false, this.pv);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.lineBuf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(this.lines), gl.DYNAMIC_DRAW);
    gl.enableVertexAttribArray(L.aPos);
    gl.enableVertexAttribArray(L.aColor);
    gl.vertexAttribPointer(L.aPos, 3, gl.FLOAT, false, 28, 0);
    gl.vertexAttribPointer(L.aColor, 4, gl.FLOAT, false, 28, 12);
    gl.drawArrays(gl.LINES, 0, this.lines.length / 7);
  }
  if (this.points.length) {
    const L = this.loc.point;
    gl.depthMask(false);
    gl.useProgram(this.pointProg);
    gl.uniformMatrix4fv(L.uPV, false, this.pv);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.pointBuf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(this.points), gl.DYNAMIC_DRAW);
    gl.enableVertexAttribArray(L.aPos);
    gl.enableVertexAttribArray(L.aColor);
    gl.enableVertexAttribArray(L.aSize);
    gl.vertexAttribPointer(L.aPos, 3, gl.FLOAT, false, 32, 0);
    gl.vertexAttribPointer(L.aColor, 4, gl.FLOAT, false, 32, 12);
    gl.vertexAttribPointer(L.aSize, 1, gl.FLOAT, false, 32, 28);
    gl.drawArrays(gl.POINTS, 0, this.points.length / 8);
    gl.depthMask(true);
  }
};
