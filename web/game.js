// game.js — net, input, HUD, galactic map. Rendering primitives live in gl.js.
"use strict";

const TEAM_NAMES = { F: "Federation", R: "Romulan", K: "Klingon", O: "Orion" };
const TEAM_CSS = { F: "#ffd54f", R: "#ef5350", K: "#66bb6a", O: "#64b5f6", I: "#9e9e9e" };
const SHIP_TYPES = ["SC", "DD", "CA", "BB", "AS", "SB", "GA"];
const GWIDTH = 100000;

const glCanvas = document.getElementById("gl");
const overlay = document.getElementById("overlay");
const mapCanvas = document.getElementById("map");
const R = new Renderer(glCanvas);

let ws = null, myId = -1;
let planets = [];              // static part from welcome; o/a updated by snaps
let prevSnap = null, curSnap = null, snapAt = 0;
let joined = false, mapOn = false;
let mouse = { x: innerWidth / 2, y: innerHeight / 2 };
let phaserFx = [];             // {fx,fy,tx,ty,t,until}
let booms = [];                // {x,y,big,at}
let selTeam = null, selShip = "CA";

// ---------- join UI ----------
const joinDiv = document.getElementById("join");
const teamsP = document.getElementById("teams");
const shipsP = document.getElementById("ships");
const joinMsg = document.getElementById("joinMsg");
const nameInput = document.getElementById("name");
nameInput.value = localStorage.nfpName || "";

function buildJoinUI(counts) {
  teamsP.innerHTML = "";
  for (const tm of ["F", "R", "K", "O"]) {
    const b = document.createElement("button");
    b.className = `l7-btn l7-btn--outline btn-sm sel-${tm}`; // sel-* gated by .sel
    const n = counts ? (counts[tm] || 0) : 0;
    b.textContent = `${TEAM_NAMES[tm]} (${n})`;
    b.style.color = TEAM_CSS[tm]; // team colors are the game's, not the system's
    b.disabled = n >= 32;
    b.onclick = () => { selTeam = tm; refreshSel(); };
    b.dataset.team = tm;
    teamsP.appendChild(b);
  }
  if (!shipsP.childElementCount) {
    for (const st of SHIP_TYPES) {
      const b = document.createElement("button");
      b.className = "l7-btn l7-btn--outline btn-sm sel-A"; // amber selection tint
      b.textContent = st;
      b.onclick = () => { selShip = st; refreshSel(); };
      b.dataset.ship = st;
      shipsP.appendChild(b);
    }
  }
  refreshSel();
}
function refreshSel() {
  for (const b of teamsP.children) b.classList.toggle("sel", b.dataset.team === selTeam);
  for (const b of shipsP.children) b.classList.toggle("sel", b.dataset.ship === selShip);
}
const botPanel = document.getElementById("botPanel");
function buildBotControls(target, inline) {
  if (target.childElementCount) return;
  const add = (label, msg, color) => {
    const b = document.createElement("button");
    b.className = "l7-btn l7-btn--outline btn-sm";
    b.textContent = label;
    if (color) b.style.color = color;
    b.onclick = () => send(msg);
    target.appendChild(b);
  };
  if (inline) {
    const lbl = document.createElement("span");
    lbl.textContent = "bots: ";
    target.appendChild(lbl);
  }
  for (const tm of ["F", "R", "K", "O"])
    add("+" + tm, { t: "addbot", team: tm }, TEAM_CSS[tm]);
  if (!inline) target.appendChild(document.createElement("br"));
  for (const tm of ["F", "R", "K", "O"])
    add("−" + tm, { t: "removebot", team: tm }, TEAM_CSS[tm]);
  if (!inline) target.appendChild(document.createElement("br"));
  add("BALANCE", { t: "balancebots" });
  add("FILL", { t: "fillbots" });
  add("CLEAR", { t: "clearbots" });
}
function buildBotUI() {
  buildBotControls(document.getElementById("bots"), true);
  buildBotControls(document.getElementById("botPanelButtons"), false);
}
// ---------- player list: 4 team columns, kills descending ----------
const playerListDiv = document.getElementById("playerList");
function togglePlayerList(show) {
  const on = show !== undefined ? show : playerListDiv.style.display !== "block";
  playerListDiv.style.display = on ? "block" : "none";
  if (on) renderPlayerList();
}
function renderPlayerList() {
  if (!curSnap) return;
  const byTeam = { F: [], R: [], K: [], O: [] };
  for (const p of curSnap.players) byTeam[p.tm]?.push(p);
  playerListDiv.textContent = "";
  const cols = document.createElement("div");
  cols.className = "cols";
  for (const [tm, label] of [["F", "Federation"], ["R", "Romulan"],
                             ["K", "Klingon"], ["O", "Orion"]]) {
    const col = document.createElement("div");
    const h = document.createElement("h3");
    h.textContent = label;
    h.style.color = TEAM_CSS[tm];
    col.appendChild(h);
    for (const p of byTeam[tm].sort((a, b) => (b.ki || 0) - (a.ki || 0) || a.i - b.i)) {
      const row = document.createElement("div");
      row.className = "row" + (p.st === "dead" ? " dead" : "");
      row.style.color = TEAM_CSS[tm];
      row.textContent = `${String(p.i).padStart(3)}  ${p.nm.padEnd(16).slice(0, 16)} ` +
                        `${(p.ki || 0).toFixed(2).padStart(6)}`;
      col.appendChild(row);
    }
    cols.appendChild(col);
  }
  playerListDiv.appendChild(cols);
}

