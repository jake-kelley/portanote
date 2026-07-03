/* Portanote frontend — no framework, talks to the local Go API. */
"use strict";

const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];

const state = {
  meta: {},
  notes: [],            // ListItem[] from /api/notes
  view: "all",          // all | starred | untagged | trash
  tag: null,            // active tag filter (exclusive with view/folder)
  folder: null,         // active folder filter (exclusive with view/tag)
  folders: [],          // ordered folder names
  q: "",
  results: null,        // search results when q is non-empty
  current: null,        // full Note being edited
  mode: localStorage.getItem("pn-mode") || "split",
  sort: localStorage.getItem("pn-sort") || "updated",
  dirty: false,
  saveTimer: null,
  searchTimer: null,
  previewTimer: null,
};

/* ---------------------------------------------------------------- init */

document.addEventListener("DOMContentLoaded", async () => {
  marked.use({ gfm: true, breaks: true });
  const themeParam = new URLSearchParams(location.search).get("theme");
  if (themeParam === "dark" || themeParam === "light") setTheme(themeParam === "dark");
  else if (localStorage.getItem("pn-theme") === "dark") setTheme(true);

  const [meta, notes, folders] = await Promise.all([
    fetch("/api/meta").then((r) => r.json()),
    fetch("/api/notes").then((r) => r.json()),
    fetch("/api/folders").then((r) => r.json()),
  ]);
  state.meta = meta;
  state.notes = notes;
  state.folders = folders.map((f) => f.name);

  $("#verLabel").textContent = "v" + meta.version;
  const badge = $("#pdfBadge");
  if (meta.eisvogel) {
    badge.textContent = "PDF: eisvogel";
    badge.classList.add("on");
    badge.title = "pandoc: " + meta.pandoc + "\nengine: " + meta.engine;
  } else {
    badge.textContent = "PDF: built-in";
    badge.title = "Print-to-PDF available. For true Eisvogel export, drop pandoc + tectonic into tools/ (see README).";
  }

  $("#sortSel").value = state.sort;
  bindEvents();
  renderAll();

  // deep link: /#<note-id> opens that note (and refresh keeps it open)
  const hashId = decodeURIComponent(location.hash.slice(1));
  if (hashId) selectNote(hashId);
});

/* ---------------------------------------------------------------- data helpers */

function fmtDate(iso, withTime = false) {
  const d = new Date(iso);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay && !withTime) return d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const opts = { month: "short", day: "numeric" };
  if (d.getFullYear() !== now.getFullYear()) opts.year = "numeric";
  let out = d.toLocaleDateString(undefined, opts);
  if (withTime) out += ", " + d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  return out;
}

