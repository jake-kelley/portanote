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
  folders: [],          // all folder paths ("Work/Projects/Alpha")
  collapsed: new Set(JSON.parse(localStorage.getItem("pn-folders-collapsed") || "[]")),
  templates: [],
  tasks: [],            // standalone to-do items
  taskDrag: null,       // task id being dragged for reorder
  selected: new Set(),  // multi-selected note ids
  lastClicked: null,
  dragging: null,
  q: "",
  results: null,        // search results when q is non-empty
  searchGlobal: false,  // an active folder scopes the search; this widens it back to all notes
  current: null,        // full Note being edited
  mode: localStorage.getItem("pn-mode") || "split",
  sort: localStorage.getItem("pn-sort") || "updated",
  dirty: false,
  saveTimer: null,
  searchTimer: null,
  previewTimer: null,
  claudeOpen: localStorage.getItem("pn-claude-open") === "1",  // Ask Claude drawer
  claudeStreaming: false,   // a turn is in flight
  claudeStopRequested: false,
  claudeAtBottom: true,     // auto-scroll the thread unless the user scrolled up
};

/* ---------------------------------------------------------------- init */

document.addEventListener("DOMContentLoaded", async () => {
  configureMarkdown();
  const themeParam = new URLSearchParams(location.search).get("theme");
  if (themeParam === "dark" || themeParam === "light") setTheme(themeParam === "dark");
  else if (localStorage.getItem("pn-theme") === "dark") setTheme(true);

  const [meta, notes, folders, templates, tasks] = await Promise.all([
    fetch("/api/meta").then((r) => r.json()),
    fetch("/api/notes").then((r) => r.json()),
    fetch("/api/folders").then((r) => r.json()),
    fetch("/api/templates").then((r) => r.json()),
    fetch("/api/tasks").then((r) => r.json()),
  ]);
  state.meta = meta;
  state.notes = notes;
  state.folders = folders.map((f) => f.name);
  state.templates = templates || [];
  state.tasks = tasks || [];
  renderTemplateMenu();

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

  // remember the last-used export toggles (title page defaults on, TOC off)
  $("#optTitlepage").checked = localStorage.getItem("pn-export-titlepage") === "1";
  $("#optToc").checked = localStorage.getItem("pn-export-toc") === "1";

  $("#sortSel").value = state.sort;
  bindEvents();
  initResizers();
  renderAll();

  // deep link: /?q=<query> pre-fills the search (operators work too)
  const params = new URLSearchParams(location.search);
  const q = params.get("q");
  if (q) { $("#search").value = q; runSearch(); }
  if (params.get("settings") === "1") openSettings();
  const view = params.get("view");
  if (["todo", "starred", "untagged", "trash"].includes(view)) { state.view = view; renderAll(); }

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
  // active search (free text and/or operators), filtered by operators
  if (state.q) {
    const p = state.parsed;
    let list = (state.results ? [...state.results] : [...state.notes]).filter((n) => matchesFilters(n, p));
    // an open folder scopes the search to its subtree, unless the user chose
    // "search all notes" or typed an explicit folder: operator (which wins)
    if (state.folder && !state.searchGlobal && !p.folders.length) {
      list = list.filter((n) => n.folder === state.folder || (n.folder || "").startsWith(state.folder + "/"));
    }
    if (!state.results) list.sort((a, b) => new Date(b.updated) - new Date(a.updated)); // else keep score order
    return list;
  }
  // no search: filter by the sidebar view / folder / tag
  let list = state.notes.filter((n) => {
    if (state.folder) return !n.trashed && n.folder && (n.folder === state.folder || n.folder.startsWith(state.folder + "/"));
    if (state.tag) return !n.trashed && n.tags.includes(state.tag);
    switch (state.view) {
      case "starred":  return n.starred && !n.trashed;
      case "untagged": return !n.trashed && n.tags.length === 0;
      case "trash":    return n.trashed;
      default:         return !n.trashed;
    }
  });
  const key = state.sort;
  list.sort((a, b) => {
    if (state.view !== "trash" && a.starred !== b.starred) return a.starred ? -1 : 1;
    if (key === "title") return a.title.localeCompare(b.title);
    return new Date(b[key]) - new Date(a[key]);
  });
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

function folderDirectCounts() {
  const counts = {};
  for (const n of state.notes) {
    if (n.trashed || !n.folder) continue;
    counts[n.folder] = (counts[n.folder] || 0) + 1;
  }
  return counts;
}

function saveCollapsed() {
  localStorage.setItem("pn-folders-collapsed", JSON.stringify([...state.collapsed]));
}

// nest the flat folder paths into a tree of {name, path, children:Map}
function buildFolderTree() {
  const root = { children: new Map() };
  for (const path of state.folders) {
    let node = root, acc = "";
    for (const seg of path.split("/")) {
      acc = acc ? acc + "/" + seg : seg;
      if (!node.children.has(seg)) node.children.set(seg, { name: seg, path: acc, children: new Map() });
      node = node.children.get(seg);
    }
  }
  return root;
}

function sortedChildren(node) {
  return [...node.children.values()].sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()));
}

function subtreeCount(node, direct) {
  let c = direct[node.path] || 0;
  for (const k of node.children.values()) c += subtreeCount(k, direct);
  return c;
}

// flat display rows honoring the collapsed set (for the sidebar)
function folderRows() {
  const direct = folderDirectCounts();
  const rows = [];
  const walk = (node, depth) => {
    for (const c of sortedChildren(node)) {
      const hasKids = c.children.size > 0;
      const collapsed = state.collapsed.has(c.path);
      rows.push({ path: c.path, name: c.name, depth, hasKids, collapsed, count: subtreeCount(c, direct) });
      if (hasKids && !collapsed) walk(c, depth + 1);
    }
  };
  walk(buildFolderTree(), 0);
  return rows;
}

// every folder in tree order with depth (for the editor's move-to dropdown)
function folderOrder() {
  const out = [];
  const walk = (node, depth) => {
    for (const c of sortedChildren(node)) {
      out.push({ path: c.path, name: c.name, depth });
      walk(c, depth + 1);
    }
  };
  walk(buildFolderTree(), 0);
  return out;
}

/* ---------------------------------------------------------------- rendering */

function renderAll() {
  renderSidebar();
  renderList();
  renderEditor();
  renderMain();
}

// switch between the normal 3-pane layout and the full-width To-Do list
function renderMain() {
  const todo = state.view === "todo";
  $("#listpane").hidden = todo;
  $$(".resizer").forEach((r) => (r.hidden = todo));
  $("#editor").hidden = todo;
  $("#boardpane").hidden = !todo;
  if (todo) { state.selected.clear(); renderTasks(); }
}

function noteTitleFor(noteId) {
  const n = state.notes.find((x) => x.id === noteId && !x.trashed);
  return n ? (n.title || "Untitled") : null;
}

