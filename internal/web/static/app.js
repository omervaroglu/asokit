"use strict";

// ---------- theme (per-viewer convenience, best-effort) ---------------------
const THEME_KEY = "asokit-theme";
function savedTheme() { try { return localStorage.getItem(THEME_KEY); } catch { return null; } }
function applyTheme(t) {
  if (t) document.documentElement.setAttribute("data-theme", t);
  else document.documentElement.removeAttribute("data-theme");
}
function currentTheme() {
  const set = document.documentElement.getAttribute("data-theme");
  if (set) return set;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
function toggleTheme() {
  const next = currentTheme() === "dark" ? "light" : "dark";
  applyTheme(next);
  try { localStorage.setItem(THEME_KEY, next); } catch { /* ignore */ }
  if (DATA) render();
}
applyTheme(savedTheme());

// ---------- refs & state ----------------------------------------------------
const dashEl = document.getElementById("dash");
const chartsEl = document.getElementById("charts");
const tableEl = document.getElementById("table");
const setupEl = document.getElementById("setup");
const marketsEl = document.getElementById("markets");
const controlsEl = document.getElementById("controls");
const metaEl = document.getElementById("meta");

let CONFIG = null;     // { initialized, error, app_id, search_limit, markets:[{country,keywords}] }
let DATA = null;       // /api/data response
let CURRENT = "";      // selected market (country code)
let VIEW = "charts";   // charts | table
let SETUP_MODE = "first"; // first | add | edit
let EDIT_COUNTRY = "";

// ---------- control bar + market tabs ---------------------------------------
function mkBtn(label, onClick, opts = {}) {
  const b = document.createElement("button");
  b.className = "ctl" + (opts.primary ? " primary" : "");
  b.textContent = label;
  if (opts.id) b.id = opts.id;
  if (opts.pressed != null) b.setAttribute("aria-pressed", String(opts.pressed));
  b.addEventListener("click", onClick);
  return b;
}
function renderControls(showDashControls) {
  controlsEl.innerHTML = "";
  if (showDashControls) {
    controlsEl.append(
      mkBtn("Run now", runNow, { id: "run" }),
      mkBtn("Settings", () => showSetup("edit")),
      mkBtn("Charts", () => setView("charts"), { pressed: VIEW === "charts" }),
      mkBtn("Table", () => setView("table"), { pressed: VIEW === "table" }),
    );
  }
  controlsEl.append(mkBtn("◐", toggleTheme));
}
function renderMarketTabs() {
  marketsEl.innerHTML = "";
  if (!CONFIG || !CONFIG.initialized) return;
  for (const m of CONFIG.markets) {
    const b = document.createElement("button");
    b.className = "tab";
    b.textContent = m.country.toUpperCase();
    b.setAttribute("aria-selected", String(m.country === CURRENT));
    b.addEventListener("click", () => { CURRENT = m.country; renderMarketTabs(); loadData(); });
    marketsEl.appendChild(b);
  }
  const add = document.createElement("button");
  add.className = "tab add";
  add.textContent = "＋ Market";
  add.addEventListener("click", () => showSetup("add"));
  marketsEl.appendChild(add);
}
function setView(v) {
  VIEW = v;
  chartsEl.classList.toggle("hidden", v !== "charts");
  tableEl.classList.toggle("hidden", v !== "table");
  renderControls(true);
}

// ---------- views -----------------------------------------------------------
function showDashboard() {
  setupEl.classList.add("hidden");
  dashEl.classList.remove("hidden");
  renderControls(true);
  renderMarketTabs();
}
function showSetup(mode) {
  SETUP_MODE = mode;
  dashEl.classList.add("hidden");
  setupEl.classList.remove("hidden");
  renderControls(false);
  marketsEl.innerHTML = "";
  metaEl.textContent = "";

  const title = { first: "Set up tracking", add: "Add a market", edit: `Edit market: ${CURRENT.toUpperCase()}` }[mode];
  document.getElementById("setup-title").textContent = title;
  // app_id is global: editable on first/edit, hidden on add (same app).
  document.getElementById("field-appid").classList.toggle("hidden", mode === "add");
  document.getElementById("hint-appid").classList.toggle("hidden", mode === "add");
  document.getElementById("f-cancel").classList.toggle("hidden", !(CONFIG && CONFIG.initialized));
  document.getElementById("f-remove").classList.toggle("hidden",
    !(mode === "edit" && CONFIG && CONFIG.markets.length > 1));

  const err = document.getElementById("f-error");
  err.textContent = (mode === "first" && CONFIG && CONFIG.error) ? CONFIG.error : "";

  const appid = document.getElementById("f-appid");
  const country = document.getElementById("f-country");
  const kw = document.getElementById("f-keywords");

  appid.value = CONFIG && CONFIG.app_id ? CONFIG.app_id : "";
  if (mode === "edit") {
    const m = (CONFIG.markets || []).find((x) => x.country === CURRENT);
    EDIT_COUNTRY = CURRENT;
    country.value = CURRENT;
    country.readOnly = false; // editable; changing it is confirmed on save
    kw.value = m ? m.keywords.join("\n") : "";
    kw.focus();
  } else {
    EDIT_COUNTRY = "";
    country.value = "";
    country.readOnly = false;
    kw.value = "";
    (mode === "add" ? country : appid).focus();
  }
}

// ---------- data fetching ---------------------------------------------------
async function loadConfig() {
  const res = await fetch("api/config", { cache: "no-store" });
  if (!res.ok) throw new Error("HTTP " + res.status);
  CONFIG = await res.json();
  if (CONFIG.initialized) {
    const countries = CONFIG.markets.map((m) => m.country);
    if (!countries.includes(CURRENT)) CURRENT = countries[0];
    showDashboard();
    await loadData();
  } else {
    showSetup("first");
  }
}
async function loadData() {
  const res = await fetch("api/data?country=" + encodeURIComponent(CURRENT), { cache: "no-store" });
  if (!res.ok) throw new Error("HTTP " + res.status);
  DATA = await res.json();
  render();
}

// ---------- setup form actions ----------------------------------------------
document.getElementById("f-save").addEventListener("click", saveConfig);
document.getElementById("f-cancel").addEventListener("click", () => {
  if (CONFIG && CONFIG.initialized) { showDashboard(); render(); }
});
document.getElementById("f-remove").addEventListener("click", removeMarket);

function strip(m) { return { country: m.country, keywords: m.keywords }; }

async function postConfig(appID, markets) {
  const res = await fetch("api/config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appID || 0, markets }),
  });
  return { ok: res.ok, status: res.status, body: await res.json() };
}