function esc(s) {
  return (s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function highlight(text, q) {
  let html = esc(text);
  const terms = q.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter((t) => t.length >= 2);
  const full = q.trim().toLowerCase();
  if (full.length >= 2 && !terms.includes(full)) terms.unshift(full);
  for (const t of [...new Set(terms)]) {
    const re = new RegExp("(" + t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ")", "gi");
    html = html.replace(re, "<mark>$1</mark>");
  }
  return html;
}

function visibleNotes() {
  let list;
  if (state.q && state.results) {
    list = state.results;
  } else {
    list = [...state.notes];
  }
  list = list.filter((n) => {
    if (state.folder) return !n.trashed && n.folder === state.folder;
    if (state.tag) return !n.trashed && n.tags.includes(state.tag);
    switch (state.view) {
      case "starred":  return n.starred && !n.trashed;
      case "untagged": return !n.trashed && n.tags.length === 0;
      case "trash":    return n.trashed;
      default:         return !n.trashed;
    }
  });
  if (!state.q) {
    const key = state.sort;
    list.sort((a, b) => {
      if (state.view !== "trash" && a.starred !== b.starred) return a.starred ? -1 : 1;
      if (key === "title") return a.title.localeCompare(b.title);
      return new Date(b[key]) - new Date(a[key]);
    });
  }
  return list;
}

function tagCounts() {
  const counts = {};
  for (const n of state.notes) {
    if (n.trashed) continue;
    for (const t of n.tags) counts[t] = (counts[t] || 0) + 1;
  }
  return counts;
}

function folderCounts() {
  const counts = {};
  for (const n of state.notes) {
    if (n.trashed || !n.folder) continue;
    counts[n.folder] = (counts[n.folder] || 0) + 1;
  }
  return counts;
}

/* ---------------------------------------------------------------- rendering */

function renderAll() {
  renderSidebar();
  renderList();
  renderEditor();
}

function renderSidebar() {
  const notes = state.notes;
  $("#cAll").textContent = notes.filter((n) => !n.trashed).length;
  $("#cStarred").textContent = notes.filter((n) => n.starred && !n.trashed).length || "";
  $("#cUntagged").textContent = notes.filter((n) => !n.trashed && n.tags.length === 0).length || "";
  $("#cTrash").textContent = notes.filter((n) => n.trashed).length || "";

  $$("#views a").forEach((a) =>
    a.classList.toggle("active", !state.tag && !state.folder && a.dataset.view === state.view));

  // folders (manifest order; counts computed live, empty folders still show)
  const fcounts = folderCounts();
  if (state.folders.length) {
    $("#folderlist").innerHTML = state.folders.map((f) =>
      `<a data-folder="${esc(f)}" class="${state.folder === f ? "active" : ""}">
         <span class="ficon">📁</span><span class="fname">${esc(f)}</span>
         <span class="count">${fcounts[f] || ""}</span>
         <span class="folder-x" data-del="${esc(f)}" title="Delete folder">✕</span></a>`).join("");
  } else {
    $("#folderlist").innerHTML = `<div class="folder-empty">No folders yet — tap +</div>`;
  }

  const counts = tagCounts();
  const tags = Object.keys(counts).sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));
  $("#taglist").innerHTML = tags.map((t) =>
    `<a data-tag="${esc(t)}" class="${state.tag === t ? "active" : ""}">
       <span class="thash">#</span><span class="tname">${esc(t)}</span>
       <span class="count">${counts[t]}</span></a>`).join("");

  $("#tagOptions").innerHTML = tags.map((t) => `<option value="${esc(t)}">`).join("");
}

function renderList() {
  const list = visibleNotes();
  $("#listTitle").textContent = state.folder ? state.folder : state.tag ? "#" + state.tag :
    { all: "Notes", starred: "Starred", untagged: "Untagged", trash: "Trash" }[state.view];

  if (!list.length) {
    const msg = state.q ? "No results for “" + esc(state.q) + "”"
      : state.view === "trash" ? "Trash is empty"
      : state.folder ? "This folder is empty.<br>Open a note and pick this folder, or make a new one here."
      : "No notes here yet.<br>Press <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>N</kbd> to create one.";
    $("#notelist").innerHTML = `<div class="list-empty">${msg}</div>`;
    return;
  }

  $("#notelist").innerHTML = list.map((n) => {
    const title = state.q ? highlight(n.title || "Untitled", state.q) : esc(n.title || "Untitled");
    const snip = state.q ? highlight(n.snippet, state.q) : esc(n.snippet);
    return `<div class="note-item ${state.current?.id === n.id ? "active" : ""}" data-id="${esc(n.id)}">
      <div class="ni-top"><span class="ni-title">${title}</span>${n.starred ? '<span class="ni-star">★</span>' : ""}</div>
      ${snip ? `<div class="ni-snippet">${snip}</div>` : ""}
      <div class="ni-date">${fmtDate(n.updated, true)}</div>
      ${(n.folder && !state.folder) || n.tags.length ? `<div class="ni-tags">${
        n.folder && !state.folder ? `<span class="chip chip-folder">📁 ${esc(n.folder)}</span>` : ""
      }${n.tags.map((t) => `<span class="chip">${esc(t)}</span>`).join("")}</div>` : ""}
    </div>`;
  }).join("");
}

function renderEditor() {
  const n = state.current;
  $("#emptyState").style.display = n ? "none" : "";
  $("#editorMain").hidden = !n;
  if (!n) return;

  $("#trashBanner").hidden = !n.trashed;
  $("#title").value = n.title;
  $("#mdtext").value = n.body;
  renderFolderSelect();
  renderTagChips();
  $("#starBtn").textContent = n.starred ? "★" : "☆";
  $("#starBtn").classList.toggle("on", n.starred);

  setMode(state.mode, false);
  renderPreview();
  renderStats();
  $("#noteDates").textContent =
    "created " + fmtDate(n.created, true) + " · edited " + fmtDate(n.updated, true);

  const eis = state.meta.eisvogel;
  $("#exEisvogel").disabled = !eis;
  $("#exEisvogelToc").disabled = !eis;
  $("#exNote").hidden = !!eis;
}