function renderTasks() {
  const doneCount = state.tasks.filter((t) => t.done).length;
  const rows = state.tasks.map((t) => {
    const linkTitle = t.noteId ? noteTitleFor(t.noteId) : null;
    const link = t.noteId
      ? `<a class="task-note${linkTitle ? "" : " missing"}" data-note="${esc(t.noteId)}"
           title="${linkTitle ? "Open note: " + esc(linkTitle) : "Linked note no longer exists"}">🔗${linkTitle ? " " + esc(linkTitle) : ""}</a>`
      : "";
    return `<div class="task-item${t.done ? " done" : ""}" data-id="${esc(t.id)}" draggable="true">
      <span class="task-grip" title="Drag to reorder">⠿</span>
      <input type="checkbox" class="task-check" ${t.done ? "checked" : ""}>
      <span class="task-text" title="Double-click to edit">${esc(t.text)}</span>
      ${link}
      <button class="task-del" title="Delete task">✕</button>
    </div>`;
  }).join("");
  $("#boardpane").innerHTML = `<div class="todo">
    <div class="todo-topbar">
      <h2 class="todo-h">To-Do</h2>
      ${doneCount ? `<button id="clearDoneBtn" class="btn-secondary" title="Delete all completed tasks">Clear completed (${doneCount})</button>` : ""}
    </div>
    <div class="task-add"><input id="taskAddInput" placeholder="Add a task…  (press Enter)" autocomplete="off"></div>
    <div id="taskList">${rows || `<div class="task-empty">No tasks yet. Add one above, or use the ☑ button in a note's toolbar to create one linked to that note.</div>`}</div>
  </div>`;
}

async function createTask(text, noteId) {
  text = (text || "").trim();
  if (!text) return;
  const t = await fetch("/api/tasks", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, noteId: noteId || "" }),
  }).then((r) => r.json());
  state.tasks.push(t);
  renderSidebar();
  if (state.view === "todo") renderTasks();
}

async function toggleTask(id, done) {
  await fetch("/api/tasks/" + encodeURIComponent(id), {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ done }),
  });
  const t = state.tasks.find((x) => x.id === id);
  if (t) t.done = done;
  renderSidebar();
  renderTasks();
}

async function deleteTask(id) {
  await fetch("/api/tasks/" + encodeURIComponent(id), { method: "DELETE" });
  state.tasks = state.tasks.filter((x) => x.id !== id);
  renderSidebar();
  renderTasks();
}

async function editTask(id, text) {
  text = (text || "").trim();
  if (!text) return;
  await fetch("/api/tasks/" + encodeURIComponent(id), {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text }),
  });
  const t = state.tasks.find((x) => x.id === id);
  if (t) t.text = text;
  renderTasks();
}

async function clearDoneTasks() {
  state.tasks = await fetch("/api/tasks/clear-done", { method: "POST" }).then((r) => r.json());
  renderSidebar();
  renderTasks();
}

async function reorderTasks(ids) {
  state.tasks = await fetch("/api/tasks/reorder", {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ids }),
  }).then((r) => r.json());
  renderTasks();
}

// toolbar: create a to-do task linked back to the current note
async function createTaskFromNote() {
  if (!state.current) return;
  await createTask(state.current.title || "Untitled", state.current.id);
  setSaveState("saved", "☑ Task created");
}

// open the note a task links to
async function openTaskNote(noteId) {
  const n = state.notes.find((x) => x.id === noteId && !x.trashed);
  if (!n) { alert("That note no longer exists."); return; }
  state.view = "all"; state.tag = null; state.folder = null;
  renderAll();
  await selectNote(noteId);
}

function renderSidebar() {
  const notes = state.notes;
  $("#cAll").textContent = notes.filter((n) => !n.trashed).length;
  $("#cStarred").textContent = notes.filter((n) => n.starred && !n.trashed).length || "";
  $("#cUntagged").textContent = notes.filter((n) => !n.trashed && n.tags.length === 0).length || "";
  $("#cTrash").textContent = notes.filter((n) => n.trashed).length || "";
  $("#cTodo").textContent = state.tasks.filter((t) => !t.done).length || "";

  $$("#views a").forEach((a) =>
    a.classList.toggle("active", !state.tag && !state.folder && a.dataset.view === state.view));

  // folder tree (collapsible; subtree-inclusive counts; empty folders still show)
  if (state.folders.length) {
    $("#folderlist").innerHTML = folderRows().map((r) => {
      const toggle = r.hasKids
        ? `<span class="ftoggle" data-toggle="${esc(r.path)}">${r.collapsed ? "▸" : "▾"}</span>`
        : `<span class="ftoggle empty"></span>`;
      return `<a data-folder="${esc(r.path)}" class="frow ${state.folder === r.path ? "active" : ""}" style="padding-left:${8 + r.depth * 14}px">
        ${toggle}<span class="ficon">📁</span><span class="fname">${esc(r.name)}</span>
        <span class="count">${r.count || ""}</span>
        <span class="fadd" data-add="${esc(r.path)}" title="Add subfolder">＋</span>
        <span class="folder-x" data-del="${esc(r.path)}" title="Delete folder">✕</span></a>`;
    }).join("");
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
  // drop selections that are no longer visible
  for (const id of [...state.selected]) if (!list.some((n) => n.id === id)) state.selected.delete(id);
  renderBulkBar();
  renderSearchScope();
  $("#search").placeholder = state.folder ? `Search in ${state.folder}…  (Ctrl+K)` : "Search…  (Ctrl+K)";
  $("#listTitle").textContent = state.folder ? state.folder : state.tag ? "#" + state.tag :
    { all: "Notes", starred: "Starred", untagged: "Untagged", trash: "Trash" }[state.view];

  if (!list.length) {
    const scoped = state.q && state.folder && !state.searchGlobal && !(state.parsed?.folders.length);
    const msg = state.q ? "No results for “" + esc(state.q) + "”" + (scoped ? " in 📁 " + esc(state.folder) : "")
      : state.view === "trash" ? "Trash is empty"
      : state.folder ? "This folder is empty.<br>Open a note and pick this folder, or make a new one here."
      : "No notes here yet.<br>Press <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>N</kbd> to create one.";
    $("#notelist").innerHTML = `<div class="list-empty">${msg}</div>`;
    return;
  }

  const hl = (state.parsed && state.parsed.text) || "";
  $("#notelist").innerHTML = list.map((n) => {
    const title = hl ? highlight(n.title || "Untitled", hl) : esc(n.title || "Untitled");
    const snip = hl ? highlight(n.snippet, hl) : esc(n.snippet);
    const cls = (state.current?.id === n.id ? " active" : "") + (state.selected.has(n.id) ? " selected" : "");
    return `<div class="note-item${cls}" data-id="${esc(n.id)}" draggable="true">
      <input type="checkbox" class="note-check" ${state.selected.has(n.id) ? "checked" : ""} tabindex="-1" title="Select">
      <div class="ni-body">
        <div class="ni-top"><span class="ni-title">${title}</span>${n.starred ? '<span class="ni-star">★</span>' : ""}</div>
        ${snip ? `<div class="ni-snippet">${snip}</div>` : ""}
        <div class="ni-date">${fmtDate(n.updated, true)}</div>
        ${(n.folder && !state.folder) || n.tags.length ? `<div class="ni-tags">${
          n.folder && !state.folder ? `<span class="chip chip-folder">📁 ${esc(n.folder)}</span>` : ""
        }${n.tags.map((t) => `<span class="chip">${esc(t)}</span>`).join("")}</div>` : ""}
      </div>
    </div>`;
  }).join("");
  $("#notelist").classList.toggle("has-selection", state.selected.size > 0);
}

function renderEditor() {
  const n = state.current;
  $("#emptyState").style.display = n ? "none" : "";
  $("#editorMain").hidden = !n;
  renderClaudeUI();
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
  $("#optTitlepage").disabled = !eis;
  $("#optToc").disabled = !eis;
  $("#exNote").hidden = !!eis;
}

function renderTagChips() {
  $("#tagchips").innerHTML = state.current.tags.map((t) =>
    `<span class="tagchip">${esc(t)}<span class="x" data-tag="${esc(t)}" title="Remove">✕</span></span>`).join("");
}

function renderFolderSelect() {
  const cur = state.current.folder || "";
  const opts = [`<option value="">(no folder)</option>`];
  for (const f of folderOrder()) {
    const indent = "  ".repeat(f.depth);
    opts.push(`<option value="${esc(f.path)}" ${f.path === cur ? "selected" : ""}>${indent}${esc(f.name)}</option>`);
  }
  // a note may sit in a folder that isn't in the manifest (hand-edited) — show it
  if (cur && !state.folders.includes(cur)) {
    opts.push(`<option value="${esc(cur)}" selected>${esc(cur)}</option>`);
  }
  opts.push(`<option value="__new__">+ New folder…</option>`);
  $("#folderSel").innerHTML = opts.join("");
}

// GitHub-Flavored Markdown. (input tag/attrs kept so GFM task lists render.)
const PURIFY_CFG = { ADD_TAGS: ["input"], ADD_ATTR: ["type", "checked", "disabled"] };

function configureMarkdown() {
  const wikilink = {
    name: "wikilink", level: "inline",
    start(src) { return src.indexOf("[["); },
    tokenizer(src) {
      const m = /^\[\[([^\]|]+)(?:\|([^\]]*))?\]\]/.exec(src);
      if (m) return { type: "wikilink", raw: m[0], target: m[1].trim(), label: (m[2] || m[1]).trim() };
    },
    renderer(token) { return `<a class="wikilink" data-wikilink="${esc(token.target)}">${esc(token.label)}</a>`; },
  };
  marked.use({ gfm: true, breaks: true, extensions: [wikilink] });
}

function renderMarkdown(src) {
  return DOMPurify.sanitize(marked.parse(src || ""), PURIFY_CFG);
}

function renderPreview() {
  if (!state.current) return;
  $("#preview").innerHTML = renderMarkdown($("#mdtext").value);
  highlightBlocks($("#preview"));
  renderMermaid($("#preview"));
}

// syntax-highlight code blocks, leaving ```mermaid for the diagram renderer
function highlightBlocks(root) {
  root.querySelectorAll("pre code").forEach((el) => {
    if (!el.classList.contains("language-mermaid")) hljs.highlightElement(el);
  });
}

let mermaidReady = false;
let mermaidSeq = 0;
function renderMermaid(root) {
  const blocks = [...root.querySelectorAll("code.language-mermaid")];
  if (!blocks.length || !window.mermaid) return;
  if (!mermaidReady) {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: document.body.classList.contains("dark") ? "dark" : "default",
    });
    mermaidReady = true;
  }
  for (const code of blocks) {
    const src = code.textContent;
    const pre = code.closest("pre");
    const holder = document.createElement("div");
    holder.className = "mermaid-diagram";
    pre.replaceWith(holder);
    mermaid.render("mmd-" + (++mermaidSeq), src)
      .then(({ svg }) => { holder.innerHTML = svg; })
      .catch((e) => { holder.innerHTML = `<div class="mermaid-error">Mermaid: ${esc(String(e && e.message || e))}</div>`; });
  }
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
  refreshSuggestions();
  refreshBacklinks();
}