function toggleBotPanel(show) {
  const on = show !== undefined ? show : botPanel.style.display !== "block";
  botPanel.style.display = on ? "block" : "none";
}

document.getElementById("go").onclick = tryJoin;
nameInput.onkeydown = e => { if (e.key === "Enter") tryJoin(); e.stopPropagation(); };
function tryJoin() {
  const name = nameInput.value.trim() || "guest";
  if (!selTeam) { joinMsg.textContent = "pick a team"; return; }
  localStorage.nfpName = name;
  send({ t: "join", name, team: selTeam, ship: selShip });
}

// ---------- net ----------
function send(m) { if (ws && ws.readyState === 1) ws.send(JSON.stringify(m)); }

function connect() {
  // resolve relative to the page so a reverse proxy can mount us under a
  // subpath (e.g. caddy handle_path /netrekfp/*)
  const base = location.pathname.endsWith("/")
    ? location.pathname : location.pathname.replace(/[^/]*$/, "");
  ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") +
    location.host + base + "ws");
  ws.onmessage = e => handle(JSON.parse(e.data));
  ws.onclose = () => {
    joined = false; joinDiv.style.display = "flex";
    joinMsg.textContent = "disconnected — retrying...";
    setTimeout(connect, 2000);
  };
}

function handle(m) {
  switch (m.t) {
    case "welcome":
      planets = m.planets;
      buildJoinUI(m.counts);
      break;
    case "joined":
      myId = m.id;
      joined = true;
      joinDiv.style.display = "none";
      joinMsg.textContent = "";
      break;
    case "deny":
      joinMsg.textContent = m.reason;
      break;
    case "snap": {
      prevSnap = curSnap; curSnap = m; snapAt = performance.now();
      for (const p of m.planets) {
        const pl = planets[p.n]; pl.o = p.o; pl.a = p.a; pl.f = p.f;
      }
      for (const ph of m.phasers || [])
        phaserFx.push({ ...ph, until: snapAt + 300 });
      for (const b of m.booms || [])
        booms.push({ ...b, at: snapAt });
      for (const txt of m.msgs || []) logMsg(txt);
      if (playerListDiv.style.display === "block") renderPlayerList();
      if (m.you.st === "dead" && joined) {
        joined = false;
        joinDiv.style.display = "flex";
        joinMsg.textContent = "ship destroyed";
        buildJoinUI(m.counts);
      } else if (m.you.st === "alive" && !joined) {
        // self-heal if the joined reply was lost: the server thinks we fly
        myId = m.you.i;
        joined = true;
        joinDiv.style.display = "none";
      }
      break;
    }
  }
}

