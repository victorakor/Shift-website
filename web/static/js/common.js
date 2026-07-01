// common.js — shared helpers loaded on every page.
// RANKS mirrors internal/game/config.go Ranks (kept in sync manually per
// progress.md's "Rank color centralized" checklist item).
const RANKS = [
  { name: "Rookie", minWins: 0, color: "#8A8FA3", slug: "rookie" },
  { name: "Bronze", minWins: 10, color: "#C87F4A", slug: "bronze" },
  { name: "Silver", minWins: 30, color: "#C4C9D4", slug: "silver" },
  { name: "Gold", minWins: 60, color: "#F4C542", slug: "gold" },
  { name: "Platinum", minWins: 100, color: "#8FE3E0", slug: "platinum" },
  { name: "Diamond", minWins: 180, color: "#7FC7FF", slug: "diamond" },
  { name: "Master", minWins: 300, color: "#B48CFF", slug: "master" },
  { name: "Champion", minWins: 500, color: "#FF8CC6", slug: "champion" },
  { name: "Legend", minWins: 750, color: "#FF6A4D", slug: "legend" },
  { name: "Shift Master", minWins: 1000, color: "gradient", slug: "shift-master" },
];

function rankSlug(rankName) {
  const r = RANKS.find(r => r.name === rankName);
  return r ? r.slug : "rookie";
}

function rankColor(rankName) {
  const r = RANKS.find(r => r.name === rankName);
  return r ? r.color : "#8A8FA3";
}

function levelProgress(wins) {
  return { into: wins % 5, needed: 5 };
}

function el(tag, attrs = {}, children = []) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else if (k === "html") node.innerHTML = v;
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of [].concat(children)) {
    if (c == null) continue;
    node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return node;
}

function levelRingSVG(wins, size = 48) {
  const { into, needed } = levelProgress(wins);
  const r = (size - 6) / 2;
  const c = 2 * Math.PI * r;
  const offset = c * (1 - into / needed);
  return `<svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
    <circle class="ring-bg" cx="${size/2}" cy="${size/2}" r="${r}" stroke-width="3"/>
    <circle class="ring-fg" cx="${size/2}" cy="${size/2}" r="${r}" stroke-width="3"
      stroke-dasharray="${c}" stroke-dashoffset="${offset}"/>
  </svg>`;
}

function toast(message, kind = "pending") {
  const container = document.getElementById("toast-root") || (() => {
    const d = el("div", { id: "toast-root", class: "toast" });
    document.body.appendChild(d);
    return d;
  })();
  const card = el("div", { class: `vault-card vault-card--${kind}` }, message);
  container.appendChild(card);
  setTimeout(() => card.remove(), 4000);
}

async function apiFetch(url, opts = {}) {
  const res = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    ...opts,
  });
  let body = null;
  try { body = await res.json(); } catch (e) { /* no body */ }
  if (!res.ok) {
    const err = new Error((body && body.message) || (body && body.error) || "request failed");
    err.code = body && body.code;
    err.status = res.status;
    throw err;
  }
  return body;
}

function countUp(node, from, to, duration = 250) {
  const start = performance.now();
  function step(now) {
    const t = Math.min(1, (now - start) / duration);
    const val = Math.round(from + (to - from) * t);
    node.textContent = val;
    if (t < 1) requestAnimationFrame(step);
  }
  requestAnimationFrame(step);
}
