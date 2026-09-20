// src/ansi.ts
var BASE = [
  "#000000",
  "#cd3131",
  "#0dbc79",
  "#e5e510",
  "#2472c8",
  "#bc3fbc",
  "#11a8cd",
  "#e5e5e5",
  "#666666",
  "#f14c4c",
  "#23d18b",
  "#f5f543",
  "#3b8eea",
  "#d670d6",
  "#29b8db",
  "#e5e5e5"
];
function xterm256(n) {
  if (n < 16) return BASE[n];
  if (n < 232) {
    const c = n - 16;
    const level = (v) => v === 0 ? 0 : 55 + v * 40;
    return rgb(level(Math.floor(c / 36) % 6), level(Math.floor(c / 6) % 6), level(c % 6));
  }
  const grey = 8 + (n - 232) * 10;
  return rgb(grey, grey, grey);
}
function rgb(r, g, b) {
  const hex = (v) => Math.max(0, Math.min(255, v)).toString(16).padStart(2, "0");
  return `#${hex(r)}${hex(g)}${hex(b)}`;
}
function blank() {
  return { fg: null, bg: null, bold: false, dim: false, italic: false, underline: false, reverse: false };
}
function apply(style, params) {
  for (let i = 0; i < params.length; i++) {
    const p = params[i];
    if (p === 0) Object.assign(style, blank());
    else if (p === 1) style.bold = true;
    else if (p === 2) style.dim = true;
    else if (p === 3) style.italic = true;
    else if (p === 4) style.underline = true;
    else if (p === 7) style.reverse = true;
    else if (p === 22) {
      style.bold = false;
      style.dim = false;
    } else if (p === 23) style.italic = false;
    else if (p === 24) style.underline = false;
    else if (p === 27) style.reverse = false;
    else if (p >= 30 && p <= 37) style.fg = BASE[p - 30];
    else if (p >= 90 && p <= 97) style.fg = BASE[p - 90 + 8];
    else if (p >= 40 && p <= 47) style.bg = BASE[p - 40];
    else if (p >= 100 && p <= 107) style.bg = BASE[p - 100 + 8];
    else if (p === 39) style.fg = null;
    else if (p === 49) style.bg = null;
    else if (p === 38 || p === 48) {
      const target = p === 38 ? "fg" : "bg";
      if (params[i + 1] === 5) {
        style[target] = xterm256(params[i + 2] ?? 0);
        i += 2;
      } else if (params[i + 1] === 2) {
        style[target] = rgb(params[i + 2], params[i + 3], params[i + 4]);
        i += 4;
      }
    }
  }
}
function css(style) {
  const parts = [];
  const fg = style.reverse ? style.bg ?? "#000000" : style.fg;
  const bg = style.reverse ? style.fg ?? "#cccccc" : style.bg;
  if (fg) parts.push(`color:${fg}`);
  if (bg) parts.push(`background:${bg}`);
  if (style.bold) parts.push("font-weight:700");
  if (style.dim) parts.push("opacity:.6");
  if (style.italic) parts.push("font-style:italic");
  if (style.underline) parts.push("text-decoration:underline");
  return parts.join(";");
}
var ESCAPE = /\x1b\[([0-9;:]*)m/g;
function toHTML(text) {
  const style = blank();
  let html = "";
  let at = 0;
  ESCAPE.lastIndex = 0;
  for (let m = ESCAPE.exec(text); m; m = ESCAPE.exec(text)) {
    html += span(text.slice(at, m.index), style);
    apply(style, m[1].split(/[;:]/).map((v) => v === "" ? 0 : Number(v)));
    at = m.index + m[0].length;
  }
  html += span(text.slice(at), style);
  return html.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, "");
}
function span(chunk, style) {
  if (chunk === "") return "";
  const escaped = chunk.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
  const rule = css(style);
  return rule ? `<span style="${rule}">${escaped}</span>` : escaped;
}