// ---------- messages ----------
const msgsDiv = document.getElementById("msgs");
function logMsg(text) {
  const d = document.createElement("div");
  d.textContent = text;
  msgsDiv.appendChild(d);
  while (msgsDiv.childElementCount > 7) msgsDiv.firstChild.remove();
  setTimeout(() => { d.style.transition = "opacity 1s"; d.style.opacity = 0; }, 9000);
}

// ---------- input ----------
function bearingFromScreen(mx, my, you) {
  // ray through pixel onto the y=0 plane; fall back to horizontal angle offset
  const w = innerWidth, h = innerHeight;
  const tanF = Math.tan(65 * Math.PI / 360);
  const ndcX = (mx / w) * 2 - 1, ndcY = 1 - (my / h) * 2;
  const camX = ndcX * tanF * (w / h), camY = ndcY * tanF;
  const yaw = you.d;
  // camera space: forward (cos,0,sin), right (-sin,0,cos), up (0,1,0)
  const dx = Math.cos(yaw) - camX * Math.sin(yaw);
  const dz = Math.sin(yaw) + camX * Math.cos(yaw);
  const dy = camY;
  if (dy < -0.02) {
    const t = 160 / -dy; // EYE_HEIGHT
    const wx = you.x + dx * t, wz = you.y + dz * t;
    return Math.atan2(wz - you.y, wx - you.x);
  }
  return Math.atan2(dz, dx);
}

addEventListener("mousemove", e => { mouse.x = e.clientX; mouse.y = e.clientY; });
addEventListener("contextmenu", e => e.preventDefault());
addEventListener("mousedown", e => {
  if (!joined || !curSnap) return;
  const you = interpYou();
  const d = bearingFromScreen(e.clientX, e.clientY, you);
  if (mapOn) {
    // right-click: course toward that point; left-click: lock nearest planet
    const g = mapToGalaxy(e.clientX, e.clientY);
    if (!g) return;
    if (e.button === 2) {
      send({ t: "course", d: Math.atan2(g[1] - you.y, g[0] - you.x) });
    } else if (e.button === 0) {
      send({ t: "lock", v: nearestPlanetTo(g[0], g[1]) });
    }
    return;
  }
  if (e.button === 0) send({ t: "torp", d });
  else if (e.button === 1) { send({ t: "phaser", d }); e.preventDefault(); }
  else if (e.button === 2) send({ t: "course", d });
});

addEventListener("keydown", e => {
  if (!joined) return;
  if (e.key >= "0" && e.key <= "9") { send({ t: "speed", v: +e.key }); return; }
  const you = curSnap ? interpYou() : null;
  switch (e.key) {
    case "=": send({ t: "speed", v: 99 }); break; // server clamps to maxspeed
    case "s": send({ t: "shields" }); break;
    case "t": if (you) send({ t: "torp", d: bearingFromScreen(mouse.x, mouse.y, you) }); break;
    case "f": if (you) send({ t: "phaser", d: bearingFromScreen(mouse.x, mouse.y, you) }); break;
    case "p": togglePlayerList(); break;
    case "o": send({ t: "orbit" }); break;
    case "b": send({ t: "bomb" }); break;
    case "z": send({ t: "beamup" }); break;
    case "x": send({ t: "beamdown" }); break;
    case "R": send({ t: "repair" }); break;
    case "c": send({ t: "cloak" }); break;
    case "d": send({ t: "det" }); break;
    case "l": { // lock: nearest planet to the pointer (map) or under the reticle (3D)
      if (!curSnap) break;
      if (mapOn) {
        const g = mapToGalaxy(mouse.x, mouse.y);
        if (g) send({ t: "lock", v: nearestPlanetTo(g[0], g[1]) });
        break;
      }
      const y2 = interpYou();
      let best = -1, bd = 80; // screen px
      for (const pl of planets) {
        if (Math.hypot(pl.x - y2.x, pl.y - y2.y) > 30000) continue;
        const s = R.project(pl.x, 0, pl.y);
        if (!s) continue;
        const d = Math.hypot(s[0] - mouse.x, s[1] - mouse.y);
        if (d < bd) { bd = d; best = pl.n; }
      }
      if (best >= 0) send({ t: "lock", v: best });
      break;
    }
    case "m": mapOn = !mapOn; mapCanvas.style.display = mapOn ? "block" : "none"; break;
    case "\\": toggleBotPanel(); break;
    case "Q": send({ t: "selfdestruct" }); break;
    case "Escape":
      if (botPanel.style.display === "block") { toggleBotPanel(false); break; }
      send({ t: "quit" });
      break;
  }
});