async function saveConfig() {
  const country = document.getElementById("f-country").value.trim().toLowerCase();
  const keywords = document.getElementById("f-keywords").value
    .split(/[\n,;]+/).map((s) => s.trim()).filter(Boolean);
  const appID = SETUP_MODE === "add" && CONFIG
    ? CONFIG.app_id
    : parseInt(document.getElementById("f-appid").value, 10);
  const errEl = document.getElementById("f-error");
  errEl.textContent = "";

  let markets;
  const existing = (CONFIG && CONFIG.markets ? CONFIG.markets : []).map(strip);
  if (SETUP_MODE === "edit") {
    if (country !== EDIT_COUNTRY && country) {
      const ok = window.confirm(
        `Change this market from ${EDIT_COUNTRY.toUpperCase()} to ${country.toUpperCase()}?\n\n` +
        `The ${EDIT_COUNTRY.toUpperCase()} history stays saved but won't show here until you add ${EDIT_COUNTRY.toUpperCase()} back, ` +
        `and ${country.toUpperCase()} starts its own history.`);
      if (!ok) return;
    }
    markets = existing.map((m) => (m.country === EDIT_COUNTRY ? { country, keywords } : m));
  } else if (SETUP_MODE === "add") {
    markets = [...existing, { country, keywords }];
  } else {
    markets = [{ country, keywords }];
  }

  const btn = document.getElementById("f-save");
  btn.disabled = true;
  try {
    const r = await postConfig(appID, markets);
    if (!r.ok) { errEl.textContent = r.body.error || ("HTTP " + r.status); return; }
    CONFIG = r.body;
    CURRENT = country;
    showDashboard();
    await loadData();
    toast("Saved");
  } catch (e) {
    errEl.textContent = e.message;
  } finally {
    btn.disabled = false;
  }
}

async function removeMarket() {
  if (!CONFIG || CONFIG.markets.length <= 1) return;
  if (!window.confirm(`Remove the ${CURRENT.toUpperCase()} market from tracking? Its stored history stays in the database.`)) return;
  const markets = CONFIG.markets.map(strip).filter((m) => m.country !== CURRENT);
  const r = await postConfig(CONFIG.app_id, markets);
  if (!r.ok) { toast(r.body.error || "Remove failed"); return; }
  CONFIG = r.body;
  CURRENT = CONFIG.markets[0].country;
  showDashboard();
  await loadData();
  toast("Market removed");
}