function renderTagChips() {
  $("#tagchips").innerHTML = state.current.tags.map((t) =>
    `<span class="tagchip">${esc(t)}<span class="x" data-tag="${esc(t)}" title="Remove">✕</span></span>`).join("");
}

function renderFolderSelect() {
  const cur = state.current.folder || "";
  const opts = [`<option value="">(no folder)</option>`];
  for (const f of state.folders) {
    opts.push(`<option value="${esc(f)}" ${f === cur ? "selected" : ""}>${esc(f)}</option>`);
  }
  // a note may sit in a folder that isn't in the manifest (hand-edited) — show it
  if (cur && !state.folders.includes(cur)) {
    opts.push(`<option value="${esc(cur)}" selected>${esc(cur)}</option>`);
  }
  opts.push(`<option value="__new__">+ New folder…</option>`);
  $("#folderSel").innerHTML = opts.join("");
}

function renderPreview() {
  if (!state.current) return;
  const raw = marked.parse($("#mdtext").value);
  $("#preview").innerHTML = DOMPurify.sanitize(raw);
  $("#preview").querySelectorAll("pre code").forEach((el) => hljs.highlightElement(el));
}

function renderStats() {
  const t = $("#mdtext").value;
  const words = t.trim() ? t.trim().split(/\s+/).length : 0;
  $("#stats").textContent = words.toLocaleString() + " words · " + t.length.toLocaleString() + " chars";
}

function setSaveState(cls, text) {
  const el = $("#saveState");
  el.className = cls;
  el.textContent = text;
}

/* ---------------------------------------------------------------- actions */

async function selectNote(id) {
  if (state.dirty) await saveNow();
  const r = await fetch("/api/notes/" + encodeURIComponent(id));
  if (!r.ok) return;
  state.current = await r.json();
  history.replaceState(null, "", "#" + encodeURIComponent(state.current.id));
  renderList();
  renderEditor();
}

async function newNote() {
  const r = await fetch("/api/notes", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
  const n = await r.json();
  // creating a note while a folder is open drops it into that folder
  const targetFolder = state.folder || "";
  n.folder = targetFolder;
  state.notes.unshift({ ...n, snippet: "" });
  if (state.view === "trash" || state.tag) { state.view = "all"; state.tag = null; }
  state.q = ""; state.results = null; $("#search").value = "";
  state.current = n;
  renderAll();
  if (targetFolder) await saveNow();
  $("#title").focus();
}

function scheduleSave() {
  state.dirty = true;
  setSaveState("saving", "Saving…");
  clearTimeout(state.saveTimer);
  state.saveTimer = setTimeout(saveNow, 600);
}

async function saveNow(extra = {}) {
  clearTimeout(state.saveTimer);
  if (!state.current) return;
  const payload = {
    title: $("#title").value,
    body: $("#mdtext").value,
    folder: state.current.folder || "",
    tags: state.current.tags,
    starred: state.current.starred,
    trashed: state.current.trashed,
    ...extra,
  };
  const r = await fetch("/api/notes/" + encodeURIComponent(state.current.id), {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload),
  });
  if (!r.ok) { setSaveState("", "Save failed!"); return; }
  const saved = await r.json();
  state.current = saved;
  state.dirty = false;
  const i = state.notes.findIndex((x) => x.id === saved.id);
  const item = { ...saved, snippet: plainSnippet(saved.body) };
  delete item.body;
  if (i >= 0) state.notes[i] = item; else state.notes.unshift(item);
  setSaveState("saved", "Saved ✓");
  renderSidebar();
  renderList();
  $("#noteDates").textContent =
    "created " + fmtDate(saved.created, true) + " · edited " + fmtDate(saved.updated, true);
}