// src/inspector.ts
function need(id) {
  const el = document.getElementById(id);
  if (!el) throw new Error(`the inspector page has no #${id}`);
  return el;
}
var preview = need("preview");
var highlight = need("highlight");
var treeBox = need("tree");
var detail = need("detail");
var status = need("status");
var selected = null;
var elements = [];
var cell = { w: 8, h: 16 };
var pad = { x: 8, y: 8 };
var drag = null;
var capture = (() => {
  const held = document.getElementById("capture");
  return held ? JSON.parse(held.textContent ?? "") : null;
})();
async function rpc(body) {
  if (capture) return answerFromCapture(capture, body);
  const res = await fetch("rpc", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body)
  });
  const answer = await res.json();
  if (answer.error) throw new Error(answer.error);
  return answer;
}
function answerFromCapture(held, req) {
  switch (req.op) {
    case "frame":
      return { hit: "", frame: held.frame };
    case "elements":
      return { hit: "", elements: held.elements };
    case "tree":
      return { hit: "", tree: held.tree };
    case "query": {
      const element = held.elements.find((e) => e.id === req.id);
      if (!element) throw new Error(`no element has id ${JSON.stringify(req.id)} in this capture`);
      return { hit: "", element };
    }
    case "at": {
      const index = held.hits[req.y ?? -1]?.[req.x ?? -1] ?? -1;
      const hit = index < 0 ? "" : held.elements[index].id;
      return { hit, found: hit !== "" };
    }
    default:
      throw new Error(`this is a capture, not a running program: ${req.op} needs one`);
  }
}
function setStatus(text, state) {
  status.textContent = text;
  status.dataset.state = state;
}
function saidWhy(err) {
  return err instanceof Error ? err.message : String(err);
}
function measureCell() {
  const probe = document.createElement("span");
  probe.textContent = "X".repeat(100);
  probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;font:inherit";
  preview.appendChild(probe);
  const width = probe.getBoundingClientRect().width / 100;
  probe.remove();
  const style = getComputedStyle(preview);
  const height = parseFloat(style.lineHeight);
  if (width > 0 && height > 0) cell = { w: width, h: height };
  pad = { x: parseFloat(style.paddingLeft), y: parseFloat(style.paddingTop) };
}
var frameRows = 0;
function drawFrame(frame) {
  preview.innerHTML = toHTML(frame.ansi ?? frame.text ?? "");
  need("frame-meta").textContent = `#${frame.seq} \xB7 ${frame.width}x${frame.height}`;
  frameRows = frame.height;
  measureCell();
  placeHighlight();
}
function placeHighlight() {
  const el = elements.find((e) => e.id === selected);
  if (!el) {
    highlight.hidden = true;
    return;
  }
  highlight.hidden = false;
  highlight.style.left = `${pad.x + el.rect.x * cell.w}px`;
  highlight.style.top = `${pad.y + el.rect.y * cell.h}px`;
  highlight.style.width = `${Math.max(1, el.rect.w) * cell.w}px`;
  highlight.style.height = `${Math.max(1, el.rect.h) * cell.h}px`;
  highlight.dataset.label = `${el.element} #${el.id}  ${el.rect.w}x${el.rect.h}@${el.rect.x},${el.rect.y}`;
  highlight.dataset.place = el.rect.y + el.rect.h >= frameRows - 1 ? "inside" : "below";
}
function cellAt(event) {
  const box = preview.getBoundingClientRect();
  return {
    x: Math.floor((event.clientX - box.left - pad.x) / cell.w),
    y: Math.floor((event.clientY - box.top - pad.y) / cell.h)
  };
}
function nodeLabel(node) {
  const bits = [`<${node.element}>`];
  if (node.id) bits.push(`#${node.id}`);
  const geom = `${node.rect.w}x${node.rect.h}@${node.rect.x},${node.rect.y}`;
  return { head: bits.join(" "), geom, text: node.text ?? "" };
}
function renderTree(root) {
  treeBox.replaceChildren(buildList([root]));
}
function buildList(nodes) {
  const ul = document.createElement("ul");
  for (const node of nodes) {
    const li = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.className = "node";
    button.dataset.focus = String(node.focus);
    if (node.id) button.dataset.id = node.id;
    if (node.id === selected) button.setAttribute("aria-current", "true");
    const { head, geom, text } = nodeLabel(node);
    button.append(head + " ");
    const idSpan = document.createElement("span");
    idSpan.className = "id";
    idSpan.textContent = geom;
    button.append(idSpan);
    if (text) {
      const textSpan = document.createElement("span");
      textSpan.className = "text";
      textSpan.textContent = ` ${text}`;
      button.append(textSpan);
    }
    button.addEventListener("click", () => {
      if (node.id) void select(node.id);
    });
    li.append(button);
    if (node.children?.length) li.append(buildList(node.children));
    ul.append(li);
  }
  return ul;
}
var FIELDS = [
  ["element", (e) => e.element],
  ["action", (e) => e.action || "-"],
  ["rect", (e) => `x${e.rect.x} y${e.rect.y} ${e.rect.w}x${e.rect.h}`],
  ["content", (e) => `${e.content.w}x${e.content.h}`],
  ["clip", (e) => `x${e.clip.x} y${e.clip.y} ${e.clip.w}x${e.clip.h}`],
  ["scroll", (e) => `${e.scroll.y}/${e.scroll.maxY} (x ${e.scroll.x}/${e.scroll.maxX})`],
  ["focus", (e) => String(e.focus)]
];
function showDetail(el) {
  need("selected-id").textContent = el.id;
  detail.replaceChildren();
  for (const [label, read] of FIELDS) {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = read(el);
    detail.append(dt, dd);
  }
  need("element-text").textContent = el.text;
}
async function select(id) {
  selected = id;
  try {
    const { element } = await rpc({ op: "query", id });
    if (element) showDetail(element);
    for (const button of treeBox.querySelectorAll(".node")) {
      button.toggleAttribute("aria-current", button.dataset.id === id);
    }
    placeHighlight();
  } catch (err) {
    setStatus(saidWhy(err), "lost");
  }
}
async function refresh() {
  const [got, walked] = await Promise.all([rpc({ op: "elements" }), rpc({ op: "tree" })]);
  elements = got.elements ?? [];
  if (walked.tree) renderTree(walked.tree);
  if (selected && elements.some((e) => e.id === selected)) await select(selected);
  else placeHighlight();
}
preview.addEventListener("pointerdown", async (event) => {
  const at = cellAt(event);
  try {
    const hit = await rpc({ op: "at", x: at.x, y: at.y });
    if (!hit.found) return;
    const el = elements.find((e) => e.id === hit.hit);
    if (hit.hit === selected && el && !capture) {
      drag = { id: hit.hit, from: at, rect: { ...el.rect }, resize: event.shiftKey };
      preview.setPointerCapture(event.pointerId);
      return;
    }
    await select(hit.hit);
  } catch (err) {
    setStatus(saidWhy(err), "lost");
  }
});
preview.addEventListener("pointermove", async (event) => {
  if (!drag) return;
  const at = cellAt(event);
  const dx = at.x - drag.from.x;
  const dy = at.y - drag.from.y;
  if (dx === 0 && dy === 0) return;
  drag.from = at;
  const attrs = drag.resize ? { width: String(Math.max(1, drag.rect.w + dx)), height: String(Math.max(1, drag.rect.h + dy)) } : { "margin-left": String(Math.max(0, drag.rect.x + dx)), "margin-top": String(Math.max(0, drag.rect.y + dy)) };
  if (drag.resize) {
    drag.rect.w = Math.max(1, drag.rect.w + dx);
    drag.rect.h = Math.max(1, drag.rect.h + dy);
  } else {
    drag.rect.x = Math.max(0, drag.rect.x + dx);
    drag.rect.y = Math.max(0, drag.rect.y + dy);
  }
  try {
    await rpc({ op: "restyle", id: drag.id, attrs });
  } catch (err) {
    setStatus(saidWhy(err), "lost");
    drag = null;
  }
});
preview.addEventListener("pointerup", (event) => {
  if (drag) preview.releasePointerCapture(event.pointerId);
  drag = null;
});
need("restyle").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!selected) return;
  const form = new FormData(event.target);
  const attr = String(form.get("attr") ?? "").trim();
  if (!attr) return;
  try {
    await rpc({ op: "restyle", id: selected, attrs: { [attr]: String(form.get("value") ?? "") } });
    setStatus(`applied ${attr} to ${selected}`, "live");
  } catch (err) {
    setStatus(saidWhy(err), "lost");
  }
});
need("reset").addEventListener("click", async () => {
  try {
    await rpc({ op: "reset" });
    setStatus("overrides dropped", "live");
  } catch (err) {
    setStatus(saidWhy(err), "lost");
  }
});
need("keys").addEventListener("submit", async (event) => {
  event.preventDefault();
  const key = need("key").value.trim();
  if (!key) return;
  try {
    await rpc({ op: "key", key });
    setStatus(`sent ${key}`, "live");
  } catch (err) {
    setStatus(saidWhy(err), "lost");
  }
});
if (!capture) {
  const stream = new EventSource("events");
  stream.addEventListener("frame", async (event) => {
    const frame = JSON.parse(event.data);
    drawFrame(frame);
    setStatus(`live \xB7 frame ${frame.seq}`, "live");
    if (need("follow").checked) await refresh();
  });
  stream.addEventListener("error", () => setStatus("connection lost", "lost"));
}
void (async () => {
  if (capture) document.body.dataset.capture = "true";
  try {
    const { frame } = await rpc({ op: "frame", ansi: true });
    if (!frame) throw new Error("the answer to op=frame carried no frame");
    drawFrame(frame);
    await refresh();
    setStatus(capture ? `capture \xB7 frame ${frame.seq} \xB7 ${frame.at}` : `live \xB7 frame ${frame.seq}`, "live");
  } catch (err) {
    setStatus(saidWhy(err), "lost");
  }
})();