// notes whose [[wiki links]] point at the current note
async function refreshBacklinks() {
  const box = $("#backlinks");
  const n = state.current;
  if (!n || n.trashed) { box.hidden = true; return; }
  try {
    const items = await fetch(`/api/notes/${encodeURIComponent(n.id)}/backlinks`).then((r) => r.json());
    if (!items || !items.length) { box.hidden = true; $("#blList").innerHTML = ""; return; }
    $("#blCount").textContent = items.length;
    $("#blList").innerHTML = items.map((it) =>
      `<a class="bl-item" data-id="${esc(it.id)}"><b>${esc(it.title || "Untitled")}</b> <span class="bl-snip">${esc(it.snippet)}</span></a>`).join("");
    box.hidden = false;
  } catch { box.hidden = true; }
}

// follow a [[wiki link]]: open the matching note by title, or offer to create it
async function openWikilink(title) {
  const t = (title || "").trim().toLowerCase();
  const found = state.notes.find((n) => !n.trashed && (n.title || "").toLowerCase() === t);
  if (found) { selectNote(found.id); return; }
  if (!confirm(`No note titled “${title}”. Create it?`)) return;
  const r = await fetch("/api/notes", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ title }) });
  const n = await r.json();
  n.folder = state.folder || "";
  state.notes.unshift({ ...n, snippet: "" });
  state.current = n;
  renderAll();
  await saveNow();
  $("#title").focus();
}

// offline topic-tag suggestions (server-side TF-IDF); click a chip to accept
async function refreshSuggestions() {
  const row = $("#suggestrow");
  const n = state.current;
  if (!n || n.trashed) { row.hidden = true; return; }
  try {
    const res = await fetch(`/api/notes/${encodeURIComponent(n.id)}/suggest-tags`);
    const { tags } = await res.json();
    const fresh = (tags || []).filter((t) => !n.tags.includes(t));
    if (!fresh.length) { row.hidden = true; $("#suggestchips").innerHTML = ""; return; }
    $("#suggestchips").innerHTML = fresh.map((t) =>
      `<button class="suggest-chip" data-suggest="${esc(t)}" title="Add tag">${esc(t)}</button>`).join("");
    row.hidden = false;
  } catch { row.hidden = true; }
}

function renderTemplateMenu() {
  $("#tplMenu").hidden = false; // always available, so you can save new templates
  let html = "";
  if (state.templates.length) {
    html += `<div class="tpl-head">New from template</div>`;
    html += state.templates.map((t, i) =>
      `<div class="tpl-row">
         <button class="tpl-pick" data-tpl="${i}">${esc(t.name)}</button>
         <button class="tpl-del" data-tpldel="${esc(t.name)}" title="Delete template">✕</button>
       </div>`).join("");
    html += `<div class="mdiv"></div>`;
  }
  html += `<button class="tpl-save">＋ Save current note as template…</button>`;
  $("#tplList").innerHTML = html;
}

async function refreshTemplates() {
  state.templates = (await fetch("/api/templates").then((r) => r.json())) || [];
  renderTemplateMenu();
}

// save the open note's body as a reusable template
async function saveNoteAsTemplate() {
  $("#tplMenu").open = false;
  if (!state.current) { alert("Open a note first to save it as a template."); return; }
  const name = (prompt("Template name:", $("#title").value || "New Template") || "").trim();
  if (!name) return;
  if (state.templates.some((t) => t.name.toLowerCase() === name.toLowerCase()) &&
      !confirm(`A template named “${name}” already exists. Overwrite it?`)) return;
  const r = await fetch("/api/templates", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, body: $("#mdtext").value }),
  });
  if (!r.ok) { alert("Could not save template."); return; }
  await refreshTemplates();
  setSaveState("saved", "Template saved ✓");
}

async function deleteTemplate(name) {
  if (!confirm(`Delete the template “${name}”?`)) return;
  await fetch("/api/templates/delete", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }),
  });
  await refreshTemplates();
}

/* ---- multi-select + drag-and-drop ---- */

function renderBulkBar() {
  const bar = $("#bulkbar");
  if (!state.selected.size) { bar.hidden = true; return; }
  bar.hidden = false;
  const count = `<span id="bulkCount">${state.selected.size} selected</span>`;
  if (state.view === "trash") {
    bar.innerHTML = count +
      `<button data-bulk="restore" title="Restore selected">↩ Restore</button>` +
      `<button data-bulk="purge" class="bulk-danger" title="Permanently delete selected">🗑 Delete forever</button>` +
      `<button data-bulk="clear" title="Clear selection">✕</button>`;
    return;
  }
  bar.innerHTML = count + `<select id="bulkFolder"></select>` +
    `<button data-bulk="star" title="Star selected">⭐</button>` +
    `<button data-bulk="trash" title="Move selected to Trash">🗑</button>` +
    `<button data-bulk="clear" title="Clear selection">✕</button>`;
  $("#bulkFolder").innerHTML = `<option value="">Move to…</option><option value="__none__">(no folder)</option>` +
    folderOrder().map((f) => `<option value="${esc(f.path)}">${"  ".repeat(f.depth)}${esc(f.name)}</option>`).join("");
}

function toggleSelect(id) {
  if (state.selected.has(id)) state.selected.delete(id); else state.selected.add(id);
  state.lastClicked = id;
  renderList();
}

function rangeSelect(id) {
  const ids = visibleNotes().map((n) => n.id);
  const to = ids.indexOf(id);
  const from = state.lastClicked ? ids.indexOf(state.lastClicked) : to;
  if (from < 0 || to < 0) { state.selected.add(id); }
  else { const [a, b] = from < to ? [from, to] : [to, from]; for (let i = a; i <= b; i++) state.selected.add(ids[i]); }
  renderList();
}

// apply the same patch to a set of notes (bulk star/trash/move)
async function bulkApplyTo(ids, patch) {
  if (!ids || !ids.length) return;
  await Promise.all(ids.map((id) =>
    fetch("/api/notes/" + encodeURIComponent(id), {
      method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(patch),
    })));
  state.selected.clear();
  await reloadNotes();
}
const bulkApply = (patch) => bulkApplyTo([...state.selected], patch);
const moveNotes = (ids, folder) => bulkApplyTo(ids, { folder });

// permanently delete every selected (already-trashed) note
async function bulkDeleteForever() {
  const ids = [...state.selected];
  if (!ids.length) return;
  if (!confirm(`Permanently delete ${ids.length} note${ids.length === 1 ? "" : "s"}? This cannot be undone.`)) return;
  await Promise.all(ids.map((id) => fetch("/api/notes/" + encodeURIComponent(id), { method: "DELETE" })));
  if (state.current && ids.includes(state.current.id)) state.current = null;
  state.selected.clear();
  await reloadNotes();
}

