---
type: Archived Design Note
title: Inline text color + highlighting (archived)
description: Exact implementation of the shelved ==highlight== and inline-color editor feature, kept so it can be restored.
tags: [archive, editor, markdown, ui]
timestamp: 2026-07-03T17:02:14-06:00
---

# Archived: inline text color + highlighting

Set aside on request ("archive for now"). This documents the exact implementation
so it can be restored. Nothing here is wired into the live app.

The feature added: `==highlight==` (Obsidian-style) rendering to `<mark>`, and a
toolbar **A** menu that inserted inline `<span style="color:…">` / `<mark
style="background-color:…">`. A DOMPurify hook restricted `style` to `color` /
`background-color` only. Pandoc export used `+mark` so `==highlight==` reached the PDF.

## To restore

### `ui/app.js`

`configureMarkdown()` — add the highlight extension + style hook:
```js
const highlightExt = {
  name: "highlight", level: "inline",
  start(src) { return src.indexOf("=="); },
  tokenizer(src) {
    const m = /^==(?=\S)([\s\S]*?\S)==/.exec(src);
    if (m) return { type: "highlight", raw: m[0], text: m[1], tokens: this.lexer.inlineTokens(m[1]) };
  },
  renderer(token) { return `<mark>${this.parser.parseInline(token.tokens)}</mark>`; },
};
marked.use({ gfm: true, breaks: true, extensions: [highlightExt] });

DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (!node.hasAttribute || !node.hasAttribute("style")) return;
  const safe = node.getAttribute("style").split(";").map((d) => d.trim())
    .filter((d) => /^(color|background-color)\s*:\s*(#[0-9a-fA-F]{3,8}|rgb\([\d\s,.%]+\)|[a-zA-Z]+)$/.test(d))
    .join("; ");
  if (safe) node.setAttribute("style", safe); else node.removeAttribute("style");
});
```

`PURIFY_CFG` — add `mark` tag and `style` attr:
```js
const PURIFY_CFG = { ADD_TAGS: ["mark", "input"], ADD_ATTR: ["style", "type", "checked", "disabled"] };
```

`applyMd()` — add the highlight case:
```js
case "highlight": return mdWrap("==", "==", "highlight");
```

Add `applyColor` and its binding:
```js
function applyColor(kind, color) {
  if (!color) return;
  if (kind === "mark") mdWrap(`<mark style="background-color:${color}">`, "</mark>", "text");
  else mdWrap(`<span style="color:${color}">`, "</span>", "text");
  $("#colorMenu").open = false;
}
// in bindEvents():
$("#colorMenu").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-color]");
  if (b) applyColor(b.parentElement.dataset.kind, b.dataset.color);
});
// and include "#colorMenu" in the outside-click close loop
```

### `ui/index.html`

Toolbar — the highlight button and color menu (goes in the first `.tb-group`):
```html
<button data-md="highlight" title="Highlight  ==text==">🖍</button>
<details id="colorMenu" class="tb-color">
  <summary title="Text color &amp; highlight color"><span class="a-ic">A</span></summary>
  <div class="cmenu">
    <div class="cmlabel">Text color</div>
    <div class="swatches" data-kind="color">
      <button style="--sw:#e02b2b" data-color="#e02b2b" title="red"></button>
      <button style="--sw:#e8730c" data-color="#e8730c" title="orange"></button>
      <button style="--sw:#1a8a53" data-color="#1a8a53" title="green"></button>
      <button style="--sw:#086dd6" data-color="#086dd6" title="blue"></button>
      <button style="--sw:#8850cf" data-color="#8850cf" title="purple"></button>
      <button style="--sw:#5b6b7b" data-color="#5b6b7b" title="grey"></button>
    </div>
    <div class="cmlabel">Highlight</div>
    <div class="swatches" data-kind="mark">
      <button style="--sw:#fff3a3" data-color="#fff3a3" title="yellow"></button>
      <button style="--sw:#c3f0c8" data-color="#c3f0c8" title="green"></button>
      <button style="--sw:#bfe3ff" data-color="#bfe3ff" title="blue"></button>
      <button style="--sw:#ffd0e0" data-color="#ffd0e0" title="pink"></button>
    </div>
  </div>
</details>
```

Cheatsheet — the `==highlight==` row (in the Emphasis table) and the Color & highlight section:
```html
<tr><td><code>==highlight==</code></td><td><mark>highlighted</mark> <span class="cheat-note">(Portanote)</span></td></tr>
```
```html
<section>
  <h3>Color &amp; highlight <span class="cheat-note">(inline HTML — Portanote)</span></h3>
  <table class="cheat">
    <tr><td><code>&lt;span style="color:#e02b2b"&gt;red&lt;/span&gt;</code></td><td><span style="color:#e02b2b">red</span></td></tr>
    <tr><td><code>&lt;mark style="background-color:#bfe3ff"&gt;blue&lt;/mark&gt;</code></td><td><mark style="background-color:#bfe3ff">blue hl</mark></td></tr>
  </table>
  <p class="cheat-tip">Use the <b>🖍 / A</b> toolbar buttons to insert these. Only <code>color</code> and <code>background-color</code> are allowed for safety.</p>
</section>
```

### `ui/app.css`
```css
.tb-color{position:relative;display:flex;}
.tb-color summary{list-style:none;cursor:pointer;padding:4px 9px;border-radius:6px;font-size:13px;}
.tb-color summary::-webkit-details-marker{display:none;}
.tb-color summary:hover{background:var(--bg3);}
.a-ic{font-weight:800;border-bottom:3px solid var(--accent);padding-bottom:1px;}
.cmenu{position:absolute;left:0;top:32px;z-index:50;background:var(--bg);border:1px solid var(--line);
  border-radius:10px;box-shadow:0 8px 24px rgba(16,24,40,.14);padding:8px;width:168px;}
.cmlabel{font-size:10px;text-transform:uppercase;letter-spacing:.5px;color:var(--muted);font-weight:800;margin:2px 2px 5px;}
.swatches{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:8px;}
.swatches button{width:22px;height:22px;border-radius:6px;border:1px solid var(--line);background:var(--sw);cursor:pointer;padding:0;}
.swatches button:hover{transform:scale(1.12);}
```

### `ui/print.html`
Mirror the same highlight extension + style hook + `PURIFY_CFG` (`mark`, `style`) as app.js.

### `export.go`
Re-add pandoc's `mark` extension so `==highlight==` renders in the PDF:
```go
"--from", "markdown+emoji+mark-raw_tex-raw_attribute",
```