async function runNow() {
  const btn = document.getElementById("run");
  if (btn) { btn.disabled = true; btn.textContent = "Running…"; }
  try {
    const res = await fetch("api/run?country=" + encodeURIComponent(CURRENT), { method: "POST" });
    const body = await res.json();
    if (!res.ok) toast(body.error ? "Run failed: " + body.error : "Run failed");
    else toast(`Stored ${body.stored} keyword(s)` + (body.failures ? `, ${body.failures} failed` : ""));
    await loadData();
  } catch (e) {
    toast("Run failed: " + e.message);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = "Run now"; }
  }
}

// ---------- render dashboard ------------------------------------------------
function render() {
  if (!DATA) return;
  metaEl.textContent =
    `app ${DATA.app_id} · ${(DATA.country || "").toUpperCase()} · ${DATA.keywords.length} keyword(s)`;
  renderCharts();
  renderTable();
  setView(VIEW);
}

function delta(cur, prev) {
  if (!cur) return { cls: "none", label: "" };
  if (!cur.found) {
    if (prev && prev.found) return { cls: "out", label: "dropped out" };
    return { cls: "none", label: "" };
  }
  if (!prev) return { cls: "new", label: "first run" };
  if (!prev.found) return { cls: "new", label: "new" };
  const d = prev.position - cur.position;
  if (d > 0) return { cls: "up", label: "▲ " + d };
  if (d < 0) return { cls: "down", label: "▼ " + (-d) };
  return { cls: "flat", label: "=" };
}
const fmtDate = (iso) => new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
const fmtDateTime = (iso) => new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

function renderCharts() {
  chartsEl.innerHTML = "";
  const anyData = DATA.keywords.some((k) => k.series.length > 0);
  if (!anyData) {
    const d = document.createElement("div");
    d.className = "empty";
    d.innerHTML = `No snapshots yet for <b>${escapeHTML((DATA.country || "").toUpperCase())}</b>.<br>`;
    d.appendChild(mkBtn("Run now", runNow, { id: "run", primary: true }));
    chartsEl.appendChild(d);
    return;
  }
  for (const kw of DATA.keywords) {
    const card = document.createElement("div");
    card.className = "card";
    const d = delta(kw.current, kw.previous);
    const rankHTML = kw.current && kw.current.found
      ? `<span class="rank">#${kw.current.position}</span>`
      : `<span class="rank none">—</span>`;
    const deltaHTML = d.label ? `<span class="delta ${d.cls}">${d.label}</span>` : "";
    const asof = kw.current ? `as of ${fmtDateTime(kw.current.ts)}` : "no data yet";
    card.innerHTML =
      `<p class="kw">${escapeHTML(kw.keyword)}</p>` +
      `<div class="rankrow">${rankHTML}${deltaHTML}</div>` +
      chartSVG(kw.series) +
      `<div class="asof">${asof}</div>`;
    chartsEl.appendChild(card);
    wireHover(card.querySelector("svg.chart"));
  }
}

// ---------- SVG small-multiple line chart (rank over time, y inverted) ------
const VW = 300, VH = 120, PX0 = 30, PX1 = 292, PY0 = 12, PY1 = 98;
const PLOTW = PX1 - PX0, PLOTH = PY1 - PY0;