// generic drop target: highlights on dragover, calls onDrop(target, ids) on drop
function bindDropTarget(container, selector, onDrop) {
  container.addEventListener("dragover", (e) => {
    const t = e.target.closest(selector);
    if (t && state.dragging) { e.preventDefault(); t.classList.add("drop-hover"); }
  });
  container.addEventListener("dragleave", (e) => {
    const t = e.target.closest(selector);
    if (t) t.classList.remove("drop-hover");
  });
  container.addEventListener("drop", (e) => {
    const t = e.target.closest(selector);
    if (!t || !state.dragging) return;
    e.preventDefault();
    t.classList.remove("drop-hover");
    const ids = state.dragging;
    state.dragging = null;
    onDrop(t, ids);
  });
}

async function newFromTemplate(tpl) {
  $("#tplMenu").open = false;
  const targetFolder = state.folder || "";
  const r = await fetch("/api/notes", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title: tpl.title || "" }),
  });
  const n = await r.json();
  n.folder = targetFolder;
  n.body = tpl.body || "";
  state.notes.unshift({ ...n, snippet: "" });
  if (state.view === "trash" || state.tag) { state.view = "all"; state.tag = null; }
  state.q = ""; state.results = null; state.searchGlobal = false; $("#search").value = "";
  state.current = n;
  renderAll();
  await saveNow();  // persist the template body + folder
  $("#title").focus();
}

async function newNote() {
  const r = await fetch("/api/notes", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
  const n = await r.json();
  // creating a note while a folder is open drops it into that folder
  const targetFolder = state.folder || "";
  n.folder = targetFolder;
  state.notes.unshift({ ...n, snippet: "" });
  if (state.view === "trash" || state.tag) { state.view = "all"; state.tag = null; }
  state.q = ""; state.results = null; state.searchGlobal = false; $("#search").value = "";
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
  refreshSuggestions();
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

async function openSettings() {
  const st = await fetch("/api/settings").then((r) => r.json());
  $("#setInterval").value = st.backupIntervalHours;
  $("#setKeep").value = st.backupKeep;
  renderBackupStatus(st);
  $("#updateStatus").textContent = "Portanote v" + state.meta.version;
  $("#updateApplyBtn").hidden = true;
  $("#updateCheckBtn").disabled = false;
  loadClaudeConfigUI();
  renderClaudeLog();
  $("#settingsOverlay").hidden = false;
}

// ---- Ask Claude: executable / settings config + activity log ----

function renderClaudeCfgStatus(c) {
  const parts = [c.available ? "✓ Ask Claude is enabled." : "Ask Claude is off — no claude executable found."];
  if (c.exeWarning) parts.push("⚠ " + c.exeWarning);
  if (c.settingsWarning) parts.push("⚠ " + c.settingsWarning);
  $("#claudeCfgStatus").textContent = parts.join("  ");
}

// fields show the effective value (override or detected); the detected value
// is also the placeholder, so clearing a field reads as "auto-detect"
async function loadClaudeConfigUI() {
  try {
    const c = await fetch("/api/claude/config").then((r) => r.json());
    $("#setClaudeExe").value = c.exe || c.detectedExe || "";
    $("#setClaudeExe").placeholder = c.detectedExe || "not found — enter a path";
    $("#setClaudeSettings").value = c.settingsFile || c.detectedSettings || "";
    $("#setClaudeSettings").placeholder = c.detectedSettings || "claude default";
    $("#setClaudeEnv").value = (c.env || []).join("\n");
    renderClaudeSettingsEnv(c.settingsEnv);
    renderClaudeCfgStatus(c);
  } catch {
    $("#claudeCfgStatus").textContent = "Could not load Claude settings.";
  }
}

// read-only note of the env vars Portanote auto-loads from settings.json's env
// block (keys only — values may be paths/secrets); merged into every turn
function renderClaudeSettingsEnv(settingsEnv) {
  const box = $("#claudeSettingsEnv");
  const keys = (settingsEnv || []).map((v) => v.split("=")[0]).filter(Boolean);
  if (!keys.length) { box.hidden = true; box.innerHTML = ""; return; }
  box.hidden = false;
  box.innerHTML = `<span class="cl-se-label">Auto-loaded from settings.json:</span> ` +
    keys.map((k) => `<code>${esc(k)}</code>`).join(" ");
}

async function saveClaudeConfig() {
  const btn = $("#claudeCfgSave");
  btn.disabled = true;
  try {
    const env = $("#setClaudeEnv").value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
    const c = await fetch("/api/claude/config", {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ exe: $("#setClaudeExe").value.trim(), settingsFile: $("#setClaudeSettings").value.trim(), env }),
    }).then((r) => r.json());
    $("#setClaudeExe").value = c.exe || c.detectedExe || "";
    $("#setClaudeSettings").value = c.settingsFile || c.detectedSettings || "";
    $("#setClaudeEnv").value = (c.env || []).join("\n");
    renderClaudeCfgStatus(c);
    // availability may have changed — refresh meta so the panel button appears/hides now
    state.meta = await fetch("/api/meta").then((r) => r.json());
    renderClaudeUI();
  } catch (e) {
    $("#claudeCfgStatus").textContent = "Save failed: " + ((e && e.message) || e);
  } finally {
    btn.disabled = false;
  }
}

async function renderClaudeLog() {
  const box = $("#claudeLogList");
  try {
    const logs = await fetch("/api/claude/logs").then((r) => r.json());
    if (!logs || !logs.length) { box.innerHTML = `<div class="cl-log-empty">No activity yet.</div>`; return; }
    box.innerHTML = logs.map((l) => {
      const when = new Date(l.time).toLocaleString();
      const dur = l.ms >= 1000 ? (l.ms / 1000).toFixed(1) + "s" : (l.ms || 0) + "ms";
      const meta = [l.noteTitle ? "📄 " + esc(l.noteTitle) : "", l.lines ? "✂ lines " + esc(l.lines) : "", dur]
        .filter(Boolean).join(" · ");
      return `<div class="cl-log-row${l.ok ? "" : " err"}">
        <div class="cl-log-top"><span class="cl-log-time">${esc(when)}</span><span class="cl-log-prompt">${esc(l.prompt)}</span></div>
        ${meta ? `<div class="cl-log-meta">${meta}</div>` : ""}
        ${l.error ? `<div class="cl-log-err">${esc(l.error)}</div>` : ""}
      </div>`;
    }).join("");
  } catch {
    box.innerHTML = `<div class="cl-log-empty">Could not load the log.</div>`;
  }
}

async function clearClaudeLog() {
  await fetch("/api/claude/logs/clear", { method: "POST" });
  renderClaudeLog();
}

async function checkForUpdates() {
  const st = $("#updateStatus"), btn = $("#updateCheckBtn");
  btn.disabled = true;
  st.textContent = "Checking…";
  $("#updateApplyBtn").hidden = true;
  try {
    const r = await fetch("/api/update/check");
    const info = await r.json();
    if (!r.ok) throw new Error(info.error || "check failed");
    if (info.available) {
      st.textContent = `${info.latest} is available (you have ${info.current}).`;
      $("#updateApplyBtn").hidden = false;
      $("#updateApplyBtn").disabled = false;
    } else {
      st.textContent = `You're up to date (${info.current}).`;
    }
  } catch (e) {
    st.textContent = "Update check failed: " + e.message;
  } finally {
    btn.disabled = false;
  }
}

async function installUpdate() {
  const st = $("#updateStatus");
  $("#updateApplyBtn").disabled = true;
  $("#updateCheckBtn").disabled = true;
  st.textContent = "Downloading and installing…";
  try {
    const r = await fetch("/api/update/apply", { method: "POST" });
    const res = await r.json();
    if (!r.ok) throw new Error(res.error || "update failed");
    st.textContent = `Installed ${res.version} — restarting…`;
    const before = state.meta.version;
    for (let i = 0; i < 60; i++) {          // poll for the new instance, ~45 s
      await new Promise((x) => setTimeout(x, 750));
      try {
        const meta = await fetch("/api/meta").then((x) => x.json());
        if (meta.version !== before) { location.reload(); return; }
      } catch { /* server is down mid-restart */ }
    }
    st.textContent = "The restart is taking a while — reload this page manually.";
  } catch (e) {
    st.textContent = "Update failed: " + e.message;
    $("#updateApplyBtn").disabled = false;
    $("#updateCheckBtn").disabled = false;
  }
}