// ---------- interpolation ----------
function lerp(a, b, f) { return a + (b - a) * f; }
function lerpAngle(a, b, f) {
  let d = b - a;
  while (d > Math.PI) d -= 2 * Math.PI;
  while (d < -Math.PI) d += 2 * Math.PI;
  return a + d * f;
}
function interpFrac() {
  return Math.min(1, (performance.now() - snapAt) / 100);
}
function interpYou() {
  const y = curSnap.you;
  if (!prevSnap || prevSnap.you.st !== "alive") return y;
  const f = interpFrac(), p = prevSnap.you;
  return { ...y, x: lerp(p.x, y.x, f), y: lerp(p.y, y.y, f), d: lerpAngle(p.d, y.d, f) };
}
function interpList(cur, prev, f) {
  const prevById = {};
  if (prev) for (const p of prev) prevById[p.i] = p;
  return cur.map(c => {
    const p = prevById[c.i];
    if (!p) return c;
    return { ...c, x: lerp(p.x, c.x, f), y: lerp(p.y, c.y, f),
             d: c.d !== undefined ? lerpAngle(p.d, c.d, f) : undefined };
  });
}

// ---------- HUD ----------
const hudLeft = document.getElementById("hudLeft");
const hudTop = document.getElementById("hudTop");
const alertDiv = document.getElementById("alert");

function bar(label, val, max, warnHigh, color, text) {
  const pct = Math.max(0, Math.min(100, val / max * 100));
  const bad = warnHigh ? pct > 70 : pct < 30;
  const col = color || (bad ? "var(--danger)" : "var(--green)");
  return `${label} <span class="bar"><i style="width:${pct}%;background:${col}"></i></span>` +
         ` ${text !== undefined ? text : Math.round(val)}<br>`;
}