function chartSVG(series) {
  const found = series.filter((p) => p.found);
  if (!found.length) {
    return `<svg class="chart" viewBox="0 0 ${VW} ${VH}" role="img" aria-label="no ranked data">
      <text x="${VW / 2}" y="${VH / 2}" text-anchor="middle" fill="var(--muted)" font-size="12">not in top results</text></svg>`;
  }
  const times = series.map((p) => Date.parse(p.ts));
  const tMin = Math.min(...times), tMax = Math.max(...times);
  const xOf = (i, t) => {
    if (series.length === 1) return PX0 + PLOTW / 2;
    if (tMax === tMin) return PX0 + (PLOTW * i) / (series.length - 1);
    return PX0 + (PLOTW * (t - tMin)) / (tMax - tMin);
  };
  const maxPos = Math.max(...found.map((p) => p.position));
  const yMax = niceMax(maxPos);
  const yOf = (pos) => PY0 + (PLOTH * (pos - 1)) / (yMax - 1);

  let grid = "";
  for (const t of yTicks(yMax)) {
    const y = yOf(t).toFixed(1);
    grid += `<line x1="${PX0}" y1="${y}" x2="${PX1}" y2="${y}" stroke="var(--grid)" stroke-width="1"/>`;
    grid += `<text x="${PX0 - 6}" y="${y}" text-anchor="end" dominant-baseline="middle" fill="var(--muted)" font-size="10">#${t}</text>`;
  }

  let dPath = "", dots = "", hits = "", penDown = false;
  series.forEach((p, i) => {
    const x = xOf(i, times[i]);
    if (!p.found) { penDown = false; return; }
    const y = yOf(p.position);
    dPath += `${penDown ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)} `;
    penDown = true;
    dots += `<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="3" fill="var(--series)"/>`;
    hits += `<circle class="hit" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="10" fill="transparent" data-ts="${p.ts}" data-pos="${p.position}"/>`;
  });

  const xlabels =
    `<text x="${PX0}" y="${VH - 2}" fill="var(--muted)" font-size="10">${fmtDate(series[0].ts)}</text>` +
    (series.length > 1
      ? `<text x="${PX1}" y="${VH - 2}" text-anchor="end" fill="var(--muted)" font-size="10">${fmtDate(series[series.length - 1].ts)}</text>`
      : "");

  return `<svg class="chart" viewBox="0 0 ${VW} ${VH}" role="img" aria-label="rank over time">
    ${grid}
    <line class="crosshair" x1="0" y1="${PY0}" x2="0" y2="${PY1}" stroke="var(--baseline)" stroke-width="1" style="display:none"/>
    <path d="${dPath.trim()}" fill="none" stroke="var(--series)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
    ${dots}${hits}${xlabels}
  </svg>`;
}
function niceMax(m) {
  if (m <= 1) return 2;
  if (m <= 5) return m + 1;
  if (m <= 10) return 10;
  if (m <= 20) return 20;
  if (m <= 50) return Math.ceil(m / 10) * 10;
  if (m <= 100) return Math.ceil(m / 25) * 25;
  return Math.ceil(m / 50) * 50;
}
function yTicks(yMax) {
  const set = new Set([1, yMax]);
  set.add(Math.round((1 + yMax) / 2));
  return [...set].sort((a, b) => a - b);
}

// ---------- hover crosshair + tooltip ---------------------------------------
const tip = document.getElementById("tip");
function wireHover(svg) {
  if (!svg) return;
  const hits = [...svg.querySelectorAll("circle.hit")];
  if (!hits.length) return;
  const cross = svg.querySelector(".crosshair");
  svg.addEventListener("mousemove", (e) => {
    const rect = svg.getBoundingClientRect();
    const vbX = ((e.clientX - rect.left) / rect.width) * VW;
    let best = null, bestDist = Infinity;
    for (const h of hits) {
      const dist = Math.abs(parseFloat(h.getAttribute("cx")) - vbX);
      if (dist < bestDist) { bestDist = dist; best = h; }
    }
    if (!best) return;
    const cx = parseFloat(best.getAttribute("cx"));
    cross.setAttribute("x1", cx); cross.setAttribute("x2", cx); cross.style.display = "block";
    tip.textContent = `#${best.dataset.pos} · ${fmtDateTime(best.dataset.ts)}`;
    tip.style.left = e.clientX + "px"; tip.style.top = e.clientY + "px"; tip.style.opacity = "1";
  });
  svg.addEventListener("mouseleave", () => { cross.style.display = "none"; tip.style.opacity = "0"; });
}

// ---------- table view ------------------------------------------------------
function renderTable() {
  let rows = "";
  for (const kw of DATA.keywords) {
    const d = delta(kw.current, kw.previous);
    const pos = kw.current && kw.current.found ? "#" + kw.current.position : "—";
    const asof = kw.current ? fmtDateTime(kw.current.ts) : "—";
    rows += `<tr><td>${escapeHTML(kw.keyword)}</td><td class="num">${pos}</td>` +
      `<td class="num">${d.label || "—"}</td><td class="num">${kw.series.length}</td><td>${asof}</td></tr>`;
  }
  tableEl.innerHTML = `<table class="view"><thead><tr>` +
    `<th>Keyword</th><th>Position</th><th>Trend</th><th>Snapshots</th><th>As of</th>` +
    `</tr></thead><tbody>${rows}</tbody></table>`;
}

// ---------- toast -----------------------------------------------------------
let toastTimer = null;
function toast(msg) {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("show"), 2600);
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// ---------- go --------------------------------------------------------------
loadConfig().catch((err) => {
  controlsEl.innerHTML = "";
  controlsEl.append(mkBtn("◐", toggleTheme));
  dashEl.classList.remove("hidden");
  chartsEl.innerHTML = `<div class="empty">Failed to load: ${escapeHTML(err.message)}</div>`;
});
