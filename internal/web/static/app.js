"use strict";

// ---- theme toggle (per-viewer convenience, best-effort) --------------------
const THEME_KEY = "asokit-theme";
function savedTheme() {
  try { return localStorage.getItem(THEME_KEY); } catch { return null; }
}
function applyTheme(t) {
  if (t) document.documentElement.setAttribute("data-theme", t);
  else document.documentElement.removeAttribute("data-theme");
}
function currentTheme() {
  const set = document.documentElement.getAttribute("data-theme");
  if (set) return set;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
applyTheme(savedTheme());
document.getElementById("theme").addEventListener("click", () => {
  const next = currentTheme() === "dark" ? "light" : "dark";
  applyTheme(next);
  try { localStorage.setItem(THEME_KEY, next); } catch { /* ignore */ }
  render(); // redraw so SVG strokes pick up new tokens cleanly
});

// ---- view toggle -----------------------------------------------------------
const chartsEl = document.getElementById("charts");
const tableEl = document.getElementById("table");
const btnCharts = document.getElementById("view-charts");
const btnTable = document.getElementById("view-table");
function setView(v) {
  const charts = v === "charts";
  chartsEl.classList.toggle("hidden", !charts);
  tableEl.classList.toggle("hidden", charts);
  btnCharts.setAttribute("aria-pressed", String(charts));
  btnTable.setAttribute("aria-pressed", String(!charts));
}
btnCharts.addEventListener("click", () => setView("charts"));
btnTable.addEventListener("click", () => setView("table"));

// ---- data ------------------------------------------------------------------
let DATA = null;

async function load() {
  const res = await fetch("api/data", { cache: "no-store" });
  if (!res.ok) throw new Error("HTTP " + res.status);
  DATA = await res.json();
  render();
}

function render() {
  if (!DATA) return;
  document.getElementById("meta").textContent =
    `app ${DATA.app_id} · ${DATA.country.toUpperCase()} · ${DATA.keywords.length} keyword(s)`;
  renderCharts();
  renderTable();
}

// trend badge from current + previous points
function delta(cur, prev) {
  if (!cur) return { cls: "none", label: "" };
  if (!cur.found) {
    if (prev && prev.found) return { cls: "out", label: "dropped out" };
    return { cls: "none", label: "" };
  }
  if (!prev) return { cls: "new", label: "first run" };
  if (!prev.found) return { cls: "new", label: "new" };
  const d = prev.position - cur.position; // positive => moved up (improved)
  if (d > 0) return { cls: "up", label: "▲ " + d };
  if (d < 0) return { cls: "down", label: "▼ " + (-d) };
  return { cls: "flat", label: "=" };
}

const fmtDate = (iso) =>
  new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
const fmtDateTime = (iso) =>
  new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

function renderCharts() {
  chartsEl.innerHTML = "";
  if (!DATA.keywords.length) {
    chartsEl.innerHTML = `<div class="empty">No keywords configured.</div>`;
    return;
  }
  const anyData = DATA.keywords.some((k) => k.series.length > 0);
  if (!anyData) {
    chartsEl.innerHTML = `<div class="empty">No snapshots yet — run <code>asokit run</code>, then refresh.</div>`;
    return;
  }

  for (const kw of DATA.keywords) {
    const card = document.createElement("div");
    card.className = "card";
    const d = delta(kw.current, kw.previous);
    const rankHTML = kw.current && kw.current.found
      ? `<span class="rank">#${kw.current.position}</span>`
      : `<span class="rank none">—</span>`;
    const deltaHTML = d.label
      ? `<span class="delta ${d.cls}">${d.label}</span>` : "";
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

// ---- SVG small-multiple line chart (rank over time, y inverted) ------------
const VW = 300, VH = 120;
const PX0 = 30, PX1 = 292, PY0 = 12, PY1 = 98;
const PLOTW = PX1 - PX0, PLOTH = PY1 - PY0;

function chartSVG(series) {
  const found = series.filter((p) => p.found);
  if (!found.length) {
    return `<svg class="chart" viewBox="0 0 ${VW} ${VH}" role="img" aria-label="no ranked data">
      <text x="${VW / 2}" y="${VH / 2}" text-anchor="middle" fill="var(--muted)" font-size="12">not in top results</text></svg>`;
  }

  // x by time; fall back to index when all timestamps are equal / single point
  const times = series.map((p) => Date.parse(p.ts));
  const tMin = Math.min(...times), tMax = Math.max(...times);
  const xOf = (i, t) => {
    if (series.length === 1) return PX0 + PLOTW / 2;
    if (tMax === tMin) return PX0 + (PLOTW * i) / (series.length - 1);
    return PX0 + (PLOTW * (t - tMin)) / (tMax - tMin);
  };

  // y domain: 1 (best, top) .. niceMax (worst, bottom)
  const maxPos = Math.max(...found.map((p) => p.position));
  const yMax = niceMax(maxPos);
  const yOf = (pos) => PY0 + (PLOTH * (pos - 1)) / (yMax - 1);

  // gridlines + y labels at integer-ish ticks
  const ticks = yTicks(yMax);
  let grid = "";
  for (const t of ticks) {
    const y = yOf(t).toFixed(1);
    grid += `<line x1="${PX0}" y1="${y}" x2="${PX1}" y2="${y}" stroke="var(--grid)" stroke-width="1"/>`;
    grid += `<text x="${PX0 - 6}" y="${y}" text-anchor="end" dominant-baseline="middle" fill="var(--muted)" font-size="10">#${t}</text>`;
  }

  // build line path, breaking at not-found gaps
  let dPath = "", pts = "", dots = "";
  let penDown = false;
  series.forEach((p, i) => {
    const x = xOf(i, times[i]);
    if (!p.found) { penDown = false; return; }
    const y = yOf(p.position);
    dPath += `${penDown ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)} `;
    penDown = true;
    dots += `<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="3" fill="var(--series)"/>`;
    pts += `<circle class="hit" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="10" fill="transparent"
      data-ts="${p.ts}" data-pos="${p.position}"/>`;
  });

  // x labels: first and last date
  const xlabels =
    `<text x="${PX0}" y="${VH - 2}" fill="var(--muted)" font-size="10">${fmtDate(series[0].ts)}</text>` +
    (series.length > 1
      ? `<text x="${PX1}" y="${VH - 2}" text-anchor="end" fill="var(--muted)" font-size="10">${fmtDate(series[series.length - 1].ts)}</text>`
      : "");

  return `<svg class="chart" viewBox="0 0 ${VW} ${VH}" role="img" aria-label="rank over time">
    ${grid}
    <line class="crosshair" x1="0" y1="${PY0}" x2="0" y2="${PY1}" stroke="var(--baseline)" stroke-width="1" style="display:none"/>
    <path d="${dPath.trim()}" fill="none" stroke="var(--series)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
    ${dots}${pts}${xlabels}
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

// ---- hover crosshair + tooltip ---------------------------------------------
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
      const cx = parseFloat(h.getAttribute("cx"));
      const dist = Math.abs(cx - vbX);
      if (dist < bestDist) { bestDist = dist; best = h; }
    }
    if (!best) return;
    const cx = parseFloat(best.getAttribute("cx"));
    cross.setAttribute("x1", cx); cross.setAttribute("x2", cx);
    cross.style.display = "block";
    tip.textContent = `#${best.dataset.pos} · ${fmtDateTime(best.dataset.ts)}`;
    tip.style.left = e.clientX + "px";
    tip.style.top = e.clientY + "px";
    tip.style.opacity = "1";
  });
  svg.addEventListener("mouseleave", () => {
    cross.style.display = "none";
    tip.style.opacity = "0";
  });
}

// ---- table view (accessibility / exact values) ----------------------------
function renderTable() {
  if (!DATA.keywords.length) { tableEl.innerHTML = ""; return; }
  let rows = "";
  for (const kw of DATA.keywords) {
    const d = delta(kw.current, kw.previous);
    const pos = kw.current && kw.current.found ? "#" + kw.current.position : "—";
    const asof = kw.current ? fmtDateTime(kw.current.ts) : "—";
    rows += `<tr>
      <td>${escapeHTML(kw.keyword)}</td>
      <td class="num">${pos}</td>
      <td class="num">${d.label || "—"}</td>
      <td class="num">${kw.series.length}</td>
      <td>${asof}</td>
    </tr>`;
  }
  tableEl.innerHTML = `<table class="view">
    <thead><tr><th>Keyword</th><th>Position</th><th>Trend</th><th>Snapshots</th><th>As of</th></tr></thead>
    <tbody>${rows}</tbody></table>`;
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// ---- go --------------------------------------------------------------------
load().catch((err) => {
  chartsEl.innerHTML = `<div class="empty">Failed to load data: ${escapeHTML(err.message)}</div>`;
});