function plainSnippet(body) {
  return body.replace(/[#*_`>~]|!?\[([^\]]*)\]\([^)]*\)/g, "$1").replace(/\s+/g, " ").trim().slice(0, 180);
}

async function toggleStar() {
  if (!state.current) return;
  state.current.starred = !state.current.starred;
  await saveNow({ starred: state.current.starred });
  renderEditor();
}

async function trashNote() {
  if (!state.current) return;
  await saveNow({ trashed: true });
  state.current = null;
  renderAll();
}

async function restoreNote() {
  await saveNow({ trashed: false });
  renderAll();
}

async function purgeNote() {
  if (!state.current) return;
  if (!confirm(`Permanently delete “${state.current.title || "Untitled"}”? This cannot be undone.`)) return;
  await fetch("/api/notes/" + encodeURIComponent(state.current.id), { method: "DELETE" });
  state.notes = state.notes.filter((n) => n.id !== state.current.id);
  state.current = null;
  renderAll();
}

function setMode(mode, persist = true) {
  state.mode = mode;
  if (persist) localStorage.setItem("pn-mode", mode);
  $("#panes").className = "mode-" + mode;
  $$("#modeSeg button").forEach((b) => b.classList.toggle("active", b.dataset.mode === mode));
  if (mode !== "edit") renderPreview();
}

function setTheme(dark) {
  document.body.classList.toggle("dark", dark);
  $("#hljsLight").disabled = dark;
  $("#hljsDark").disabled = !dark;
  $("#themeBtn").textContent = dark ? "☀️" : "🌙";
  localStorage.setItem("pn-theme", dark ? "dark" : "light");
}

async function runSearch() {
  const q = $("#search").value.trim();
  state.q = q;
  if (!q) { state.results = null; renderList(); return; }
  const r = await fetch("/api/search?q=" + encodeURIComponent(q) + (state.view === "trash" ? "&trash=1" : ""));
  state.results = await r.json();
  renderList();
}

/* markdown text helpers */
function mdWrap(before, after = before, placeholder = "") {
  const ta = $("#mdtext");
  const { selectionStart: s, selectionEnd: e, value: v } = ta;
  const sel = v.slice(s, e) || placeholder;
  ta.setRangeText(before + sel + after, s, e, "select");
  ta.setSelectionRange(s + before.length, s + before.length + sel.length);
  ta.focus();
  onBodyInput();
}

function mdLinePrefix(prefix) {
  const ta = $("#mdtext");
  const { selectionStart: s, selectionEnd: e, value: v } = ta;
  const ls = v.lastIndexOf("\n", s - 1) + 1;
  const le = v.indexOf("\n", e) === -1 ? v.length : v.indexOf("\n", e);
  const block = v.slice(ls, le);
  const lines = block.split("\n");
  const allHave = lines.every((l) => l.startsWith(prefix));
  const out = lines.map((l) => (allHave ? l.slice(prefix.length) : prefix + l)).join("\n");
  ta.setRangeText(out, ls, le, "end");
  ta.focus();
  onBodyInput();
}

function applyMd(kind) {
  switch (kind) {
    case "bold":   return mdWrap("**", "**", "bold");
    case "italic": return mdWrap("*", "*", "italic");
    case "code": {
      const ta = $("#mdtext");
      const sel = ta.value.slice(ta.selectionStart, ta.selectionEnd);
      return sel.includes("\n") ? mdWrap("```\n", "\n```", "code") : mdWrap("`", "`", "code");
    }
    case "link":  return mdWrap("[", "](url)", "text");
    case "h2":    return mdLinePrefix("## ");
    case "ul":    return mdLinePrefix("- ");
    case "quote": return mdLinePrefix("> ");
  }
}

/* exports */
function exportPrint() {
  if (state.current) window.open("/print/" + encodeURIComponent(state.current.id), "_blank");
  $("#exportMenu").open = false;
}

async function exportEisvogel(toc) {
  const n = state.current;
  if (!n) return;
  $("#exportMenu").open = false;
  setSaveState("saving", "Exporting PDF… (first run may take a few minutes)");
  try {
    if (state.dirty) await saveNow();
    const r = await fetch(`/api/export/pdf/${encodeURIComponent(n.id)}?titlepage=1${toc ? "&toc=1" : ""}`);
    if (!r.ok) {
      const err = await r.json().catch(() => ({}));
      alert("PDF export failed:\n\n" + (err.error || r.statusText));
      setSaveState("", "");
      return;
    }
    const blob = await r.blob();
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = (n.title || "note") + ".pdf";
    a.click();
    URL.revokeObjectURL(a.href);
    setSaveState("saved", "PDF exported ✓");
  } catch (e) {
    alert("PDF export failed: " + e);
    setSaveState("", "");
  }
}

/* ---------------------------------------------------------------- events */

function onBodyInput() {
  scheduleSave();
  renderStats();
  if (state.mode !== "edit") {
    clearTimeout(state.previewTimer);
    state.previewTimer = setTimeout(renderPreview, 120);
  }
}

function bindEvents() {
  // sidebar navigation
  $("#views").addEventListener("click", (e) => {
    const a = e.target.closest("a[data-view]");
    if (!a) return;
    state.view = a.dataset.view; state.tag = null; state.folder = null;
    runSearch();      // re-run (trash flag differs) then render
    renderAll();
  });
  $("#taglist").addEventListener("click", (e) => {
    const a = e.target.closest("a[data-tag]");
    if (!a) return;
    state.tag = a.dataset.tag; state.view = "all"; state.folder = null;
    renderAll();
  });

  // folders
  $("#folderlist").addEventListener("click", (e) => {
    const del = e.target.closest("[data-del]");
    if (del) { e.stopPropagation(); deleteFolder(del.dataset.del); return; }
    const a = e.target.closest("a[data-folder]");
    if (!a) return;
    state.folder = a.dataset.folder; state.view = "all"; state.tag = null;
    renderAll();
  });
  $("#folderlist").addEventListener("dblclick", (e) => {
    const a = e.target.closest("a[data-folder]");
    if (a) renameFolder(a.dataset.folder);
  });
  $("#newFolderBtn").addEventListener("click", () => createFolderFlow());
  $("#folderSel").addEventListener("change", onFolderSelect);

  // list
  $("#notelist").addEventListener("click", (e) => {
    const item = e.target.closest(".note-item");
    if (item) selectNote(item.dataset.id);
  });
  $("#newBtn").addEventListener("click", newNote);
  $("#sortSel").addEventListener("change", (e) => {
    state.sort = e.target.value;
    localStorage.setItem("pn-sort", state.sort);
    renderList();
  });
  $("#search").addEventListener("input", () => {
    clearTimeout(state.searchTimer);
    state.searchTimer = setTimeout(runSearch, 150);
  });
  $("#search").addEventListener("keydown", (e) => {
    if (e.key === "Escape") { e.target.value = ""; runSearch(); e.target.blur(); }
  });

  // editor
  $("#title").addEventListener("input", scheduleSave);
  $("#mdtext").addEventListener("input", onBodyInput);
  $("#mdtext").addEventListener("keydown", (e) => {
    if (e.key === "Tab") {
      e.preventDefault();
      $("#mdtext").setRangeText("  ", $("#mdtext").selectionStart, $("#mdtext").selectionEnd, "end");
      onBodyInput();
    }
    if ((e.ctrlKey || e.metaKey) && !e.altKey) {
      const k = e.key.toLowerCase();
      if (k === "b") { e.preventDefault(); applyMd("bold"); }
      if (k === "i") { e.preventDefault(); applyMd("italic"); }
      if (k === "l") { e.preventDefault(); applyMd("link"); }
      if (k === "`") { e.preventDefault(); applyMd("code"); }
    }
  });
  $$("#toolbar [data-md]").forEach((b) =>
    b.addEventListener("click", () => applyMd(b.dataset.md)));
  $$("#modeSeg button").forEach((b) =>
    b.addEventListener("click", () => setMode(b.dataset.mode)));

  // tags
  $("#tagInput").addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      addTag(e.target.value);
      e.target.value = "";
    } else if (e.key === "Backspace" && !e.target.value && state.current?.tags.length) {
      removeTag(state.current.tags[state.current.tags.length - 1]);
    }
  });
  $("#tagInput").addEventListener("change", (e) => {   // datalist click
    if (e.target.value) { addTag(e.target.value); e.target.value = ""; }
  });
  $("#tagchips").addEventListener("click", (e) => {
    if (e.target.classList.contains("x")) removeTag(e.target.dataset.tag);
  });

  // note actions
  $("#starBtn").addEventListener("click", toggleStar);
  $("#trashBtn").addEventListener("click", trashNote);
  $("#restoreBtn").addEventListener("click", restoreNote);
  $("#purgeBtn").addEventListener("click", purgeNote);
  $("#exPrint").addEventListener("click", exportPrint);
  $("#exEisvogel").addEventListener("click", () => exportEisvogel(false));
  $("#exEisvogelToc").addEventListener("click", () => exportEisvogel(true));
  document.addEventListener("click", (e) => {
    const menu = $("#exportMenu");
    if (menu.open && !menu.contains(e.target)) menu.open = false;
  });

  $("#themeBtn").addEventListener("click", () => setTheme(!document.body.classList.contains("dark")));

  // global shortcuts
  document.addEventListener("keydown", (e) => {
    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.altKey && e.key.toLowerCase() === "n") { e.preventDefault(); newNote(); }
    else if (mod && e.key.toLowerCase() === "k") { e.preventDefault(); $("#search").focus(); $("#search").select(); }
    else if (mod && e.key.toLowerCase() === "s") { e.preventDefault(); saveNow(); }
    else if (mod && e.key.toLowerCase() === "e" && state.current) {
      e.preventDefault();
      const order = ["edit", "split", "preview"];
      setMode(order[(order.indexOf(state.mode) + 1) % order.length]);
    }
  });

  window.addEventListener("beforeunload", () => {
    if (state.dirty && state.current) {
      navigator.sendBeacon && saveNow();
    }
  });
}

