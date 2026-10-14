// Any Install user interface. Plain JavaScript, no build step.
(() => {
  "use strict";

  const $app = document.getElementById("app");
  const $overlay = document.getElementById("overlay");

  // The token from the start URL authorises our API calls. It is kept for
  // reloads and removed from the address bar.
  const query = new URLSearchParams(location.search);
  let TOKEN = query.get("t") || "";
  try {
    if (TOKEN) sessionStorage.setItem("anyinstall-token", TOKEN);
    else TOKEN = sessionStorage.getItem("anyinstall-token") || "";
  } catch (e) { /* storage blocked: keep the token in memory */ }
  if (query.get("t")) history.replaceState(null, "", location.pathname);

  const S = {
    lang: "de",
    state: null,      // /api/state
    game: null,       // selected game id
    view: "loading",  // loading | pick | main | done
    tab: "new",       // new | mine
    scan: null,
    scanning: false,
    sel: new Set(),
    unchecked: new Set(),
    open: new Set(),   // packs whose single mods are shown
    busyHint: "",
    showOthers: false,
    installed: null,
    result: null,
    busy: "",
    dialog: null,
    toast: "",
    confirm: "",
    pathInput: "",
    folderPanel: false,
    folderInput: false,
  };

  // ---------- helpers ----------

  function t(key, p) {
    const dict = I18N[S.lang] || I18N.de;
    let s = dict[key] ?? I18N.de[key] ?? key;
    if (p) s = s.replace(/\{(\w+)\}/g, (_, k) => (p[k] ?? ""));
    return s;
  }
  function esc(s) {
    return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }
  const icon = (id, cls = "") => `<svg class="ic ${cls}" aria-hidden="true"><use href="#i-${id}"/></svg>`;
  const plural = (n, one, many, p = {}) => t(n === 1 ? one : many, { n, ...p });

  async function api(method, path, body, raw) {
    const opts = { method, headers: { "X-Token": TOKEN } };
    if (body !== undefined) {
      if (raw) opts.body = body;
      else { opts.headers["Content-Type"] = "application/json"; opts.body = JSON.stringify(body); }
    }
    let res;
    try { res = await fetch(path, opts); } catch (e) { throw { error: "offline_app", detail: String(e) }; }
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw (data && data.error) ? data : { error: "failed", detail: "HTTP " + res.status };
    return data;
  }

  const game = () => S.state?.games.find((g) => g.id === S.game);
  const loc = () => game()?.location || {};

  function fmtSize(n) {
    if (!n) return "";
    const units = ["B", "KB", "MB", "GB"];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    const digits = n < 10 && i > 0 ? 1 : 0;
    return n.toLocaleString(S.lang === "de" ? "de-DE" : "en-GB", { maximumFractionDigits: digits }) + " " + units[i];
  }
  function fmtDate(iso) {
    const d = new Date(iso);
    if (isNaN(d)) return "";
    const locale = S.lang === "de" ? "de-DE" : "en-GB";
    const time = d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
    const today = new Date();
    const yest = new Date(); yest.setDate(today.getDate() - 1);
    if (d.toDateString() === today.toDateString()) return t("today", { t: time });
    if (d.toDateString() === yest.toDateString()) return t("yesterday", { t: time });
    const opts = { day: "2-digit", month: "2-digit" };
    if (d.getFullYear() !== today.getFullYear()) opts.year = "numeric";
    return d.toLocaleDateString(locale, opts);
  }
  const loaderName = (l) => (l ? t("loader_" + l) : "");
  // In demo mode the long temporary folder is shown as "Demo".
  const shownPath = (p) => {
    const root = S.state?.demo;
    return root && p && p.startsWith(root) ? "Demo" + p.slice(root.length) : (p || "");
  };

  function noteText(n) {
    const p = { ...(n.params || {}) };
    if (p.loader) p.loader = loaderName(p.loader);
    let code = n.code;
    if (code === "mc_needs_loader") {
      const raw = n.params?.loader;
      if (raw === "forge" || raw === "neoforge") code = "mc_needs_loader_manual";
      else if (!p.mc) code = "mc_needs_loader_any";
    }
    return t("note." + code, p);
  }
  function notesHTML(notes) {
    if (!notes || !notes.length) return "";
    return `<div class="notes">${notes.map((n) =>
      `<div class="note ${esc(n.level)}">${icon(n.level === "info" ? "info" : "warn")}<span>${esc(noteText(n))}</span></div>`).join("")}</div>`;
  }
  function errorText(e) {
    const p = { ...(e.params || {}) };
    if (!p.mod) p.mod = "";
    return t("err." + (e.error || "failed"), p);
  }

  function showError(e, retry) {
    S.busy = "";
    S.dialog = { text: errorText(e), detail: e.detail || "", retry };
    render();
  }
  let toastTimer = 0;
  function toast(text) {
    S.toast = text;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { S.toast = ""; renderOverlay(); }, 3500);
    renderOverlay();
  }

  // ---------- data ----------

  async function loadState() {
    S.state = await api("GET", "/api/state");
    if (!S.langChosen) S.lang = S.state.lang || ((navigator.language || "").toLowerCase().startsWith("de") ? "de" : "en");
    document.documentElement.lang = S.lang;
  }

  async function rescan(silent) {
    if (!S.game) return;
    if (!silent) { S.scan = null; render(); }
    S.scanning = true;
    try {
      const scan = await api("GET", "/api/scan?game=" + encodeURIComponent(S.game));
      S.scan = scan;
      // Pre-select new mods and updates, but respect what the user unticked.
      const valid = new Set();
      for (const it of scan.items) for (const m of it.mods) {
        const k = selKey(it, m);
        valid.add(k);
        if (m.checked && !S.unchecked.has(k)) S.sel.add(k);
        if (!m.selectable) S.sel.delete(k);
      }
      for (const k of [...S.sel]) if (!valid.has(k)) S.sel.delete(k);
    } catch (e) {
      S.scan = { items: [], others: [], folders: [] };
      showError(e, () => rescan());
    } finally {
      S.scanning = false;
      render();
    }
  }

  async function loadInstalled() {
    if (!S.game) return;
    try {
      S.installed = await api("GET", "/api/installed?game=" + encodeURIComponent(S.game));
    } catch (e) {
      S.installed = { items: [] };
      showError(e);
    }
    render();
  }

  // Selection keys end up in HTML attributes, so they must be plain text.
  const selKey = (it, m) => encodeURIComponent(it.path) + "|" + encodeURIComponent(m.key);

  async function enterMain() {
    S.view = "main";
    S.tab = "new";
    S.scan = null;
    S.installed = null;
    S.sel.clear();
    S.unchecked.clear();
    render();
    await Promise.all([rescan(true), loadInstalled()]);
  }

  // ---------- views ----------

  function topBar() {
    return `<header class="top">
      <div class="brand">${icon("logo")}<span>Any Install</span>${S.state?.demo ? `<span class="pill warn">DEMO</span>` : ""}</div>
      <div class="lang" role="group" aria-label="Sprache / Language">
        <button data-action="lang" data-lang="de" aria-pressed="${S.lang === "de"}">DE</button>
        <button data-action="lang" data-lang="en" aria-pressed="${S.lang === "en"}">EN</button>
      </div>
    </header>`;
  }

  const coverText = { "7dtd": "7 Days<br>to Die", eldenring: "Elden<br>Ring", minecraft: "Mine<br>craft" };
  function coverHTML(g, cls) {
    const img = g.hasCover ? `<img src="/api/cover/${esc(g.id)}?t=${esc(TOKEN)}" alt="" onerror="this.remove()">` : "";
    return `<div class="cover cv-${esc(g.id)} ${cls || ""}"><span>${coverText[g.id] || esc(g.name)}</span>${img}</div>`;
  }

  function viewLoading() {
    return `${topBar()}<div class="empty"><div class="spinner"></div><span>${esc(t("loading"))}</span></div><div></div>`;
  }

  function viewPick() {
    const st = S.state;
    const steam = st.steam.found
      ? `<span class="found">${icon("check")}${esc(t("steam_found", {
          libs: plural(st.steam.libraries, "libs_1", "libs_n"),
          games: plural(st.steam.games, "games_1", "games_n"),
        }))}</span>`
      : `<span class="found missing">${icon("warn")}${esc(t("steam_missing"))}</span>`;
    const tiles = st.games.map((g) => {
      const l = g.location;
      if (l.found) {
        const custom = l.via === "custom"
          ? `<small>${esc(t("via_custom"))} · <button class="link" data-action="reset-path" data-game="${esc(g.id)}">${esc(t("reset_path"))}</button></small>` : "";
        return `<div class="tile ok ${S.game === g.id ? "sel" : ""}">
          <button class="tile-hit" data-action="pick-game" data-game="${esc(g.id)}" aria-label="${esc(g.name)}">${coverHTML(g)}</button>
          <div class="tile-info"><b>${esc(g.name)}</b><span class="pill new">${esc(t("found"))}</span>
          <small>${esc(shownPath(l.modsDir))}</small>${custom}</div></div>`;
      }
      const input = S.pathInput === g.id
        ? `<div class="field"><input id="path-${esc(g.id)}" placeholder="${esc(t("path_prompt"))}" autocomplete="off">
           <button class="btn" data-action="save-path" data-game="${esc(g.id)}">${esc(t("save"))}</button></div>` : "";
      return `<div class="tile missing">${coverHTML(g)}
        <div class="tile-info"><b>${esc(g.name)}</b><span class="pill muted">${esc(t("not_found"))}</span>
        <button class="btn" data-action="choose-path" data-game="${esc(g.id)}">${icon("folder")}${esc(t("choose_folder"))}</button>${input}</div></div>`;
    }).join("");
    return `${topBar()}<div class="pick">
      <div style="display:grid;gap:8px">${steam}
        <h1 class="h1">${esc(t("pick_title"))}</h1><p class="sub">${esc(t("pick_sub"))}</p></div>
      <div class="tiles">${tiles}</div>
    </div><div></div>`;
  }

  function gameHead() {
    const g = game();
    return `<section class="gamehead">
      ${coverHTML(g, "thumb")}
      <div class="gh-text"><span class="gh-name">${esc(g.name)}</span>
        <span class="gh-path" title="${esc(shownPath(loc().modsDir))}">${esc(t("mods_folder"))}${loc().proton ? " (Proton)" : ""}: ${esc(shownPath(loc().modsDir))}</span></div>
      <button class="btn-ghost" data-action="change-game">${esc(t("change_game"))}</button>
    </section>`;
  }

  // Counts downloads, not the mods inside them: an overhaul pack is one.
  function newCount() {
    if (!S.scan) return "";
    return S.scan.items.filter((it) => it.mods.some((m) => m.status === "new" || m.status === "update")).length;
  }
  function mineCount() {
    if (!S.installed) return "";
    return S.installed.items.filter((i) => !i.builtin).length;
  }

  function tabs() {
    const extra = (S.state.extraFolders || []).length;
    return `<nav class="tabs" role="tablist">
      <button class="tab" role="tab" data-action="tab" data-tab="new" aria-selected="${S.tab === "new"}">${esc(t("tab_new"))} <span class="count">${newCount()}</span></button>
      <button class="tab" role="tab" data-action="tab" data-tab="mine" aria-selected="${S.tab === "mine"}">${esc(t("tab_mine"))} <span class="count">${mineCount()}</span></button>
      ${S.tab === "new" ? `<span class="where">${esc(t("searched_in"))} <button class="link" data-action="folders">${esc(t("downloads"))}${extra ? " " + esc(t("plus_folders", { n: extra })) : ""}</button>
        <button class="iconbtn" data-action="refresh" title="${esc(t("refresh"))}" aria-label="${esc(t("refresh"))}">${icon("refresh")}</button></span>` : ""}
    </nav>`;
  }

  function kindMeta(m) {
    if (m.kind === "jar") {
      return m.mcLabel ? t("mc_for", { loader: loaderName(m.loader), mc: m.mcLabel }) : t("mc_any", { loader: loaderName(m.loader) });
    }
    const label = m.kind && m.kind !== "mod" && m.kind !== "broken" ? t("kind_" + m.kind) : "";
    return label === m.name ? "" : label;
  }

  function statusPill(m) {
    let text = t("st_" + m.status);
    if (m.status === "update" && m.installedVersion && m.version) text = t("st_update_v", { from: m.installedVersion, to: m.version });
    return `<span class="pill ${esc(m.status)}">${esc(text)}</span>`;
  }

  // The version requirement of the first Fabric mod that has no Fabric
  // install yet, so the setup button picks a fitting Minecraft version.
  function fabricNeed() {
    for (const it of S.scan?.items || []) for (const m of it.mods) {
      if ((m.loader === "fabric" || m.loader === "quilt") && (m.notes || []).some((n) => n.code === "mc_needs_loader")) {
        return { req: m.mcVersion || "", mc: m.mcLabel || "" };
      }
    }
    return null;
  }

  // Hints about the game's folder (for example a Proton prefix that does
  // not exist yet) and, for Minecraft, the mod loaders.
  function gameBanners() {
    const notes = (loc().notes || []).filter((n) => n.code !== "mc_loader_found");
    return notes.map((n) => `<div class="banner ${n.level === "info" ? "info" : "warn"}">${icon(n.level === "info" ? "info" : "warn")}<span>${esc(noteText(n))}</span></div>`).join("")
      + minecraftBanner();
  }

  // Minecraft: which mod loaders are set up, and a button to set up Fabric
  // when there is none yet.
  function minecraftBanner() {
    if (S.game !== "minecraft") return "";
    const found = (loc().notes || []).filter((n) => n.code === "mc_loader_found");
    const hasFabric = found.some((n) => n.params.loader === "fabric" || n.params.loader === "quilt");
    const need = fabricNeed();
    let out = "";
    if (found.length) {
      out += `<div class="banner ok">${icon("check")}<span>${found.map((n) => esc(t("fabric_ok", { loader: loaderName(n.params.loader), mc: n.params.mc }))).join("<br>")}</span></div>`;
    }
    if (!hasFabric && (need || !found.length)) {
      const label = need && need.mc ? t("fabric_setup_for", { mc: need.mc }) : t("fabric_setup");
      out += `<div class="banner warn">${icon("warn")}<span>${esc(t("note.mc_no_loader"))}<br><small>${esc(t("fabric_hint"))}</small></span>
        <button class="btn" data-action="fabric" data-req="${esc(need ? need.req : "")}">${esc(label)}</button></div>`;
    }
    return out;
  }

  function modRow(it, m, sub) {
    const k = selKey(it, m);
    const on = S.sel.has(k);
    const meta = (sub ? [kindMeta(m)] : [it.imported ? t("imported") : it.file, fmtSize(it.size), fmtDate(it.modified), kindMeta(m)])
      .filter(Boolean).join(" · ");
    const check = m.selectable ? `<span class="check ${on ? "" : "unchecked"}">${on ? icon("check") : ""}</span>` : `<span class="check disabled"></span>`;
    const attrs = m.selectable
      ? `class="row selectable${sub ? " sub" : ""}" data-action="toggle-sel" data-sel="${esc(k)}" role="checkbox" aria-checked="${on}" tabindex="0"`
      : `class="row off${sub ? " sub" : ""}"`;
    return `<div ${attrs}>${check}<span class="ftype">${sub ? "" : esc(it.kind === "dir" ? t("folder") : it.kind)}</span>
      <span class="rtext"><span class="rname">${esc(m.name)}${m.version && m.status !== "update" ? ` <span class="muted" style="font-weight:400;color:var(--muted)">${esc(m.version)}</span>` : ""}</span>
      ${meta ? `<span class="rmeta">${esc(meta)}</span>` : ""}${notesHTML(m.notes)}</span>
      <span class="rside">${statusPill(m)}</span></div>`;
  }

  const packName = (it) => it.kind === "dir" ? it.file : it.file.replace(/\.(zip|7z|rar|jar|mrpack)$/i, "");

  // A download with several mods (an overhaul pack) is one row with one
  // checkbox for all of them; it can be opened to pick single mods.
  function packRow(it) {
    const sel = it.mods.filter((m) => m.selectable);
    const nOn = sel.filter((m) => S.sel.has(selKey(it, m))).length;
    const state = !sel.length ? "disabled" : !nOn ? "unchecked" : nOn === sel.length ? "on" : "partial";
    const check = state === "disabled" ? `<span class="check disabled"></span>`
      : `<span class="check ${state === "on" ? "" : state}">${state === "on" ? icon("check") : state === "partial" ? icon("minus") : ""}</span>`;
    const meta = [it.imported ? t("imported") : it.file, fmtSize(it.size), fmtDate(it.modified), t("pack_contains", { n: it.mods.length })]
      .filter(Boolean).join(" · ");
    // Each hint once for the whole pack.
    const seen = new Set();
    const notes = it.mods.flatMap((m) => m.notes || []).filter((n) => {
      const key = n.level + noteText(n);
      return seen.has(key) ? false : seen.add(key);
    });
    const statuses = it.mods.map((m) => m.status);
    const status = ["update", "new", "older", "installed", "unsupported"].find((s) => statuses.includes(s)) || "new";
    const open = S.open.has(it.path);
    const attrs = state === "disabled" ? `class="row off"`
      : `class="row selectable" data-action="toggle-pack" data-path="${esc(it.path)}" role="checkbox" aria-checked="${state === "on" ? "true" : state === "partial" ? "mixed" : "false"}" tabindex="0"`;
    let html = `<div ${attrs}>${check}<span class="ftype">${esc(it.kind === "dir" ? t("folder") : it.kind)}</span>
      <span class="rtext"><span class="rname">${esc(packName(it))}</span><span class="rmeta">${esc(meta)}</span>${notesHTML(notes)}
        <span><button class="link" data-action="toggle-open" data-path="${esc(it.path)}">${esc(open ? t("hide_mods") : t("show_mods", { n: it.mods.length }))}</button></span></span>
      <span class="rside"><span class="pill ${esc(status)}">${esc(t("st_" + status))}</span></span></div>`;
    if (open) html += `<div class="sublist">${it.mods.map((m) => modRow(it, m, true)).join("")}</div>`;
    return html;
  }

  function newList() {
    if (!S.scan) return `<div class="empty"><div class="spinner"></div><span>${esc(t("scanning"))}</span></div>`;
    const g = game();
    let rows = "";
    for (const it of S.scan.items) {
      rows += it.mods.length > 1 ? packRow(it) : modRow(it, it.mods[0], false);
    }
    let others = "";
    const n = S.scan.others.length;
    if (n) {
      others = `<div class="hidden-note">${esc(n === 1 ? t("others_1", { game: g.name }) : t("others", { n, game: g.name }))}
        <button class="link" data-action="show-others">${esc(S.showOthers ? t("hide") : t("show"))}</button></div>`;
      if (S.showOthers) {
        others += `<div class="list" style="margin-top:8px">` + S.scan.others.map((o) => `<div class="row off">
          <span class="check disabled"></span><span class="ftype">${esc(o.kind)}</span>
          <span class="rtext"><span class="rname">${esc(o.file)}</span><span class="rmeta">${esc([fmtSize(o.size), fmtDate(o.modified)].filter(Boolean).join(" · "))}</span></span>
          <span class="rside">${o.error ? `<span class="pill error">${esc(errorText({ error: o.error, params: { mod: o.file } }))}</span>` : `<span class="pill muted">${esc(t("no_mod"))}</span>`}
          ${o.imported ? `<button class="iconbtn" data-action="forget-import" data-path="${esc(o.path)}" aria-label="${esc(t("remove"))}">${icon("x")}</button>` : ""}</span></div>`).join("") + `</div>`;
      }
    }
    if (!rows) {
      return gameBanners() + `<div class="empty">${icon("tray")}<b>${esc(t("empty_title"))}</b><span>${esc(t("empty_text"))}</span></div>` + others;
    }
    return gameBanners() + `<div class="list">${rows}</div>` + others;
  }

  function selectedItems() {
    const out = [];
    for (const it of S.scan?.items || []) for (const m of it.mods) {
      if (m.selectable && S.sel.has(selKey(it, m))) out.push({ path: it.path, key: m.key });
    }
    return out;
  }

  // The downloads with something selected; a pack counts once.
  function selectedDownloads() {
    return new Set(selectedItems().map((x) => x.path));
  }

  // The button counts mods, so a pack with 17 mods counts 17; the list
  // shows one row per download and says how many mods a pack contains.
  function footNew() {
    const n = selectedItems().length;
    const label = n === 0 ? t("install_0") : n === 1 ? t("install_1") : t("install_n", { n });
    return `<footer class="foot">
      <div class="drop">${icon("tray")}<span>${esc(t("drop"))}</span>
        <button class="btn" data-action="pick-file">${esc(t("pick_file"))}</button></div>
      <button class="btn-go" data-action="install" ${n ? "" : "disabled"}>${esc(label)}</button>
      <input type="file" id="file-input" multiple accept=".zip,.7z,.rar,.jar,.mrpack" hidden>
    </footer>`;
  }

  function coopCard(pw) {
    return `<div class="card">
      <h3>${esc(t("coop_title"))}</h3><p>${esc(t("coop_text"))}</p>
      <div class="field"><input id="coop-pw" value="${esc(pw || "")}" placeholder="${esc(t("coop_placeholder"))}" maxlength="64" autocomplete="off" spellcheck="false">
        <button class="btn" data-action="save-coop">${esc(t("save"))}</button></div>
    </div>`;
  }

  const groupOrder = ["coop", "loader", "dllmod", "mod", "jar", "resourcepack", "shaderpack", "broken"];

  function mineList() {
    const inst = S.installed;
    if (!inst) return `<div class="empty"><div class="spinner"></div></div>`;
    let out = "";
    if (S.game === "eldenring" && inst.coop?.installed) out += coopCard(inst.coop.password);
    out += gameBanners();
    if (!inst.items.length) {
      return out + `<div class="empty">${icon("tray")}<b>${esc(t("mine_empty_title"))}</b><span>${esc(t("mine_empty_text"))}</span></div>`;
    }
    const kinds = [...new Set(inst.items.map((i) => i.kind))].sort((a, b) => groupOrder.indexOf(a) - groupOrder.indexOf(b));
    for (const kind of kinds) {
      if (kinds.length > 1) out += `<div class="subhead">${esc(t("group_" + kind))}</div>`;
      out += `<div class="list">`;
      for (const it of inst.items.filter((i) => i.kind === kind)) {
        const sw = it.canToggle
          ? `<button class="switch" role="switch" aria-checked="${it.enabled}" aria-label="${esc(it.name)}" data-action="toggle-mod" data-id="${esc(it.id)}"></button>`
          : `<span class="switch-spacer"></span>`;
        const meta = [it.version ? t("version", { v: it.version }) : "", it.kind === "jar" ? kindMeta(it) : ""].filter(Boolean).join(" · ");
        const repair = (it.notes || []).some((n) => n.code === "nested_wrong")
          ? `<button class="btn" data-action="repair" data-id="${esc(it.id)}">${icon("wrench")}${esc(t("repair"))}</button>` : "";
        const side = it.builtin
          ? `<span class="pill muted">${esc(t("builtin"))}</span>`
          : `${repair}<button class="iconbtn" data-action="remove" data-id="${esc(it.id)}" aria-label="${esc(t("remove"))}" title="${esc(t("remove"))}">${icon("trash")}</button>`;
        const confirm = S.confirm === it.id
          ? `<div class="confirm"><span>${esc(t("remove_q", { mod: it.name }))}</span>
             <button class="btn btn-danger" data-action="confirm-remove" data-id="${esc(it.id)}">${esc(t("remove"))}</button>
             <button class="btn-ghost" data-action="cancel-remove">${esc(t("cancel"))}</button></div>` : "";
        out += `<div class="mrow ${it.enabled ? "" : "off"}">${sw}
          <span class="rtext"><span class="rname">${esc(it.name)}</span>${meta ? `<span class="rmeta">${esc(meta)}</span>` : ""}${notesHTML(it.notes)}</span>
          <span class="rside">${side}</span>${confirm}</div>`;
      }
      out += `</div>`;
    }
    return out;
  }

  function undoLabel() {
    const last = S.installed?.last;
    if (!last) return t("undo_last");
    const mods = (last.mods || []).slice(0, 2).join(", ") + ((last.mods || []).length > 2 ? " …" : "");
    return t("undo_what", { what: t("undo_" + last.action, { mods }) });
  }

  function footMine() {
    const inst = S.installed;
    const items = (inst?.items || []).filter((i) => !i.builtin);
    const on = items.filter((i) => i.enabled).length;
    const launch = inst?.canLaunch
      ? `<button class="btn-go" data-action="launch">${icon("play", "solid")}${esc(t("start_game", { game: game().name }))}</button>` : "";
    return `<footer class="foot">
      <button class="btn-ghost" data-action="undo" ${inst?.last ? "" : "disabled"} title="${esc(undoLabel())}">${icon("undo")}<span style="max-width:340px;overflow:hidden;text-overflow:ellipsis">${esc(inst?.last ? undoLabel() : t("nothing_undo"))}</span></button>
      <button class="btn-ghost" data-action="open-folder">${icon("folder")}${esc(t("open_folder"))}</button>
      <span class="spacer"></span>
      ${items.length ? `<span class="muted">${esc(t("enabled_of", { on, n: items.length }))}</span>` : ""}
      ${launch}
    </footer>`;
  }

  function viewMain() {
    return `${topBar()}${gameHead()}${tabs()}
      <main class="content" id="content">${S.tab === "new" ? newList() : mineList()}</main>
      ${S.tab === "new" ? footNew() : footMine()}`;
  }

  function viewDone() {
    const r = S.result;
    // One line per download, so an overhaul pack is one line.
    const groups = [];
    for (const x of r.installed) {
      const g = groups.find((g) => x.source && g.source === x.source);
      if (g) g.items.push(x); else groups.push({ source: x.source, file: x.file, items: [x] });
    }
    const n = r.installed.length;
    const list = groups.map((g) => {
      if (g.items.length > 1) {
        const name = g.file.replace(/\.(zip|7z|rar|jar|mrpack)$/i, "");
        return `<li>${icon("check")}<b>${esc(name)}</b><span class="muted">${esc(t("done_pack", { n: g.items.length }))}</span></li>`;
      }
      const x = g.items[0];
      const what = x.status === "update" || x.status === "older"
        ? (x.version ? t("done_update_v", { v: x.version }) : t("done_update")) : t("done_new");
      return `<li>${icon("check")}<b>${esc(x.name)}</b><span class="muted">${esc(what)}</span></li>`;
    }).join("");
    const tip = (n) => `<div class="banner ${n.level === "warn" ? "warn" : "info"}">${icon(n.level === "warn" ? "warn" : "info")}<span>${esc(noteText(n))}</span></div>`;
    // Warnings come before the start button, general tips after it.
    const warnings = (r.tips || []).filter((n) => n.level === "warn").map(tip).join("");
    const infos = (r.tips || []).filter((n) => n.level !== "warn").map(tip).join("");
    const coop = S.game === "eldenring" && r.installed.some((x) => x.kind === "coop")
      ? coopCard(S.installed?.coop?.password) : "";
    const launch = r.canLaunch
      ? `<button class="btn-go" data-action="launch">${icon("play", "solid")}${esc(t("start_game", { game: game().name }))}</button>` : "";
    return `${topBar()}<div class="done">
      <div class="bigcheck">${icon("check")}</div>
      <h1 class="h1">${esc(n === 1 ? t("done_1") : t("done_n", { n }))}</h1>
      <ul class="donelist">${list}</ul>
      ${warnings}${coop}
      <div class="actions">${launch}<button class="btn-ghost" data-action="back">${esc(t("back_to_list"))}</button></div>
      ${infos}
    </div><div></div>`;
  }

  function renderOverlay() {
    let html = "";
    if (S.busy) {
      html += `<div class="scrim" aria-live="polite"><div class="busy"><div class="spinner"></div><span>${esc(S.busy)}</span>${S.busyHint ? `<small>${esc(S.busyHint)}</small>` : ""}</div></div>`;
    } else if (S.dialog) {
      const d = S.dialog;
      html += `<div class="scrim" role="dialog" aria-modal="true"><div class="dialog">
        <h2>${icon("warn")}${esc(t("dialog_title"))}</h2><p>${esc(d.text)}</p>
        ${d.detail ? `<details><summary>${esc(t("details"))}</summary><pre>${esc(d.detail)}</pre></details>` : ""}
        <div class="actions">${d.retry ? `<button class="btn" data-action="dialog-retry">${esc(t("retry"))}</button>` : ""}
          <button class="btn-go" data-action="dialog-ok">${esc(t("ok"))}</button></div></div></div>`;
    } else if (S.folderPanel) {
      const folders = (S.state.extraFolders || []).map((f) => `<div class="frow"><span>${esc(shownPath(f))}</span>
        <button class="iconbtn" data-action="remove-folder" data-path="${esc(f)}" aria-label="${esc(t("remove"))}">${icon("x")}</button></div>`).join("");
      const input = S.folderInput ? `<div class="field"><input id="folder-path" placeholder="${esc(t("path_prompt"))}" autocomplete="off">
        <button class="btn" data-action="save-folder">${esc(t("save"))}</button></div>` : "";
      html += `<div class="scrim" role="dialog" aria-modal="true"><div class="dialog">
        <h2>${esc(t("folders_title"))}</h2><p>${esc(t("folders_text"))}</p>
        <div class="folders"><div class="frow"><span>${esc(shownPath(S.state.downloads))}</span></div>${folders}</div>${input}
        <div class="actions"><button class="btn" data-action="add-folder">${icon("plus")}${esc(t("add_folder"))}</button>
          <button class="btn-go" data-action="close-folders">${esc(t("close"))}</button></div></div></div>`;
    }
    if (S.toast) html += `<div class="toast" role="status">${icon("check")}<span>${esc(S.toast)}</span></div>`;
    $overlay.innerHTML = html;
    const primary = $overlay.querySelector(".dialog .btn-go");
    if (primary && !$overlay.contains(document.activeElement)) primary.focus();
  }

  let lastView = "";
  function render() {
    const scroller = $app.querySelector(".content, .pick, .done");
    const scroll = scroller ? scroller.scrollTop : 0;
    const viewKey = S.view + ":" + S.tab + ":" + S.game;
    $app.className = "app" + (S.view === "main" ? "" : " simple");
    if (S.view === "loading") $app.innerHTML = viewLoading();
    else if (S.view === "pick") $app.innerHTML = viewPick();
    else if (S.view === "done") $app.innerHTML = viewDone();
    else $app.innerHTML = viewMain();
    const again = $app.querySelector(".content, .pick, .done");
    if (again && viewKey === lastView) again.scrollTop = scroll;
    lastView = viewKey;
    document.title = S.game && game() && S.view !== "pick" ? "Any Install · " + game().name : "Any Install";
    renderOverlay();
  }

  // ---------- actions ----------

  async function run(busyText, fn, retry) {
    S.busy = busyText || "";
    if (busyText) renderOverlay();
    try {
      await fn();
    } catch (e) {
      S.busyHint = "";
      showError(e, retry);
      return false;
    }
    S.busy = "";
    S.busyHint = "";
    render();
    return true;
  }

  async function install() {
    const items = selectedItems();
    if (!items.length) return;
    const paths = selectedDownloads();
    const busy = items.length === 1 ? t("installing_1") : t("installing_n", { n: items.length });
    // Big packs take a while to unpack; say so, so nobody closes the window.
    const total = S.scan.items.filter((it) => paths.has(it.path)).reduce((sum, it) => sum + (it.size || 0), 0);
    S.busyHint = total > 500 * 1024 * 1024 ? t("big_download", { size: fmtSize(total) }) : "";
    await run(busy, async () => {
      const res = await api("POST", "/api/install", { game: S.game, items });
      S.result = res;
      for (const it of items) S.unchecked.delete(selKey(it, it));
      S.view = "done";
      S.installed = null;
      await loadInstalled();
    }, install);
  }

  async function importFiles(files) {
    const list = [...files];
    for (const f of list) {
      const ext = (f.name.split(".").pop() || "").toLowerCase();
      if (!["zip", "7z", "rar", "jar", "mrpack"].includes(ext)) {
        showError({ error: "unsupported_file", params: { file: f.name } });
        return;
      }
    }
    for (const f of list) {
      const ok = await run(t("importing", { file: f.name }), () => api("POST", "/api/import?name=" + encodeURIComponent(f.name), f, true));
      if (!ok) return;
    }
    if (S.view !== "main") { S.view = "main"; }
    S.tab = "new";
    await rescan(true);
  }

  const actions = {
    lang(el) {
      S.lang = el.dataset.lang;
      S.langChosen = true;
      document.documentElement.lang = S.lang;
      api("POST", "/api/settings", { lang: S.lang }).catch(() => {});
      render();
    },
    async "pick-game"(el) {
      S.game = el.dataset.game;
      api("POST", "/api/settings", { game: S.game }).catch(() => {});
      await enterMain();
    },
    async "choose-path"(el) {
      const id = el.dataset.game;
      const g = S.state.games.find((x) => x.id === id);
      try {
        await api("POST", "/api/game-path", { game: id, pick: true, title: t("choose_folder_title", { game: g.name }) });
        await loadState();
        render();
      } catch (e) {
        if (e.error === "no_picker") { S.pathInput = id; render(); $app.querySelector("#path-" + id)?.focus(); }
        else if (e.error !== "cancelled") showError(e);
      }
    },
    async "save-path"(el) {
      const id = el.dataset.game;
      const v = $app.querySelector("#path-" + id)?.value || "";
      await run("", async () => {
        await api("POST", "/api/game-path", { game: id, path: v });
        S.pathInput = "";
        await loadState();
      });
    },
    async "reset-path"(el) {
      await run("", async () => {
        await api("POST", "/api/game-path", { game: el.dataset.game, reset: true });
        await loadState();
      });
    },
    async "change-game"() {
      S.view = "pick";
      render();
      await loadState().catch(() => {});
      render();
    },
    tab(el) {
      S.tab = el.dataset.tab;
      S.confirm = "";
      render();
      if (S.tab === "mine") loadInstalled();
      else rescan(true);
    },
    refresh() { rescan(); },
    "toggle-sel"(el) {
      const k = el.dataset.sel;
      if (S.sel.has(k)) { S.sel.delete(k); S.unchecked.add(k); } else { S.sel.add(k); S.unchecked.delete(k); }
      render();
    },
    "toggle-pack"(el) {
      const it = S.scan?.items.find((i) => i.path === el.dataset.path);
      if (!it) return;
      const mods = it.mods.filter((m) => m.selectable);
      const all = mods.every((m) => S.sel.has(selKey(it, m)));
      for (const m of mods) {
        const k = selKey(it, m);
        if (all) { S.sel.delete(k); S.unchecked.add(k); } else { S.sel.add(k); S.unchecked.delete(k); }
      }
      render();
    },
    "toggle-open"(el) {
      const p = el.dataset.path;
      if (S.open.has(p)) S.open.delete(p); else S.open.add(p);
      render();
    },
    "show-others"() { S.showOthers = !S.showOthers; render(); },
    install,
    "pick-file"() { $app.querySelector("#file-input")?.click(); },
    async "toggle-mod"(el) {
      const it = S.installed.items.find((i) => i.id === el.dataset.id);
      if (!it) return;
      await run("", async () => {
        await api("POST", "/api/toggle", { game: S.game, id: it.id, on: !it.enabled });
        await loadInstalled();
        toast(it.enabled ? t("toast_off") : t("toast_on"));
      });
    },
    remove(el) { S.confirm = el.dataset.id; render(); },
    "cancel-remove"() { S.confirm = ""; render(); },
    async "confirm-remove"(el) {
      S.confirm = "";
      await run(t("loading"), async () => {
        await api("POST", "/api/remove", { game: S.game, id: el.dataset.id });
        await loadInstalled();
        toast(t("toast_removed"));
      });
    },
    async repair(el) {
      await run(t("loading"), async () => {
        const res = await api("POST", "/api/repair", { game: S.game, id: el.dataset.id });
        await loadInstalled();
        toast(t("toast_repaired", { mods: (res.mods || []).join(", ") }));
      });
    },
    async undo() {
      await run(t("loading"), async () => {
        await api("POST", "/api/undo", { game: S.game });
        await loadInstalled();
        rescan(true);
        toast(t("toast_undone"));
      });
    },
    "open-folder"() { api("POST", "/api/open", { game: S.game, what: "mods" }).catch((e) => showError(e)); },
    async launch() {
      await run("", async () => {
        await api("POST", "/api/launch", { game: S.game });
        toast(t("toast_launch", { game: game().name }));
      });
    },
    async "save-coop"() {
      const pw = $app.querySelector("#coop-pw")?.value || "";
      await run("", async () => {
        await api("POST", "/api/coop-password", { password: pw });
        if (S.installed?.coop) S.installed.coop.password = pw.trim();
        loadInstalled();
        toast(t("toast_saved"));
      });
    },
    async fabric(el) {
      await run(t("fabric_busy"), async () => {
        const res = await api("POST", "/api/fabric", { req: el.dataset.req || "" });
        await loadState();
        await Promise.all([rescan(true), loadInstalled()]);
        toast(t("toast_fabric", { profile: res.profile }));
      });
    },
    folders() { S.folderPanel = true; S.folderInput = false; renderOverlay(); },
    "close-folders"() { S.folderPanel = false; renderOverlay(); rescan(true); },
    async "add-folder"() {
      try {
        const res = await api("POST", "/api/folders", { pick: true, title: t("add_folder") });
        S.state.extraFolders = res.extraFolders || [];
        renderOverlay();
        render();
      } catch (e) {
        if (e.error === "no_picker") { S.folderInput = true; renderOverlay(); $overlay.querySelector("#folder-path")?.focus(); }
        else if (e.error !== "cancelled") showError(e);
      }
    },
    async "save-folder"() {
      const v = $overlay.querySelector("#folder-path")?.value || "";
      try {
        const res = await api("POST", "/api/folders", { add: v });
        S.state.extraFolders = res.extraFolders || [];
        S.folderInput = false;
        render();
      } catch (e) { S.folderPanel = false; showError(e); }
    },
    async "remove-folder"(el) {
      const res = await api("POST", "/api/folders", { remove: el.dataset.path }).catch((e) => { showError(e); return null; });
      if (res) { S.state.extraFolders = res.extraFolders || []; render(); }
    },
    async "forget-import"(el) {
      await api("POST", "/api/forget-imports", { path: el.dataset.path }).catch(() => {});
      rescan(true);
    },
    back() { S.view = "main"; S.tab = "new"; render(); rescan(true); },
    "dialog-ok"() { S.dialog = null; render(); },
    "dialog-retry"() { const r = S.dialog?.retry; S.dialog = null; render(); if (r) r(); },
  };

  function onAction(ev) {
    const el = ev.target.closest("[data-action]");
    if (!el || el.disabled) return;
    const fn = actions[el.dataset.action];
    if (fn) { ev.preventDefault(); fn(el, ev); }
  }
  $app.addEventListener("click", onAction);
  $overlay.addEventListener("click", onAction);
  document.addEventListener("keydown", (ev) => {
    if ((ev.key === " " || ev.key === "Enter") && ev.target.matches?.('[role="checkbox"][data-action]')) onAction(ev);
    if (ev.key === "Escape" && (S.dialog || S.folderPanel)) { S.dialog = null; S.folderPanel = false; render(); }
    if (ev.key === "Enter" && ev.target.id === "coop-pw") actions["save-coop"]();
  });
  $app.addEventListener("change", (ev) => {
    if (ev.target.id === "file-input" && ev.target.files.length) importFiles(ev.target.files);
  });

  // Drag and drop of downloaded files onto the window.
  let dragDepth = 0;
  window.addEventListener("dragenter", (ev) => { if (ev.dataTransfer?.types.includes("Files")) { dragDepth++; document.body.classList.add("dragging"); } });
  window.addEventListener("dragleave", () => { dragDepth = Math.max(0, dragDepth - 1); if (!dragDepth) document.body.classList.remove("dragging"); });
  window.addEventListener("dragover", (ev) => { ev.preventDefault(); });
  window.addEventListener("drop", (ev) => {
    ev.preventDefault();
    dragDepth = 0;
    document.body.classList.remove("dragging");
    if (S.view === "main" || S.view === "done") {
      if (ev.dataTransfer?.files?.length) importFiles(ev.dataTransfer.files);
    }
  });

  // New downloads appear when the user comes back to the window.
  let lastFocusScan = 0;
  function maybeRescan() {
    if (document.visibilityState !== "visible" || S.view !== "main" || S.busy || S.scanning) return;
    if (Date.now() - lastFocusScan < 3000) return;
    lastFocusScan = Date.now();
    if (S.tab === "new") rescan(true); else loadInstalled();
  }
  window.addEventListener("focus", maybeRescan);
  document.addEventListener("visibilitychange", maybeRescan);

  // Keep the program alive while the window is open.
  let failedPings = 0;
  setInterval(async () => {
    try { await api("GET", "/api/ping"); failedPings = 0; } catch (e) {
      if (++failedPings === 3) showError({ error: "offline_app" });
    }
  }, 5000);
  api("GET", "/api/ping").catch(() => {});
  window.addEventListener("pagehide", () => { navigator.sendBeacon("/api/bye?t=" + encodeURIComponent(TOKEN)); });

  // ---------- start ----------
  (async () => {
    render();
    try {
      await loadState();
    } catch (e) {
      S.view = "pick";
      S.state = { games: [], steam: {}, extraFolders: [] };
      showError(e);
      return;
    }
    const saved = S.state.game && S.state.games.find((g) => g.id === S.state.game && g.location.found);
    if (saved) {
      S.game = saved.id;
      await enterMain();
    } else {
      S.view = "pick";
      render();
    }
  })();
})();