function updateHUD(you, players) {
  const compass = ((you.d * 180 / Math.PI + 90) % 360 + 360) % 360;
  hudLeft.innerHTML =
    bar("WARP", you.sp, you.maxsp, false, "var(--amber)", `${you.sp}/${you.maxsp}`) +
    bar("SHLD", you.sh, you.maxsh) +
    bar("HULL", you.maxdm - you.dm, you.maxdm) +
    bar("FUEL", you.fu, you.maxfu) +
    bar("WTMP", you.wt, you.maxwt, true) +
    bar("ETMP", you.et, you.maxet, true) +
    `HDG ${compass.toFixed(0)}&deg; &nbsp; TORPS ${you.tp} &nbsp; ARMIES ${you.ar} &nbsp; KILLS ${you.ki.toFixed(2)}` +
    (you.shup ? " &nbsp; [SHIELDS]" : "") + (you.cl ? " [CLOAK]" : "") +
    (you.rep ? " [REPAIR]" : "") + (you.bmb ? " [BOMBING]" : "");

  let top = curSnap.tmode.on
    ? `T-MODE &nbsp; ${fmtTime(curSnap.tmode.left)}` : "pickup (need 4v4 for T-mode)";
  if (curSnap.tmode.on) {
    const counts = {};
    for (const pl of planets) counts[pl.o] = (counts[pl.o] || 0) + 1;
    top += "<br>" + [["FED", "F"], ["ROM", "R"], ["KLI", "K"], ["ORI", "O"], ["IND", "I"]]
      .map(([nm, l]) =>
        `<span style="color:${TEAM_CSS[l]}">${nm}: ${counts[l] || 0}</span>`)
      .join(", ");
  }
  if (you.lk >= 0)
    top += `<br><span style="color:var(--amber)">LOCKED &rarr; ${planets[you.lk].name}</span>`;
  if (you.sd > 0)
    top = `<span style="color:var(--danger);font-weight:bold">SELF DESTRUCT IN ${you.sd}</span><br>` + top;
  if (you.orb >= 0) {
    const pl = planets[you.orb];
    const fl = (pl.f & 8 ? "HOME " : "") + (pl.f & 1 ? "REPAIR " : "") +
               (pl.f & 2 ? "FUEL " : "") + (pl.f & 4 ? "AGRI" : "");
    top += `<br>orbiting <span class="${pl.o}">${pl.name}</span> — ` +
           `${pl.a} armies ${fl ? "(" + fl.trim() + ")" : ""}`;
  }
  hudTop.innerHTML = top;
  // keep the combat log below however many status rows are showing
  msgsDiv.style.top = hudTop.offsetTop + hudTop.offsetHeight + 8 + "px";

  let nearest = 1e9;
  for (const p of players) {
    if (p.i === myId || p.tm === you.tm || p.st !== "alive") continue;
    nearest = Math.min(nearest, Math.hypot(p.x - you.x, p.y - you.y));
  }
  const [txt, col] = nearest < 7000 ? ["RED ALERT", "var(--danger)"] :
    nearest < 15000 ? ["YELLOW ALERT", "var(--amber)"] : ["CONDITION GREEN", "var(--green)"];
  alertDiv.textContent = txt;
  alertDiv.style.color = col;
}
function fmtTime(s) {
  return `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;
}

// ---------- galactic map ----------
let mapRect = null;
function nearestPlanetTo(gx, gy) {
  let best = 0, bd = Infinity;
  for (const pl of planets) {
    const d = Math.hypot(pl.x - gx, pl.y - gy);
    if (d < bd) { bd = d; best = pl.n; }
  }
  return best;
}

function mapToGalaxy(sx, sy) {
  if (!mapRect) return null;
  const [ox, oy, sz] = mapRect;
  const gx = (sx - ox) / sz * GWIDTH, gy = (sy - oy) / sz * GWIDTH;
  if (gx < 0 || gy < 0 || gx > GWIDTH || gy > GWIDTH) return null;
  return [gx, gy];
}
function fit2d(c) {
  const dpr = devicePixelRatio || 1;
  const w = innerWidth * dpr, h = innerHeight * dpr;
  if (c.width !== w || c.height !== h) { c.width = w; c.height = h; }
  const ctx = c.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, innerWidth, innerHeight);
  return ctx;
}

function drawMap(you, players) {
  const ctx = fit2d(mapCanvas);
  ctx.fillStyle = "rgba(0,0,0,0.88)";
  ctx.fillRect(0, 0, innerWidth, innerHeight);
  const sz = Math.min(innerWidth, innerHeight) - 60;
  const ox = (innerWidth - sz) / 2, oy = (innerHeight - sz) / 2;
  mapRect = [ox, oy, sz];
  ctx.strokeStyle = "#37474f";
  ctx.strokeRect(ox, oy, sz, sz);
  ctx.beginPath(); // quadrant lines
  ctx.moveTo(ox + sz / 2, oy); ctx.lineTo(ox + sz / 2, oy + sz);
  ctx.moveTo(ox, oy + sz / 2); ctx.lineTo(ox + sz, oy + sz / 2);
  ctx.strokeStyle = "#1c262b"; ctx.stroke();

  ctx.font = "11px Consolas, monospace";
  ctx.textAlign = "center";
  for (const pl of planets) {
    const x = ox + pl.x / GWIDTH * sz, y = oy + pl.y / GWIDTH * sz;
    ctx.fillStyle = TEAM_CSS[pl.o] || TEAM_CSS.I;
    ctx.beginPath(); ctx.arc(x, y, 3.5, 0, 7); ctx.fill();
    if (curSnap.you.lk === pl.n) { // lock ring
      ctx.strokeStyle = "#ffb74d";
      ctx.beginPath(); ctx.arc(x, y, 8, 0, 7); ctx.stroke();
    }
    ctx.fillText(`${pl.name.split(" ")[0]} ${pl.a}`, x, y + 14);
    if (pl.f & 4) { ctx.fillStyle = "#8d6e63"; ctx.fillText("agri", x, y + 25); }
  }
  for (const p of players) {
    if (p.st !== "alive" || p.cl) continue;
    const x = ox + p.x / GWIDTH * sz, y = oy + p.y / GWIDTH * sz;
    ctx.strokeStyle = ctx.fillStyle = TEAM_CSS[p.tm];
    ctx.save();
    ctx.translate(x, y); ctx.rotate(p.d + Math.PI / 2);
    ctx.beginPath(); ctx.moveTo(0, -6); ctx.lineTo(4, 5); ctx.lineTo(-4, 5); ctx.closePath();
    if (p.i === myId) { ctx.fill(); } else { ctx.stroke(); }
    ctx.restore();
    ctx.fillText(p.nm, x, y - 9);
  }
  ctx.fillStyle = "#cfd8dc";
  ctx.textAlign = "left";
  ctx.fillText(curSnap.tmode.on ? `T-MODE ${fmtTime(curSnap.tmode.left)}` : "pickup",
               ox + 6, oy + 16);
}

// ---------- minimap: local radar, north-up like the galactic map ----------
const MINI_SIZE = 170, MINI_RANGE = 20000;
function drawMinimap(ctx, you, players) {
  const S = MINI_SIZE, ox = innerWidth - 12 - S, oy = 10;
  const cx = ox + S / 2, cy = oy + S / 2;
  ctx.save();
  ctx.fillStyle = "rgba(8,9,7,0.55)";
  ctx.fillRect(ox, oy, S, S);
  ctx.strokeStyle = "#3f4038"; // --border-1
  ctx.strokeRect(ox, oy, S, S);
  ctx.beginPath(); ctx.rect(ox, oy, S, S); ctx.clip();
  ctx.strokeStyle = "#23241f"; // half-range ring
  ctx.beginPath(); ctx.arc(cx, cy, S / 4, 0, 7); ctx.stroke();
  const px = (x, y) => [cx + (x - you.x) / MINI_RANGE * (S / 2),
                        cy + (y - you.y) / MINI_RANGE * (S / 2)];
  for (const pl of planets) {
    if (Math.abs(pl.x - you.x) > MINI_RANGE || Math.abs(pl.y - you.y) > MINI_RANGE) continue;
    const [x, y] = px(pl.x, pl.y);
    ctx.fillStyle = TEAM_CSS[pl.o] || TEAM_CSS.I;
    ctx.beginPath(); ctx.arc(x, y, 4, 0, 7); ctx.fill();
    if (curSnap.you.lk === pl.n) {
      ctx.strokeStyle = "#ffb74d";
      ctx.beginPath(); ctx.arc(x, y, 7, 0, 7); ctx.stroke();
    }
  }
  const wedge = (x, y, d, color, alpha, r) => {
    ctx.save();
    ctx.translate(x, y); ctx.rotate(d + Math.PI / 2);
    ctx.globalAlpha = alpha;
    ctx.fillStyle = color;
    ctx.beginPath(); ctx.moveTo(0, -r); ctx.lineTo(r * .75, r * .85);
    ctx.lineTo(-r * .75, r * .85); ctx.closePath(); ctx.fill();
    ctx.restore();
  };
  for (const p of players) {
    if (p.st !== "alive" || p.i === myId) continue;
    if (Math.abs(p.x - you.x) > MINI_RANGE || Math.abs(p.y - you.y) > MINI_RANGE) continue;
    const [x, y] = px(p.x, p.y);
    wedge(x, y, p.d, TEAM_CSS[p.tm], p.cl ? 0.4 : 1, 4);
  }
  wedge(cx, cy, you.d, "#eceff1", 1, 5); // own ship
  ctx.restore();
}

// ---------- overlay (labels + reticle) ----------
function drawOverlay(labels, you, players) {
  const ctx = fit2d(overlay);
  ctx.font = "12px Consolas, monospace";
  ctx.textAlign = "center";
  for (const l of labels) {
    ctx.globalAlpha = l.alpha;
    ctx.fillStyle = l.color;
    ctx.fillText(l.text, l.x, l.y);
  }
  ctx.globalAlpha = 1;
  // reticle
  ctx.strokeStyle = "#b0bec5";
  ctx.beginPath();
  ctx.moveTo(mouse.x - 10, mouse.y); ctx.lineTo(mouse.x - 3, mouse.y);
  ctx.moveTo(mouse.x + 3, mouse.y); ctx.lineTo(mouse.x + 10, mouse.y);
  ctx.moveTo(mouse.x, mouse.y - 10); ctx.lineTo(mouse.x, mouse.y - 3);
  ctx.moveTo(mouse.x, mouse.y + 3); ctx.lineTo(mouse.x, mouse.y + 10);
  ctx.stroke();
  // course marker: where the ship is heading
  const ahead = R.project(you.x + Math.cos(you.d) * 8000, 60, you.y + Math.sin(you.d) * 8000);
  if (ahead) {
    ctx.strokeStyle = "#546e7a";
    ctx.strokeRect(ahead[0] - 4, ahead[1] - 4, 8, 8);
  }
  drawMinimap(ctx, you, players);
}

// ---------- main loop ----------
function frame() {
  requestAnimationFrame(frame);
  if (!curSnap) return;
  const you = interpYou();
  const f = interpFrac();
  const players = interpList(curSnap.players, prevSnap && prevSnap.players, f);
  const torps = interpList(curSnap.torps, prevSnap && prevSnap.torps, f);
  const now = performance.now();

  R.begin(you.x, you.y, you.d);
  const labels = [];

  for (const pl of planets) {
    const dist = Math.hypot(pl.x - you.x, pl.y - you.y);
    const alpha = R.drawPlanet(pl.x, pl.y, pl.o, dist);
    if (alpha > 0.05 && dist < 20000) {
      const s = R.project(pl.x, 700, pl.y);
      if (s) labels.push({ x: s[0], y: s[1] - 8, text: `${pl.name} ${pl.a}`,
                           color: TEAM_CSS[pl.o] || TEAM_CSS.I, alpha });
    }
  }
  for (const p of players) {
    if (p.i === myId || p.st !== "alive") continue;
    const dist = Math.hypot(p.x - you.x, p.y - you.y);
    const alpha = R.drawShip(p.x, p.y, p.d, p.tm, dist, !!p.cl);
    if (alpha > 0.05 && dist < 9000) {
      const s = R.project(p.x, 260, p.y);
      if (s) labels.push({ x: s[0], y: s[1] - 6, text: `${p.nm} (${p.s})`,
                           color: TEAM_CSS[p.tm], alpha });
    }
  }
  for (const tp of torps) {
    const dist = Math.hypot(tp.x - you.x, tp.y - you.y);
    R.drawTorp(tp.x, tp.y, tp.tm, dist);
  }
  phaserFx = phaserFx.filter(ph => ph.until > now);
  for (const ph of phaserFx)
    R.drawPhaser(ph.fx, ph.fy, ph.tx, ph.ty, ph.tm, (ph.until - now) / 300);
  booms = booms.filter(b => now - b.at < 700);
  for (const b of booms)
    R.drawExplosion(b.x, b.y, (now - b.at) / 700);
  R.finish();

  if (mapOn) {
    fit2d(overlay); // hide 3D labels/reticle under the map
    drawMap(you, players);
  } else {
    drawOverlay(labels, you, players);
  }
  updateHUD(curSnap.you, curSnap.players);
}

buildJoinUI(null);
buildBotUI();
connect();
requestAnimationFrame(frame);