async function addTag(raw) {
  const t = raw.trim().replace(/,+$/, "");
  if (!t || !state.current || state.current.tags.includes(t)) return;
  state.current.tags = [...state.current.tags, t];
  renderTagChips();
  await saveNow();
  renderSidebar();
}

async function removeTag(t) {
  if (!state.current) return;
  state.current.tags = state.current.tags.filter((x) => x !== t);
  renderTagChips();
  await saveNow();
  renderSidebar();
}

/* ---------------------------------------------------------------- folders */

async function refreshFolders() {
  const folders = await fetch("/api/folders").then((r) => r.json());
  state.folders = folders.map((f) => f.name);
}

// assign the current note to the folder chosen in the editor dropdown
async function onFolderSelect(e) {
  if (!state.current) return;
  const val = e.target.value;
  if (val === "__new__") {
    const name = await createFolderFlow(true);
    if (!name) { renderFolderSelect(); return; }   // cancelled — restore selection
    state.current.folder = name;
  } else {
    state.current.folder = val;
  }
  await saveNow();
  renderSidebar();
  renderList();
  renderFolderSelect();
}

// create a folder; when assign=true, also return its name for the current note
async function createFolderFlow(assign) {
  const name = (prompt("New folder name:") || "").trim();
  if (!name) return "";
  const r = await fetch("/api/folders", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!r.ok) { alert("Could not create folder."); return ""; }
  const created = (await r.json()).name;
  await refreshFolders();
  if (!assign) {
    state.folder = created; state.view = "all"; state.tag = null;
  }
  renderSidebar();
  renderList();
  return created;
}

async function renameFolder(from) {
  const to = (prompt("Rename folder:", from) || "").trim();
  if (!to || to === from) return;
  await fetch("/api/folders/rename", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ from, to }),
  });
  await refreshFolders();
  // reflect the rename in already-loaded notes + active filter
  for (const n of state.notes) if (n.folder === from) n.folder = to;
  if (state.current && state.current.folder === from) state.current.folder = to;
  if (state.folder === from) state.folder = to;
  renderAll();
}

async function deleteFolder(name) {
  const count = folderCounts()[name] || 0;
  const msg = count
    ? `Delete folder “${name}”? Its ${count} note${count === 1 ? "" : "s"} will become uncategorized (not deleted).`
    : `Delete empty folder “${name}”?`;
  if (!confirm(msg)) return;
  await fetch("/api/folders/delete", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  await refreshFolders();
  for (const n of state.notes) if (n.folder === name) n.folder = "";
  if (state.current && state.current.folder === name) state.current.folder = "";
  if (state.folder === name) { state.folder = null; state.view = "all"; }
  renderAll();
}