// refresh the local view after a bulk change
async function reloadNotes() {
  const [notes, folders] = await Promise.all([
    fetch("/api/notes").then((r) => r.json()),
    fetch("/api/folders").then((r) => r.json()),
  ]);
  state.notes = notes;
  state.folders = folders.map((f) => f.name);
  renderAll();
}
function renderBackupStatus(st) {
  const last = st.lastBackup ? new Date(st.lastBackup).toLocaleString() : "none yet";
  $("#backupStatus").textContent = `${st.count} backup${st.count === 1 ? "" : "s"} on disk · last: ${last}`;
}
async function saveSettings() {
  const body = {
    backupIntervalHours: Math.max(1, +$("#setInterval").value || 3),
    backupKeep: Math.max(1, +$("#setKeep").value || 12),
  };
  const st = await fetch("/api/settings", {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  }).then((r) => r.json());
  renderBackupStatus(st);
  $("#settingsOverlay").hidden = true;
}
// re-index the notes folder from disk (external adds/edits/deletes), then refresh
async function syncNow() {
  const btn = $("#syncBtn");
  if (btn.disabled) return;
  btn.disabled = true;
  btn.classList.add("spin");
  try {
    if (state.dirty) await saveNow();           // don't let the open buffer fight the rescan
    const res = await fetch("/api/rescan", { method: "POST" }).then((r) => r.json());
    const [notes, folders, templates] = await Promise.all([
      fetch("/api/notes").then((r) => r.json()),
      fetch("/api/folders").then((r) => r.json()),
      fetch("/api/templates").then((r) => r.json()),
    ]);
    state.notes = notes;
    state.folders = folders.map((f) => f.name);
    state.templates = templates || [];
    renderTemplateMenu();
    if (state.current) {                        // re-fetch the open note; close it if it vanished
      const r = await fetch("/api/notes/" + encodeURIComponent(state.current.id));
      if (r.ok) {
        state.current = await r.json();
        renderEditor();
        refreshBacklinks();
      } else {
        state.current = null;
      }
    }
    renderAll();
    const delta = res.added + res.changed + res.removed;
    btn.title = `Synced — ${res.added} added, ${res.changed} changed, ${res.removed} removed (${res.total} note${res.total === 1 ? "" : "s"} total)`;
    btn.classList.remove("spin");
    btn.textContent = delta ? `+${delta}` : "✓";
    setTimeout(() => { btn.textContent = "⟳"; }, 1500);
  } finally {
    btn.classList.remove("spin");
    btn.disabled = false;
  }
}

async function backupNow() {
  $("#backupNowBtn").disabled = true;
  $("#backupNowBtn").textContent = "Backing up…";
  try {
    await fetch("/api/backup", { method: "POST" });
    renderBackupStatus(await fetch("/api/settings").then((r) => r.json()));
  } finally {
    $("#backupNowBtn").disabled = false;
    $("#backupNowBtn").textContent = "Back up now";
  }
}

function setTheme(dark) {
  document.body.classList.toggle("dark", dark);
  $("#hljsLight").disabled = dark;
  $("#hljsDark").disabled = !dark;
  $("#themeBtn").textContent = dark ? "☀️" : "🌙";
  localStorage.setItem("pn-theme", dark ? "dark" : "light");
  mermaidReady = false;                 // re-init mermaid with the new theme
  if (state.current && state.mode !== "edit") renderPreview();
}

// parse operators out of the query: tag:, folder:, is:(starred|untagged|trashed), before:, after:
function parseQuery(raw) {
  const p = { text: "", tags: [], folders: [], is: [], before: null, after: null };
  const words = [];
  for (const tok of raw.split(/\s+/)) {
    const m = /^(tag|folder|is|before|after):(.+)$/i.exec(tok);
    if (!m) { if (tok) words.push(tok); continue; }
    const key = m[1].toLowerCase(), val = m[2];
    if (key === "tag") p.tags.push(val.toLowerCase());
    else if (key === "folder") p.folders.push(val.toLowerCase());
    else if (key === "is") p.is.push(val.toLowerCase());
    else if (key === "before" || key === "after") {
      const d = new Date(val);
      if (!isNaN(d)) p[key] = d;
    }
  }
  p.text = words.join(" ");
  return p;
}

function matchesFilters(n, p) {
  const wantTrash = p.is.includes("trashed") || state.view === "trash";
  if (wantTrash ? !n.trashed : n.trashed) return false;
  for (const t of p.tags) if (!n.tags.some((x) => x.toLowerCase() === t)) return false;
  for (const f of p.folders) {
    const nf = (n.folder || "").toLowerCase();
    if (nf !== f && !nf.startsWith(f + "/")) return false;
  }
  if (p.is.includes("starred") && !n.starred) return false;
  if (p.is.includes("untagged") && n.tags.length) return false;
  if (p.before && new Date(n.updated) >= p.before) return false;
  if (p.after && new Date(n.updated) < p.after) return false;
  return true;
}

// scope toggle under the search box: shown only while searching inside a folder
// (the Notes view is already global); an explicit folder: operator suppresses it
function renderSearchScope() {
  const el = $("#searchScope");
  if (!state.folder || !state.q || state.parsed?.folders.length) {
    el.hidden = true;
    return;
  }
  el.hidden = false;
  el.innerHTML = state.searchGlobal
    ? `Searching <b>all notes</b> — click to search only 📁 <b>${esc(state.folder)}</b>`
    : `Searching in 📁 <b>${esc(state.folder)}</b> — click to search all notes`;
}

async function runSearch() {
  const raw = $("#search").value.trim();
  state.q = raw;
  state.parsed = parseQuery(raw);
  if (!raw) { state.results = null; state.searchGlobal = false; renderList(); return; }
  const wantTrash = state.parsed.is.includes("trashed") || state.view === "trash";
  if (state.parsed.text) {
    const r = await fetch("/api/search?q=" + encodeURIComponent(state.parsed.text) + (wantTrash ? "&trash=1" : ""));
    state.results = await r.json();
  } else {
    state.results = null; // operators only — filter all notes locally
  }
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

function mdInsertBlock(text) {
  const ta = $("#mdtext");
  ta.setRangeText(text, ta.selectionStart, ta.selectionEnd, "end");
  ta.focus();
  onBodyInput();
}

// paste an image from the clipboard -> upload -> insert markdown at the cursor
async function onPasteImage(e) {
  const item = [...(e.clipboardData?.items || [])].find((it) => it.type.startsWith("image/"));
  if (!item || !state.current) return; // let normal text paste through
  e.preventDefault();
  const file = item.getAsFile();
  if (!file) return;
  setSaveState("saving", "Uploading image…");
  try {
    const r = await fetch("/api/attachments", { method: "POST", headers: { "Content-Type": file.type }, body: file });
    if (!r.ok) { setSaveState("", "Image upload failed"); return; }
    const { path } = await r.json();
    mdInsertBlock(`![pasted image](${path})`);
  } catch {
    setSaveState("", "Image upload failed");
  }
}

function applyMd(kind) {
  switch (kind) {
    case "bold":      return mdWrap("**", "**", "bold");
    case "italic":    return mdWrap("*", "*", "italic");
    case "strike":    return mdWrap("~~", "~~", "strikethrough");
    case "code":      return mdWrap("`", "`", "code");
    case "codeblock": return mdWrap("```\n", "\n```", "code");
    case "link":      return mdWrap("[", "](https://)", "text");
    case "image":     return mdWrap("![", "](path/to/image.png)", "alt");
    case "h1":        return mdLinePrefix("# ");
    case "h2":        return mdLinePrefix("## ");
    case "h3":        return mdLinePrefix("### ");
    case "quote":     return mdLinePrefix("> ");
    case "ul":        return mdLinePrefix("- ");
    case "ol":        return mdLinePrefix("1. ");
    case "task":      return mdLinePrefix("- [ ] ");
    case "hr":        return mdInsertBlock("\n---\n");
    case "table":     return mdInsertBlock("\n| Column A | Column B |\n|----------|----------|\n| cell 1   | cell 2   |\n");
  }
}

/* exports */
function exportPrint() {
  if (state.current) window.open("/print/" + encodeURIComponent(state.current.id), "_blank");
  $("#exportMenu").open = false;
}

async function exportEisvogel() {
  const n = state.current;
  if (!n) return;
  const titlepage = $("#optTitlepage").checked;
  const toc = $("#optToc").checked;
  localStorage.setItem("pn-export-titlepage", titlepage ? "1" : "0");
  localStorage.setItem("pn-export-toc", toc ? "1" : "0");
  $("#exportMenu").open = false;
  setSaveState("saving", "Exporting PDF… (first run may take a few minutes)");
  try {
    if (state.dirty) await saveNow();
    const r = await fetch(`/api/export/pdf/${encodeURIComponent(n.id)}?titlepage=${titlepage ? 1 : 0}&toc=${toc ? 1 : 0}`);
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

/* ---------------------------------------------------------------- ask claude */
/* Right-hand drawer that talks to the local Claude Code CLI through the
   /api/claude endpoints. Phase 1: each message is an independent invocation
   (no memory of prior messages); the thread is kept client-side only. */

// canned prompts for the quick-action chips
const CLAUDE_QUICK = {
  summarize: "Summarize this note in a few bullet points.",
  improve: "Suggest concrete improvements to this note's writing and structure. Do not edit the note; list the suggestions.",
  tasks: "Extract any action items from this note and add each as a to-do task via add_task, linked to this note. Reply with the list of tasks you added.",
  tags: "Suggest 3-5 topic tags for this note. Do not apply them; just list them.",
};
const CLAUDE_EMPTY = `<div class="cl-empty">Ask about this note, or try a quick action.</div>`;

// show/hide the toolbar button and the drawer; when the backend reports
// claude:false (CLI not installed) no trace of the feature is visible
function renderClaudeUI() {
  const on = !!state.meta.claude;
  $("#claudeBtn").hidden = !on;
  $("#claudeBtn").classList.toggle("on", on && state.claudeOpen);
  $("#claudePanel").hidden = !(on && state.claudeOpen && state.current);
}

function claudeTogglePanel() {
  state.claudeOpen = !state.claudeOpen;
  localStorage.setItem("pn-claude-open", state.claudeOpen ? "1" : "0");
  renderClaudeUI();
  claudeUpdateSelInfo();
  if (!$("#claudePanel").hidden) $("#claudeInput").focus();
}

// empty the visual thread (client-side only)
function claudeClearThread() {
  if (state.claudeStreaming) return;
  $("#claudeThread").innerHTML = CLAUDE_EMPTY;
}

// append a message to the thread and return its element (html must be safe)
function claudeAppendMessage(cls, html) {
  const thread = $("#claudeThread");
  thread.querySelector(".cl-empty")?.remove();
  const div = document.createElement("div");
  div.className = "cl-msg " + cls;
  div.innerHTML = html;
  thread.appendChild(div);
  claudeScrollToBottom();
  return div;
}

function claudeScrollToBottom(force = false) {
  const t = $("#claudeThread");
  if (force || state.claudeAtBottom) t.scrollTop = t.scrollHeight;
}

// while a turn streams: composer disabled, Send becomes Stop, and the note
// inputs are frozen because Claude may edit the note server-side mid-turn
function claudeSetStreaming(on) {
  state.claudeStreaming = on;
  $("#claudeInput").disabled = on;
  $("#claudeSendBtn").hidden = on;
  $("#claudeStopBtn").hidden = !on;
  $("#claudeStopBtn").disabled = false;
  $$(".cl-chip").forEach((b) => (b.disabled = on));
  $("#mdtext").disabled = on;
  $("#title").disabled = on;
  claudeUpdateSelInfo(); // hidden while streaming; recomputed when the turn ends
}

async function claudeStop() {
  state.claudeStopRequested = true;
  $("#claudeStopBtn").disabled = true;
  try { await fetch("/api/claude/stop", { method: "POST" }); } catch { /* stream ends either way */ }
}

// keep the composer between one and ~three rows tall
function claudeGrowComposer() {
  const ta = $("#claudeInput");
  ta.style.height = "auto";
  ta.style.height = Math.min(ta.scrollHeight, 72) + "px";
}

// the highlighted region of the note editor, expanded to full 1-based lines
// (a textarea keeps its selection values even while blurred, so this works
// when the user has already clicked into the composer)
function claudeSelection() {
  const ta = $("#mdtext");
  if (!state.current || ta.selectionStart === ta.selectionEnd) return null;
  const v = ta.value;
  const s = ta.selectionStart;
  let e = ta.selectionEnd;
  if (v[e - 1] === "\n") e--;                     // a selection ending on \n shouldn't drag in the next line
  const ls = s === 0 ? 0 : v.lastIndexOf("\n", s - 1) + 1;
  let le = v.indexOf("\n", e);
  if (le < 0) le = v.length;
  const startLine = (v.slice(0, ls).match(/\n/g) || []).length + 1;
  const text = v.slice(ls, le);
  const endLine = startLine + (text.match(/\n/g) || []).length;
  return { startLine, endLine, text };
}

// small chip above the composer showing which lines the next message targets
function claudeUpdateSelInfo() {
  const el = $("#claudeSelInfo");
  const sel = state.meta.claude && state.claudeOpen && !state.claudeStreaming ? claudeSelection() : null;
  if (!sel) { el.hidden = true; return; }
  el.hidden = false;
  el.textContent = sel.startLine === sel.endLine
    ? `✂ Targeting line ${sel.startLine}`
    : `✂ Targeting lines ${sel.startLine}–${sel.endLine}`;
}

function claudeSendFromInput() {
  const ta = $("#claudeInput");
  const v = ta.value.trim();
  if (!v || state.claudeStreaming) return;
  ta.value = "";
  claudeGrowComposer();
  claudeSend(v);
}

// send one message and stream the reply (SSE over fetch)
async function claudeSend(message) {
  message = (message || "").trim();
  if (!message || state.claudeStreaming || !state.meta.claude) return;
  const selection = claudeSelection();            // read before save/renders can disturb it
  if (state.dirty) await saveNow();               // Claude reads the saved state
  const noteId = state.current ? state.current.id : "";

  state.claudeStopRequested = false;
  state.claudeAtBottom = true;
  claudeAppendMessage("cl-user", esc(message) + (selection
    ? `<span class="cl-selmeta">✂ lines ${selection.startLine}–${selection.endLine}</span>` : ""));
  const live = claudeAppendMessage("cl-assistant markdown", `<span class="cl-cursor"></span>`);
  claudeSetStreaming(true);

  let text = "", sawDone = false, sawError = false;
  // finalize whatever has streamed so far (drops the cursor)
  const finalize = () => {
    if (text) { live.innerHTML = renderMarkdown(text); highlightBlocks(live); }
    else live.remove();
  };
  // one SSE frame: `data: <json>` lines separated from the next frame by \n\n
  const handleFrame = (frame) => {
    for (const line of frame.split("\n")) {
      if (!line.startsWith("data:")) continue;
      let ev;
      try { ev = JSON.parse(line.slice(5)); } catch { continue; }
      if (ev.type === "delta") {
        text += ev.text || "";
        live.innerHTML = renderMarkdown(text) + `<span class="cl-cursor"></span>`;
        claudeScrollToBottom();
      } else if (ev.type === "done") {
        sawDone = true;
      } else if (ev.type === "error") {
        sawError = true;
        finalize();
        if (state.claudeStopRequested || /^stopped$/i.test((ev.error || "").trim())) {
          claudeAppendMessage("cl-system", "Stopped.");
        } else {
          claudeAppendMessage("cl-error", esc(ev.error || "Unknown error"));
        }
      }
    }
  };

  try {
    const r = await fetch("/api/claude/chat", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(selection ? { noteId, message, selection } : { noteId, message }),
    });
    if (!r.ok) {                                  // plain JSON: 409 turn running, 503 not installed
      const err = await r.json().catch(() => ({}));
      finalize();
      claudeAppendMessage("cl-error", esc(err.error || "Request failed (" + r.status + ")"));
      return;
    }

    // frames may split across chunks — buffer and split on the double newline
    const reader = r.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf("\n\n")) >= 0) { handleFrame(buf.slice(0, i)); buf = buf.slice(i + 2); }
    }
    buf += dec.decode();
    if (buf.trim()) handleFrame(buf);

    if (!sawError) {
      if (sawDone) finalize();
      else {                                      // stream closed without done/error
        finalize();
        if (state.claudeStopRequested) claudeAppendMessage("cl-system", "Stopped.");
        else claudeAppendMessage("cl-error", "The response stream ended unexpectedly.");
      }
    }
    // Claude may have edited the note or added tasks — pull everything fresh
    if ((sawDone && !sawError) || state.claudeStopRequested) await claudeRefreshAfterTurn(noteId);
  } catch (e) {
    finalize();
    claudeAppendMessage("cl-error", esc(String((e && e.message) || e)));
  } finally {
    claudeSetStreaming(false);
    if (!$("#settingsOverlay").hidden) renderClaudeLog(); // reflect this turn if the log is on screen
  }
}

// after a turn: re-fetch the open note, the tasks, and the note list so
// server-side edits and extracted tasks appear immediately
async function claudeRefreshAfterTurn(noteId) {
  try {
    const [noteRes, tasks] = await Promise.all([
      noteId ? fetch("/api/notes/" + encodeURIComponent(noteId)) : null,
      fetch("/api/tasks").then((r) => r.json()),
    ]);
    state.tasks = tasks || [];
    if (noteRes && noteRes.ok && state.current && state.current.id === noteId) {
      state.current = await noteRes.json();
      renderEditor();
      refreshBacklinks();
    }
    await reloadNotes();                          // notes + folders, then renderAll()
  } catch { /* non-fatal — the reply already rendered */ }
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

// drag-to-resize the sidebar and note-list panes; widths persist per pane
function initResizers() {
  const panes = {
    sidebar:  { el: $("#sidebar"),  key: "pn-w-sidebar",  min: 150, max: 460, def: 220 },
    listpane: { el: $("#listpane"), key: "pn-w-listpane", min: 220, max: 640, def: 320 },
  };
  for (const p of Object.values(panes)) {
    const saved = parseInt(localStorage.getItem(p.key) || "", 10);
    p.el.style.width = (saved >= p.min && saved <= p.max ? saved : p.def) + "px";
  }
  for (const handle of $$(".resizer")) {
    handle.addEventListener("mousedown", (e) => {
      e.preventDefault();
      const p = panes[handle.dataset.resize];
      const startX = e.clientX;
      const startW = p.el.getBoundingClientRect().width;
      handle.classList.add("dragging");
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      const onMove = (ev) => {
        const w = Math.max(p.min, Math.min(p.max, startW + ev.clientX - startX));
        p.el.style.width = w + "px";
      };
      const onUp = () => {
        handle.classList.remove("dragging");
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        localStorage.setItem(p.key, String(Math.round(p.el.getBoundingClientRect().width)));
        window.removeEventListener("mousemove", onMove);
        window.removeEventListener("mouseup", onUp);
      };
      window.addEventListener("mousemove", onMove);
      window.addEventListener("mouseup", onUp);
    });
  }
}

function bindEvents() {
  // sidebar navigation
  $("#views").addEventListener("click", (e) => {
    const a = e.target.closest("a[data-view]");
    if (!a) return;
    state.view = a.dataset.view; state.tag = null; state.folder = null; state.searchGlobal = false;
    runSearch();      // re-run (trash flag differs) then render
    renderAll();
  });
  $("#taglist").addEventListener("click", (e) => {
    const a = e.target.closest("a[data-tag]");
    if (!a) return;
    state.tag = a.dataset.tag; state.view = "all"; state.folder = null; state.searchGlobal = false;
    renderAll();
  });

  // folder tree
  $("#folderlist").addEventListener("click", (e) => {
    const tog = e.target.closest("[data-toggle]");
    if (tog) { e.stopPropagation(); toggleFolder(tog.dataset.toggle); return; }
    const add = e.target.closest("[data-add]");
    if (add) { e.stopPropagation(); createFolderFlow(false, add.dataset.add); return; }
    const del = e.target.closest("[data-del]");
    if (del) { e.stopPropagation(); deleteFolder(del.dataset.del); return; }
    const a = e.target.closest("a[data-folder]");
    if (a) { state.folder = a.dataset.folder; state.view = "all"; state.tag = null; state.searchGlobal = false; renderAll(); }
  });
  $("#folderlist").addEventListener("dblclick", (e) => {
    const a = e.target.closest("a[data-folder]");
    if (a) renameFolder(a.dataset.folder);
  });
  $("#newFolderBtn").addEventListener("click", () => createFolderFlow(false, ""));
  $("#folderSel").addEventListener("change", onFolderSelect);

  // list: click (open), Ctrl/Cmd+click (toggle), Shift+click (range)
  $("#notelist").addEventListener("click", (e) => {
    const item = e.target.closest(".note-item");
    if (!item) return;
    const id = item.dataset.id;
    if (e.target.classList.contains("note-check")) {
      e.stopPropagation();
      if (e.target.checked) state.selected.add(id); else state.selected.delete(id);
      state.lastClicked = id;
      renderList();
      return;
    }
    if (e.ctrlKey || e.metaKey) { toggleSelect(id); return; }
    if (e.shiftKey) { rangeSelect(id); return; }
    if (state.selected.size) { state.selected.clear(); renderList(); }
    selectNote(id);
  });
  // drag notes -> drop onto a folder (move) or the Trash view
  $("#notelist").addEventListener("dragstart", (e) => {
    const item = e.target.closest(".note-item");
    if (!item) return;
    const id = item.dataset.id;
    state.dragging = state.selected.has(id) ? [...state.selected] : [id];
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", state.dragging.join(","));
  });
  bindDropTarget($("#folderlist"), "a[data-folder]", (a, ids) => moveNotes(ids, a.dataset.folder));
  bindDropTarget($("#views"), 'a[data-view="trash"]', (_, ids) => bulkApplyTo(ids, { trashed: true }));
  // wiki-link navigation + backlink items
  $("#preview").addEventListener("click", (e) => {
    const w = e.target.closest(".wikilink");
    if (w) { e.preventDefault(); openWikilink(w.dataset.wikilink); }
  });
  $("#blList").addEventListener("click", (e) => {
    const a = e.target.closest("[data-id]");
    if (a) selectNote(a.dataset.id);
  });
  // bulk-action bar (buttons are re-rendered per view, so delegate)
  $("#bulkbar").addEventListener("click", (e) => {
    const b = e.target.closest("[data-bulk]");
    if (!b) return;
    switch (b.dataset.bulk) {
      case "star":    bulkApply({ starred: true }); break;
      case "trash":   bulkApply({ trashed: true }); break;
      case "restore": bulkApply({ trashed: false }); break;
      case "purge":   bulkDeleteForever(); break;
      case "clear":   state.selected.clear(); renderList(); break;
    }
  });
  $("#bulkbar").addEventListener("change", (e) => {
    if (e.target.id === "bulkFolder" && e.target.value) {
      bulkApply({ folder: e.target.value === "__none__" ? "" : e.target.value });
    }
  });

  $("#newBtn").addEventListener("click", newNote);
  $("#tplList").addEventListener("click", (e) => {
    const del = e.target.closest("[data-tpldel]");
    if (del) { e.stopPropagation(); deleteTemplate(del.dataset.tpldel); return; }
    if (e.target.closest(".tpl-save")) { saveNoteAsTemplate(); return; }
    const b = e.target.closest("[data-tpl]");
    if (b) newFromTemplate(state.templates[+b.dataset.tpl]);
  });
  $("#sortSel").addEventListener("change", (e) => {
    state.sort = e.target.value;
    localStorage.setItem("pn-sort", state.sort);
    renderList();
  });
  $("#search").addEventListener("input", () => {
    clearTimeout(state.searchTimer);
    state.searchTimer = setTimeout(runSearch, 150);
  });
  $("#searchScope").addEventListener("click", () => {
    state.searchGlobal = !state.searchGlobal;
    renderList();
  });
  $("#search").addEventListener("keydown", (e) => {
    if (e.key === "Escape") { e.target.value = ""; runSearch(); e.target.blur(); }
  });

  // editor
  $("#title").addEventListener("input", scheduleSave);
  $("#mdtext").addEventListener("input", onBodyInput);
  $("#mdtext").addEventListener("paste", onPasteImage);
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
  // create a task linked to the current note
  $("#taskFromNoteBtn").addEventListener("click", createTaskFromNote);

  // to-do list interactions
  $("#boardpane").addEventListener("click", (e) => {
    if (e.target.id === "clearDoneBtn") { clearDoneTasks(); return; }
    const del = e.target.closest(".task-del");
    if (del) { deleteTask(del.closest(".task-item").dataset.id); return; }
    const link = e.target.closest(".task-note");
    if (link) { openTaskNote(link.dataset.note); return; }
    if (e.target.classList.contains("task-check")) {
      toggleTask(e.target.closest(".task-item").dataset.id, e.target.checked);
    }
  });
  $("#boardpane").addEventListener("dblclick", (e) => {
    const txt = e.target.closest(".task-text");
    if (!txt) return;
    const id = txt.closest(".task-item").dataset.id;
    const t = state.tasks.find((x) => x.id === id);
    const nv = prompt("Edit task:", t ? t.text : "");
    if (nv !== null) editTask(id, nv);
  });
  $("#boardpane").addEventListener("keydown", (e) => {
    if (e.target.id === "taskAddInput" && e.key === "Enter" && e.target.value.trim()) {
      const v = e.target.value; e.target.value = "";
      createTask(v).then(() => { const inp = $("#taskAddInput"); if (inp) inp.focus(); });
    }
  });
  // drag-to-reorder tasks
  $("#boardpane").addEventListener("dragstart", (e) => {
    const item = e.target.closest(".task-item");
    if (item) { state.taskDrag = item.dataset.id; e.dataTransfer.effectAllowed = "move"; }
  });
  $("#boardpane").addEventListener("dragover", (e) => {
    const item = e.target.closest(".task-item");
    if (item && state.taskDrag) { e.preventDefault(); item.classList.add("task-over"); }
  });
  $("#boardpane").addEventListener("dragleave", (e) => {
    const item = e.target.closest(".task-item");
    if (item) item.classList.remove("task-over");
  });
  $("#boardpane").addEventListener("drop", (e) => {
    const item = e.target.closest(".task-item");
    if (!item || !state.taskDrag) return;
    e.preventDefault();
    item.classList.remove("task-over");
    const dragId = state.taskDrag; state.taskDrag = null;
    if (dragId === item.dataset.id) return;
    const ids = state.tasks.map((t) => t.id).filter((x) => x !== dragId);
    ids.splice(ids.indexOf(item.dataset.id), 0, dragId);
    reorderTasks(ids);
  });

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
  $("#suggestchips").addEventListener("click", (e) => {
    const b = e.target.closest("[data-suggest]");
    if (b) addTag(b.dataset.suggest);
  });

  // note actions
  $("#starBtn").addEventListener("click", toggleStar);
  $("#trashBtn").addEventListener("click", trashNote);
  $("#restoreBtn").addEventListener("click", restoreNote);
  $("#purgeBtn").addEventListener("click", purgeNote);
  $("#exPrint").addEventListener("click", exportPrint);
  $("#exEisvogel").addEventListener("click", () => exportEisvogel());
  // markdown quick-reference modal
  $("#cheatBtn").addEventListener("click", () => { $("#cheatOverlay").hidden = false; });
  $("#cheatClose").addEventListener("click", () => { $("#cheatOverlay").hidden = true; });
  $("#cheatOverlay").addEventListener("click", (e) => {
    if (e.target.id === "cheatOverlay") $("#cheatOverlay").hidden = true;
  });

  // Ask Claude panel
  $("#claudeBtn").addEventListener("click", claudeTogglePanel);
  $("#claudeCloseBtn").addEventListener("click", claudeTogglePanel);
  $("#claudeClearBtn").addEventListener("click", claudeClearThread);
  $("#claudeSendBtn").addEventListener("click", claudeSendFromInput);
  $("#claudeStopBtn").addEventListener("click", claudeStop);
  $("#claudeQuick").addEventListener("click", (e) => {
    const b = e.target.closest("[data-claude-quick]");
    if (b) claudeSend(CLAUDE_QUICK[b.dataset.claudeQuick]);
  });
  $("#claudeInput").addEventListener("input", claudeGrowComposer);
  $("#claudeInput").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); claudeSendFromInput(); }
    else if (e.key === "Escape") e.target.blur();
  });
  // highlighted note lines become the next message's target — track live
  for (const evt of ["select", "keyup", "mouseup"]) {
    $("#mdtext").addEventListener(evt, claudeUpdateSelInfo);
  }
  // auto-scroll only while the user is at the bottom of the thread
  $("#claudeThread").addEventListener("scroll", (e) => {
    const t = e.target;
    state.claudeAtBottom = t.scrollHeight - t.scrollTop - t.clientHeight < 40;
  });
  // [[wiki links]] in Claude replies navigate like the preview's
  $("#claudeThread").addEventListener("click", (e) => {
    const w = e.target.closest(".wikilink");
    if (w) { e.preventDefault(); openWikilink(w.dataset.wikilink); }
  });

  // close popover menus on an outside click
  document.addEventListener("click", (e) => {
    for (const id of ["#exportMenu", "#tplMenu"]) {
      const menu = $(id);
      if (menu.open && !menu.contains(e.target)) menu.open = false;
    }
  });

  $("#themeBtn").addEventListener("click", () => setTheme(!document.body.classList.contains("dark")));

  // settings modal
  $("#settingsBtn").addEventListener("click", openSettings);
  $("#syncBtn").addEventListener("click", syncNow);
  $("#updateCheckBtn").addEventListener("click", checkForUpdates);
  $("#updateApplyBtn").addEventListener("click", installUpdate);
  $("#settingsClose").addEventListener("click", () => { $("#settingsOverlay").hidden = true; });
  $("#settingsOverlay").addEventListener("click", (e) => {
    if (e.target.id === "settingsOverlay") $("#settingsOverlay").hidden = true;
  });
  $("#settingsSave").addEventListener("click", saveSettings);
  $("#backupNowBtn").addEventListener("click", backupNow);
  $("#claudeCfgSave").addEventListener("click", saveClaudeConfig);
  $("#claudeLogClear").addEventListener("click", clearClaudeLog);

  // global shortcuts
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !$("#cheatOverlay").hidden) { $("#cheatOverlay").hidden = true; return; }
    if (e.key === "Escape" && !$("#settingsOverlay").hidden) { $("#settingsOverlay").hidden = true; return; }
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

function toggleFolder(path) {
  if (state.collapsed.has(path)) state.collapsed.delete(path);
  else state.collapsed.add(path);
  saveCollapsed();
  renderSidebar();
}

// create a folder (optionally nested under parentPath). When assign=true, return
// the created path for the current note instead of navigating to it.
async function createFolderFlow(assign, parentPath) {
  const prompt_ = parentPath
    ? `New subfolder inside “${parentPath}”:`
    : "New folder (use / to nest, e.g. Work/Projects):";
  const name = (prompt(prompt_) || "").trim();
  if (!name) return "";
  const path = parentPath ? parentPath + "/" + name : name;
  const r = await fetch("/api/folders", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: path }),
  });
  if (!r.ok) { alert("Could not create folder."); return ""; }
  const created = (await r.json()).name;
  await refreshFolders();
  if (parentPath) { state.collapsed.delete(parentPath); saveCollapsed(); } // reveal the new child
  if (!assign) { state.folder = created; state.view = "all"; state.tag = null; }
  renderSidebar();
  renderList();
  return created;
}

// remap a folder path (and its descendants) from -> to
function remapFolder(f, from, to) {
  if (f === from) return to;
  if (f && f.startsWith(from + "/")) return to + f.slice(from.length);
  return f;
}

async function renameFolder(path) {
  const leaf = path.split("/").pop();
  const to = (prompt("Rename folder:", leaf) || "").trim();
  if (!to || to === leaf) return;
  const parent = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
  const newPath = parent ? parent + "/" + to : to;
  await fetch("/api/folders/rename", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ from: path, to: newPath }),
  });
  await refreshFolders();
  for (const n of state.notes) n.folder = remapFolder(n.folder, path, newPath);
  if (state.current) state.current.folder = remapFolder(state.current.folder, path, newPath);
  if (state.folder) state.folder = remapFolder(state.folder, path, newPath);
  renderAll();
}

async function deleteFolder(path) {
  const inSub = (f) => f && (f === path || f.startsWith(path + "/"));
  const affected = state.notes.filter((n) => !n.trashed && inSub(n.folder)).length;
  const hasKids = state.folders.some((f) => f.startsWith(path + "/"));
  const msg = `Delete folder “${path}”${hasKids ? " and its subfolders" : ""}?` +
    (affected ? ` ${affected} note${affected === 1 ? "" : "s"} will become uncategorized (not deleted).` : "");
  if (!confirm(msg)) return;
  await fetch("/api/folders/delete", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: path }),
  });
  await refreshFolders();
  for (const n of state.notes) if (inSub(n.folder)) n.folder = "";
  if (state.current && inSub(state.current.folder)) state.current.folder = "";
  if (inSub(state.folder)) { state.folder = null; state.view = "all"; }
  renderAll();
}
