/* ==================================================================
   NoteHarness frontend
   ================================================================== */
"use strict";

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

let S = { notes: [], archive: [], points: [], chats: [], config: { profiles: [], presets: [], theme: "light", activeAlias: "" } };
let route = "notes";
let curNoteId = null;          // notes page current note
let curArchId = null;          // archive page current doc
let curTopicTag = null;        // topics page current tag
let curTopicDocId = null;
let curChatId = null;
let archEditing = false;
let topicEditing = false;   // Topic 详情页文档编辑中

const notesSel = new Set();    // notes page selection
const archSel = new Set();     // archive page selection
const topicSel = new Set();    // topics page selection
let archExpanded = new Set();  // expanded "YYYY-MM-DD" keys
let archTouched = false;       // user manually expanded/collapsed
let archSearch = "";

/* ---------------- api ---------------- */
async function api(path, method, body) {
  const opt = { method: method || "GET" };
  if (body !== undefined) { opt.headers = { "Content-Type": "application/json" }; opt.body = JSON.stringify(body); }
  const r = await fetch(path, opt);
  const ct = r.headers.get("content-type") || "";
  const data = ct.includes("json") ? await r.json() : await r.text();
  if (!r.ok) throw new Error((data && data.error) || ("HTTP " + r.status));
  return data;
}
async function refresh() {
  S = await api("/api/state");
  S.notes = S.notes || [];
  S.archive = S.archive || [];
  S.chats = S.chats || [];
  S.config.profiles = S.config.profiles || [];
  S.config.presets = S.config.presets || [];
  S.chats.forEach(c => { c.messages = c.messages || []; c.noteNames = c.noteNames || []; c.noteIds = c.noteIds || []; });
  S.archive.forEach(a => { a.tags = a.tags || []; });
  applyTheme();
}

/* ---------------- 全局错误兜底：任何未捕获的失败都要让用户看见，不能默默无反应 ---------------- */
window.addEventListener("unhandledrejection", (e) => {
  const r = e.reason;
  const msg = (r && (r.message || (r.error && r.error.message))) || String(r || "未知错误");
  console.error("[NoteHarness]", r);
  toast("操作失败：" + msg);
});
window.addEventListener("error", (e) => {
  console.error("[NoteHarness]", e.error || e.message);
  toast("页面异常：" + (e.message || "未知错误"));
});

/* 行点击辅助：Ctrl/Cmd+点击 切换勾选，Shift+点击 范围勾选；
   返回 true 表示已被当作「多选操作」处理，调用方不要再执行打开文档。 */
let lastSelId = null;
function rowClick(e, id, orderedIds, selSet) {
  if (e.ctrlKey || e.metaKey) {
    e.preventDefault(); e.stopPropagation();
    selSet.has(id) ? selSet.delete(id) : selSet.add(id);
    lastSelId = id;
    render();
    return true;
  }
  if (e.shiftKey && lastSelId && lastSelId !== id) {
    e.preventDefault(); e.stopPropagation();
    const a = orderedIds.indexOf(lastSelId), b = orderedIds.indexOf(id);
    if (a >= 0 && b >= 0) {
      for (let i = Math.min(a, b); i <= Math.max(a, b); i++) selSet.add(orderedIds[i]);
    }
    lastSelId = id;
    render();
    return true;
  }
  lastSelId = id;
  return false;
}

/* ---------------- toast / modal / ctxmenu ---------------- */
let toastTimer = null;
function toast(msg) {
  const t = $("#toast"); t.textContent = msg; t.classList.add("show");
  clearTimeout(toastTimer); toastTimer = setTimeout(() => t.classList.remove("show"), 2200);
}
function showModal(html) {
  $("#modal-box").innerHTML = html;
  $("#modal-mask").classList.remove("hidden");
}
function closeModal() { $("#modal-mask").classList.add("hidden"); }
$("#modal-mask").addEventListener("click", (e) => { if (e.target.id === "modal-mask") closeModal(); });
function confirmDlg(title, msg) {
  return new Promise((resolve) => {
    showModal(`<h3>${esc(title)}</h3><div class="modal-msg">${esc(msg)}</div>
      <div class="row-end"><button class="btn" id="m-no">取消</button>
      <button class="btn primary" id="m-yes">确定</button></div>`);
    $("#m-no").onclick = () => { closeModal(); resolve(false); };
    $("#m-yes").onclick = () => { closeModal(); resolve(true); };
  });
}
function alertDlg(title, msg) {
  return new Promise((resolve) => {
    showModal(`<h3>${esc(title)}</h3><div class="modal-msg">${esc(msg)}</div>
      <div class="row-end"><button class="btn primary" id="m-ok">OK</button></div>`);
    $("#m-ok").onclick = () => { closeModal(); resolve(true); };
  });
}
let ctxCleanup = null;
function showCtxMenu(x, y, html) {
  hideCtxMenu();
  const m = $("#ctx-menu"); m.innerHTML = html; m.classList.remove("hidden");
  m.style.left = Math.min(x, window.innerWidth - 180) + "px";
  m.style.top = Math.min(y, window.innerHeight - 120) + "px";
  ctxCleanup = (e) => { if (!m.contains(e.target)) hideCtxMenu(); };
  setTimeout(() => document.addEventListener("click", ctxCleanup), 0);
}
function hideCtxMenu() {
  $("#ctx-menu").classList.add("hidden");
  if (ctxCleanup) { document.removeEventListener("click", ctxCleanup); ctxCleanup = null; }
}
// 右键菜单：重命名 + 删除（删除就地二次确认）
function ctxDelete(e, label, onYes) {
  e.preventDefault();
  showCtxMenu(e.clientX, e.clientY,
    `<div class="confirm-inline"><div class="ci-msg">删除「${esc(trunc(label, 14))}」？</div>
       <div class="ci-btns"><button id="ctx-no">取消</button>
       <button id="ctx-yes" class="danger">删除</button></div></div>`);
  $("#ctx-yes").onclick = () => { hideCtxMenu(); onYes(); };
  $("#ctx-no").onclick = hideCtxMenu;
}
// 带「重命名」的右键菜单；onRename 负责把列表项切成输入框
function ctxItemMenu(e, label, onRename, onDelete) {
  e.preventDefault();
  const x = e.clientX, y = e.clientY;
  showCtxMenu(x, y, `<button id="ctx-rename">✏️ 重命名</button><button id="ctx-del">🗑 删除</button>`);
  $("#ctx-rename").onclick = () => { hideCtxMenu(); onRename(); };
  $("#ctx-del").onclick = () => ctxDelete({ preventDefault() {}, clientX: x, clientY: y }, label, onDelete);
}

/* ---------------- helpers ---------------- */
function esc(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function trunc(s, n) { s = String(s ?? ""); return s.length > n ? s.slice(0, n) + "…" : s; }
function fmtDate(ms) { const d = new Date(ms); return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`; }
function md(text) { return marked.parse(text || "", { breaks: true }); }
function activeProfile() { const c = S.config; return (c.profiles || []).find(p => p.alias === c.activeAlias) || null; }
function profileLabel(p) { return p ? `${p.alias}(${p.model})` : "未配置模型"; }

/* ---------------- theme ---------------- */
function applyTheme() {
  const t = S.config.theme === "dark" ? "dark" : "light";
  document.documentElement.dataset.theme = t;
  $("#btn-theme").textContent = t === "dark" ? "☀" : "🌙";
}
$("#btn-theme").addEventListener("click", async () => {
  const t = S.config.theme === "dark" ? "light" : "dark";
  S.config = await api("/api/config", "PUT", { theme: t });
  applyTheme();
});

/* ---------------- router ---------------- */
function go(r) {
  route = r;
  ["notes", "archive", "topics", "settings"].forEach(k => $("#page-" + k).classList.toggle("hidden", k !== r));
  $$(".nav-btn").forEach(b => b.classList.toggle("active", b.dataset.route === r));
  render();
}
$$(".nav-btn").forEach(b => b.addEventListener("click", () => go(b.dataset.route)));
$("#btn-settings").addEventListener("click", () => go("settings"));
function render() {
  if (route === "notes") renderNotes();
  else if (route === "archive") renderArchive();
  else if (route === "topics") renderTopics();
  else if (route === "settings") renderSettings();
}

/* ==================================================================
   NOTES PAGE
   ================================================================== */
function sortedNotes() { return [...S.notes].sort((a, b) => a.createdAt - b.createdAt); }

function renderNotes() {
  const list = $("#notes-list"); list.innerHTML = "";
  const all = sortedNotes();
  const orderedIds = all.map(n => n.id);
  for (const n of all) {
    const li = document.createElement("li");
    li.dataset.id = n.id;
    li.classList.toggle("active", n.id === curNoteId);
    li.innerHTML = `<input type="checkbox" ${notesSel.has(n.id) ? "checked" : ""}>
      <span class="name">${esc(n.name)}</span>
      ${n.kind === "summary" ? '<span class="badge-kind">AI</span>' : ""}`;
    li.querySelector("input").onclick = (e) => {
      e.stopPropagation();
      notesSel.has(n.id) ? notesSel.delete(n.id) : notesSel.add(n.id);
      renderNotes();
    };
    li.onclick = (e) => {
      if (rowClick(e, n.id, orderedIds, notesSel)) return;
      curNoteId = n.id; renderNotes();
    };
    li.ondblclick = () => startRename(li, n.name, renameNote(n));
    li.oncontextmenu = (e) => ctxItemMenu(e, n.name,
      () => startRename(li, n.name, renameNote(n)),
      async () => {
        await api("/api/notes/" + n.id, "DELETE");
        if (curNoteId === n.id) curNoteId = null;
        notesSel.delete(n.id);
        await refresh(); renderNotes(); toast("已删除");
      });
    list.appendChild(li);
  }
  const selN = notesSel.size;
  $("#btn-compare").disabled = selN !== 2;
  $("#btn-archive-sel").disabled = selN === 0;
  $("#btn-export-sel").disabled = selN === 0;
  $("#btn-notes-chat").disabled = selN === 0;
  $("#btn-notes-chat").textContent = selN > 1 ? `🤖 AI Chat (${selN})` : "🤖 AI Chat";
  $("#notes-sel-hint").textContent = selN ? `已选 ${selN}` : "";
  const allBox = $("#notes-sel-all");
  const totalN = all.length;
  allBox.checked = totalN > 0 && selN === totalN;
  allBox.indeterminate = selN > 0 && selN < totalN;
  renderNoteMain();
}

$("#notes-sel-all").addEventListener("change", (e) => {
  if (e.target.checked) sortedNotes().forEach(n => notesSel.add(n.id));
  else notesSel.clear();
  renderNotes();
});

// 把列表项里的名字就地切成输入框；commit(v) 保存后由调用方决定如何刷新
function startRename(el, name, commit) {
  const nameEl = el.querySelector(".name");
  if (!nameEl) return;
  const input = document.createElement("input");
  input.className = "rename-input"; input.value = name;
  nameEl.replaceWith(input);
  input.focus(); input.select();
  let done = false;
  const finish = async (save) => {
    if (done) return; done = true;
    const v = input.value.trim();
    if (save && v && v !== name) {
      try { await commit(v); await refresh(); }
      catch (err) { toast("重命名失败：" + ((err && err.message) || err)); }
    }
    render();
  };
  input.onblur = () => finish(true);
  input.onkeydown = (e) => { if (e.key === "Enter") input.blur(); if (e.key === "Escape") finish(false); };
  input.onclick = (e) => e.stopPropagation();
}
function renameNote(n) {
  return (v) => api("/api/notes/" + n.id, "PUT", { name: v, content: n.content });
}
function renameArch(n) {
  return (v) => api("/api/archive/" + n.id, "PUT", { name: v, content: n.content });
}

function curNote() { return S.notes.find(n => n.id === curNoteId) || null; }

function renderNoteMain() {
  const n = curNote();
  $("#note-empty").classList.toggle("hidden", !!n || compareMode);
  $("#note-main").classList.toggle("hidden", !n || compareMode);
  if (!n || compareMode) return;
  $("#note-title").value = n.name;
  if ($("#note-area").dataset.dirty !== n.id) {
    $("#note-area").value = n.content;
    delete $("#note-area").dataset.dirty;
  }
  $("#note-status").textContent = `更新于 ${new Date(n.updatedAt).toLocaleString("zh-CN")}`;
}

$("#btn-new-draft").addEventListener("click", async () => {
  try {
    const n = await api("/api/notes", "POST", { kind: "draft" });
    if (!n || !n.id) throw new Error("服务端没有返回新草稿，请重启应用后重试");
    await refresh();
    curNoteId = n.id;
    renderNotes();
    // enter rename mode for the new draft
    const li = $(`#notes-list li[data-id="${n.id}"]`);
    if (li) startRename(li, n.name, renameNote(n));
  } catch (e) {
    await alertDlg("新建草稿失败", (e && e.message) || String(e));
  }
});

$("#note-title").addEventListener("blur", async () => {
  const n = curNote(); if (!n) return;
  const v = $("#note-title").value.trim();
  if (v && v !== n.name) {
    await api("/api/notes/" + n.id, "PUT", { name: v, content: $("#note-area").value });
    await refresh(); renderNotes();
  }
});

$("#note-area").addEventListener("input", () => { $("#note-area").dataset.dirty = curNoteId; });

async function saveCurrentNote() {
  const n = curNote(); if (!n) return;
  await api("/api/notes/" + n.id, "PUT", { name: $("#note-title").value.trim() || n.name, content: $("#note-area").value });
  delete $("#note-area").dataset.dirty;
  await refresh(); renderNotes();
  $("#note-status").textContent = "已保存 " + new Date().toLocaleTimeString("zh-CN");
  toast("已保存");
}
$("#btn-save-note").addEventListener("click", saveCurrentNote);
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
    e.preventDefault();
    if (route === "notes" && curNote()) saveCurrentNote();
  }
});

$("#seg-edit").addEventListener("click", () => setSeg(false));
$("#seg-preview").addEventListener("click", () => setSeg(true));
function setSeg(preview) {
  $("#seg-edit").classList.toggle("active", !preview);
  $("#seg-preview").classList.toggle("active", preview);
  $("#note-area").classList.toggle("hidden", preview);
  $("#note-preview").classList.toggle("hidden", !preview);
  if (preview) $("#note-preview").innerHTML = md($("#note-area").value);
}

$("#btn-ai-summary").addEventListener("click", async () => {
  const n = curNote(); if (!n) return;
  await saveCurrentNote();
  const btn = $("#btn-ai-summary"); btn.disabled = true; btn.textContent = "总结中…";
  try {
    const created = await api(`/api/notes/${n.id}/summarize`, "POST");
    await refresh(); curNoteId = created.id; renderNotes();
    toast("AI 总结已生成");
  } catch (e) { await alertDlg("AI 总结失败", e.message); }
  btn.disabled = false; btn.textContent = "✨ AI 总结";
});

/* ---- archive flow ---- */
async function archiveFlow(ids) {
  if (!ids.length) return;
  const now = new Date();
  const p2 = (v) => String(v).padStart(2, "0");
  const defDate = `${now.getFullYear()}-${p2(now.getMonth() + 1)}-${p2(now.getDate())}`;
  const defName = `Summary ${defDate} ${p2(now.getHours())}:${p2(now.getMinutes())}:${p2(now.getSeconds())}`;
  const tagOpts = allTags().map(t => `<option value="${esc(t)}">`).join("");
  showModal(`<h3>存档 ${ids.length} 篇笔记</h3>
    <label>存档点名称</label><input id="af-name" placeholder="${esc(defName)}">
    <label>保存日期</label><input id="af-date" type="date" value="${defDate}">
    <label>Tag（可选，可输入新标签或选择已有）</label>
    <input id="af-tag" list="af-tags" placeholder="输入关键字模糊搜索已有 Tag"><datalist id="af-tags">${tagOpts}</datalist>
    <div class="row-end"><button class="btn" id="af-no">取消</button>
    <button class="btn primary" id="af-yes">存档</button></div>`);
  $("#af-no").onclick = closeModal;
  $("#af-yes").onclick = async () => {
    const body = { noteIds: ids, name: $("#af-name").value.trim(), date: $("#af-date").value, tag: $("#af-tag").value.trim() };
    closeModal();
    try {
      await api("/api/archive", "POST", body);
      notesSel.clear();
      await refresh(); renderNotes();
      toast("已存档");
    } catch (e) { await alertDlg("存档失败", e.message); }
  };
}
$("#btn-archive-one").addEventListener("click", async () => { await saveCurrentNote(); archiveFlow([curNoteId]); });
$("#btn-archive-sel").addEventListener("click", () => archiveFlow([...notesSel]));

function allTags() {
  const set = new Set();
  S.archive.forEach(a => (a.tags || []).forEach(t => set.add(t)));
  return [...set];
}

/* ---- compare / diff ---- */
let compareMode = false;
let comparePair = null; // {a, b, scope: "notes"|"arch"}

$("#btn-compare").addEventListener("click", () => {
  const [x, y] = [...notesSel].map(id => S.notes.find(n => n.id === id));
  if (!x || !y) return;
  comparePair = { a: x, b: y, scope: "notes" };
  compareMode = true;
  $("#note-empty").classList.add("hidden");
  $("#note-main").classList.add("hidden");
  $("#note-compare").classList.remove("hidden");
  renderCompare($("#compare-body"), comparePair);
});
$("#btn-close-compare").addEventListener("click", () => {
  compareMode = false; comparePair = null;
  $("#note-compare").classList.add("hidden");
  renderNotes();
});

// line-based LCS diff -> list of blocks {type: "same"|"diff", aLines, bLines}
function diffLines(aText, bText) {
  const a = aText.split("\n"), b = bText.split("\n");
  const n = a.length, m = b.length;
  if (n * m > 4e6) return [{ type: "diff", aLines: a, bLines: b }]; // too big, single block
  const dp = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const blocks = [];
  let i = 0, j = 0;
  const push = (type, al, bl) => {
    if (!al.length && !bl.length) return;
    const last = blocks[blocks.length - 1];
    if (last && last.type === type) { last.aLines.push(...al); last.bLines.push(...bl); }
    else blocks.push({ type, aLines: [...al], bLines: [...bl] });
  };
  while (i < n && j < m) {
    if (a[i] === b[j]) { push("same", [a[i]], [b[j]]); i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { push("diff", [a[i]], []); i++; }
    else { push("diff", [], [b[j]]); j++; }
  }
  while (i < n) push("diff", [a[i++]], []);
  while (j < m) push("diff", [], [b[j++]]);
  return blocks;
}

function renderCompare(container, pair) {
  const blocks = diffLines(pair.a.content, pair.b.content);
  container.innerHTML = `<div class="diff-row diff-block-head">
    <div class="diff-cell">${esc(pair.a.name)}</div><div class="diff-mid"></div><div class="diff-cell">${esc(pair.b.name)}</div></div>`;
  const maxLines = 4000;
  let count = 0;
  for (const blk of blocks) {
    if (count > maxLines) {
      const r = document.createElement("div");
      r.className = "diff-row diff-block-head";
      r.innerHTML = `<div class="diff-cell" style="grid-column:1/4">…内容过长，省略后续…</div>`;
      container.appendChild(r); break;
    }
    if (blk.type === "same") {
      blk.aLines.forEach(line => {
        count++;
        const row = document.createElement("div");
        row.className = "diff-row diff-same";
        row.innerHTML = `<div class="diff-cell">${esc(line) || " "}</div><div class="diff-mid"></div><div class="diff-cell">${esc(line) || " "}</div>`;
        container.appendChild(row);
      });
    } else {
      const len = Math.max(blk.aLines.length, blk.bLines.length);
      count += len;
      for (let k = 0; k < len; k++) {
        const row = document.createElement("div");
        row.className = "diff-row";
        const al = blk.aLines[k], bl = blk.bLines[k];
        const mid = k === 0
          ? `<div class="diff-mid"><button data-dir="l2r" title="用左侧替换右侧此段">→</button><button data-dir="r2l" title="用右侧替换左侧此段">←</button></div>`
          : `<div class="diff-mid"></div>`;
        row.innerHTML = `<div class="diff-cell ${al !== undefined ? "diff-changed-l" : ""}">${al !== undefined ? esc(al) || " " : ""}</div>${mid}<div class="diff-cell ${bl !== undefined ? "diff-changed-r" : ""}">${bl !== undefined ? esc(bl) || " " : ""}</div>`;
        if (k === 0) {
          row.querySelector('[data-dir="l2r"]').onclick = () => applyDiffBlock(pair, blk, "l2r", container);
          row.querySelector('[data-dir="r2l"]').onclick = () => applyDiffBlock(pair, blk, "r2l", container);
        }
        container.appendChild(row);
      }
    }
  }
}

async function applyDiffBlock(pair, blk, dir, container) {
  const src = dir === "l2r" ? blk.aLines : blk.bLines;
  const dst = dir === "l2r" ? blk.bLines : blk.aLines;
  if (confirm(`将此段\n【${trunc(dst.join("\\n"), 60) || "(空)"}】\n替换为\n【${trunc(src.join("\\n"), 60) || "(空)"}】？`)) {
    const target = dir === "l2r" ? pair.b : pair.a;
    target.content = replaceBlock(target.content, dir === "l2r" ? blk.bLines : blk.aLines, src);
    const url = pair.scope === "notes" ? "/api/notes/" + target.id : "/api/archive/" + target.id;
    await api(url, "PUT", { name: target.name, content: target.content });
    await refresh();
    // re-fetch fresh copies into pair
    if (pair.scope === "notes") {
      pair.a = S.notes.find(n => n.id === pair.a.id) || pair.a;
      pair.b = S.notes.find(n => n.id === pair.b.id) || pair.b;
    } else {
      pair.a = S.archive.find(n => n.id === pair.a.id) || pair.a;
      pair.b = S.archive.find(n => n.id === pair.b.id) || pair.b;
    }
    renderCompare(container, pair);
    toast("已替换并保存");
  }
}
function replaceBlock(content, oldLines, newLines) {
  if (!oldLines.length) return content; // insertion not mapped; keep simple
  const oldStr = oldLines.join("\n");
  const idx = content.indexOf(oldStr);
  if (idx < 0) return content;
  return content.slice(0, idx) + newLines.join("\n") + content.slice(idx + oldStr.length);
}

$("#btn-note-agent").addEventListener("click", async () => {
  const n = curNote(); if (!n) return;
  await saveCurrentNote();
  openChatWithRefs([{ type: "note", id: n.id, name: n.name }]);
});

/* ==================================================================
   ARCHIVE PAGE
   ================================================================== */
function archGrouped() {
  const g = {}; // date -> notes
  for (const n of S.archive) {
    if (archSearch && !n.name.toLowerCase().includes(archSearch.toLowerCase())) continue;
    (g[n.date] = g[n.date] || []).push(n);
  }
  for (const d of Object.keys(g)) g[d].sort((a, b) => a.name.localeCompare(b.name, "zh-Hans-CN-u-nu-latn"));
  return g;
}

function renderArchive() {
  const tree = $("#archive-tree"); tree.innerHTML = "";
  const g = archGrouped();
  const dates = Object.keys(g).sort().reverse();
  if (!dates.length) {
    tree.innerHTML = `<div class="hint" style="padding:14px">暂无存档${archSearch ? "（无匹配结果）" : ""}</div>`;
  }
  // auto-expand latest date on first load
  if (!archExpanded.size && dates.length) {
    archExpanded.add(dates[0]);
    const first = g[dates[0]][0];
    if (first && !curArchId) curArchId = first.id;
  }
  // group by year -> month -> day
  const years = {};
  for (const d of dates) {
    const [y, m] = d.split("-");
    ((years[y] = years[y] || {})[m] = years[y][m] || []).push(d);
  }
  for (const y of Object.keys(years).sort().reverse()) {
    tree.appendChild(treeNode(y + "年", null, true));
    const yc = document.createElement("div"); yc.className = "tree-children";
    for (const m of Object.keys(years[y]).sort().reverse()) {
      yc.appendChild(treeNode(parseInt(m) + "月", null, true));
      const mc = document.createElement("div"); mc.className = "tree-children";
      for (const d of years[y][m]) {
        const cnt = g[d].length;
        const node = treeNode(d, cnt, archExpanded.has(d));
        node.onclick = () => {
          archTouched = true;
          archExpanded.has(d) ? archExpanded.delete(d) : archExpanded.add(d);
          renderArchive();
        };
        mc.appendChild(node);
        if (archExpanded.has(d)) {
          const docs = document.createElement("div"); docs.className = "day-docs";
          const dayIds = g[d].map(x => x.id);
          for (const n of g[d]) {
            const di = document.createElement("div");
            di.className = "doc-item" + (n.id === curArchId ? " active" : "");
            di.innerHTML = `<input type="checkbox" ${archSel.has(n.id) ? "checked" : ""}><span class="name">${esc(n.name)}</span>`;
            di.querySelector("input").onclick = (e) => {
              e.stopPropagation();
              archSel.has(n.id) ? archSel.delete(n.id) : archSel.add(n.id);
              renderArchive();
            };
            di.onclick = (e) => {
              if (rowClick(e, n.id, dayIds, archSel)) return;
              curArchId = n.id; archEditing = false; renderArchive();
            };
            di.oncontextmenu = (e) => ctxItemMenu(e, n.name,
              () => startRename(di, n.name, renameArch(n)),
              async () => {
                await api("/api/archive/" + n.id, "DELETE");
                if (curArchId === n.id) curArchId = null;
                archSel.delete(n.id);
                await refresh(); renderArchive(); toast("已删除");
              });
            docs.appendChild(di);
          }
          mc.appendChild(docs);
        }
      }
      yc.appendChild(mc);
    }
    tree.appendChild(yc);
  }
  $("#btn-arch-tag").disabled = archSel.size === 0;
  $("#btn-arch-compare").disabled = archSel.size !== 2;
  $("#btn-arch-export").disabled = archSel.size === 0;
  // 右上方 AI Chat：有勾选就用勾选的多篇，否则用当前打开的这一篇
  $("#btn-arch-chat").disabled = archSel.size === 0 && !curArch();
  $("#btn-arch-chat").textContent = archSel.size > 1 ? `🤖 AI Chat (${archSel.size})` : "🤖 AI Chat";
  renderArchMain();
}

function treeNode(label, count, open) {
  const d = document.createElement("div");
  d.className = "tree-node";
  d.innerHTML = `<span class="arrow">${open === undefined ? "" : (open ? "▼" : "▶")}</span><span>${esc(label)}</span>${count != null ? `<span class="count-dot">${count}</span>` : ""}`;
  return d;
}

$("#archive-search").addEventListener("input", (e) => {
  archSearch = e.target.value.trim();
  if (archSearch) archExpanded = new Set(Object.keys(archGrouped()));
  renderArchive();
});

function curArch() { return S.archive.find(n => n.id === curArchId) || null; }

function renderArchMain() {
  const n = curArch();
  $("#arch-empty").classList.toggle("hidden", !!n || archCompareMode);
  $("#arch-main").classList.toggle("hidden", !n || archCompareMode);
  if (!n || archCompareMode) return;
  $("#arch-title").textContent = n.name + "　(" + n.date + ")";
  const tr = $("#arch-tags");
  tr.innerHTML = (n.tags || []).map(t => `<span class="tag-chip">${esc(t)}<span class="x" data-tag="${esc(t)}" title="移除">×</span></span>`).join("");
  tr.querySelectorAll(".x").forEach(x => x.onclick = async () => {
    await api("/api/archive/untag", "POST", { ids: [n.id], tag: x.dataset.tag });
    await refresh(); renderArchive();
  });
  $("#arch-view").innerHTML = md(n.content);
  $("#arch-view").classList.toggle("hidden", archEditing);
  $("#arch-area").classList.toggle("hidden", !archEditing);
  $("#btn-arch-edit").classList.toggle("hidden", archEditing);
  $("#btn-arch-save").classList.toggle("hidden", !archEditing);
  if (archEditing && $("#arch-area").dataset.for !== n.id) {
    $("#arch-area").value = n.content;
    $("#arch-area").dataset.for = n.id;
  }
  if (!archEditing) delete $("#arch-area").dataset.for;
}

$("#btn-arch-edit").addEventListener("click", () => { archEditing = true; renderArchMain(); });
$("#btn-arch-save").addEventListener("click", async () => {
  const n = curArch(); if (!n) return;
  await api("/api/archive/" + n.id, "PUT", { name: n.name, content: $("#arch-area").value });
  archEditing = false;
  await refresh(); renderArchive(); toast("已保存");
});
$("#btn-arch-chat").addEventListener("click", () => {
  let refs;
  if (archSel.size) {
    refs = [...archSel].map(id => {
      const n = S.archive.find(a => a.id === id);
      return { type: "arch", id, name: n ? n.name : "" };
    });
  } else {
    const n = curArch(); if (!n) return;
    refs = [{ type: "arch", id: n.id, name: n.name }];
  }
  openChatWithRefs(refs);
});
$("#btn-arch-tag").addEventListener("click", () => {
  const ids = [...archSel];
  const tagOpts = allTags().map(t => `<option value="${esc(t)}">`).join("");
  showModal(`<h3>给 ${ids.length} 篇文档打 Tag</h3>
    <label>Tag（可输入新标签或模糊搜索已有）</label>
    <input id="tg-input" list="tg-tags"><datalist id="tg-tags">${tagOpts}</datalist>
    <div class="row-end"><button class="btn" id="tg-no">取消</button>
    <button class="btn primary" id="tg-yes">确定</button></div>`);
  $("#tg-no").onclick = closeModal;
  $("#tg-yes").onclick = async () => {
    const tag = $("#tg-input").value.trim();
    closeModal();
    if (!tag) return;
    await api("/api/archive/tag", "POST", { ids, tag });
    await refresh(); renderArchive(); toast("已打 Tag");
  };
});

let archCompareMode = false;
let archComparePair = null;
$("#btn-arch-compare").addEventListener("click", () => {
  const [x, y] = [...archSel].map(id => S.archive.find(n => n.id === id));
  if (!x || !y) return;
  archComparePair = { a: x, b: y, scope: "arch" };
  archCompareMode = true;
  $("#arch-empty").classList.add("hidden");
  $("#arch-main").classList.add("hidden");
  $("#arch-compare").classList.remove("hidden");
  renderCompare($("#arch-compare-body"), archComparePair);
});
$("#btn-close-arch-compare").addEventListener("click", () => {
  archCompareMode = false; archComparePair = null;
  $("#arch-compare").classList.add("hidden");
  renderArchive();
});

/* ==================================================================
   TOPICS PAGE
   ================================================================== */
const TAG_COLORS = ["#5b8def", "#8b5cf6", "#ec4899", "#f59e0b", "#10b981", "#06b6d4", "#f97316", "#6366f1", "#14b8a6", "#e11d48"];
function tagColor(tag) {
  let h = 0;
  for (const c of tag) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return TAG_COLORS[h % TAG_COLORS.length];
}
function tagStats() {
  const m = {};
  for (const n of S.archive) {
    for (const t of n.tags || []) {
      m[t] = m[t] || { last: 0, docs: [] };
      if (n.archivedAt > m[t].last) m[t].last = n.archivedAt;
      m[t].docs.push(n);
    }
  }
  for (const t of Object.keys(m)) m[t].docs.sort((a, b) => b.archivedAt - a.archivedAt);
  return m;
}

function renderTopics() {
  const detail = !!curTopicTag;
  $("#topics-grid-wrap").classList.toggle("hidden", detail);
  $("#topic-detail").classList.toggle("hidden", !detail);
  if (!detail) {
    const stats = tagStats();
    const tags = Object.keys(stats).sort((a, b) => stats[b].last - stats[a].last);
    $("#topics-empty").classList.toggle("hidden", tags.length > 0);
    const grid = $("#topics-grid"); grid.innerHTML = "";
    for (const t of tags) {
      const card = document.createElement("div");
      card.className = "topic-card";
      card.style.background = `linear-gradient(135deg, ${tagColor(t)}, ${tagColor(t)}cc)`;
      card.innerHTML = `<div class="tname">${esc(t)}</div><div class="tdate">${esc(fmtDate(stats[t].last))} · ${stats[t].docs.length} 篇</div>`;
      card.onclick = () => { curTopicTag = t; curTopicDocId = stats[t].docs[0] ? stats[t].docs[0].id : null; topicSel.clear(); topicEditing = false; topicCompareMode = false; renderTopics(); };
      grid.appendChild(card);
    }
    return;
  }
  // detail view
  const stats = tagStats();
  const rail = $("#tag-rail"); rail.innerHTML = "";
  const back = document.createElement("button");
  back.className = "rail-btn back"; back.textContent = "▼"; back.title = "返回 Tag 卡片";
  back.onclick = () => { curTopicTag = null; renderTopics(); };
  rail.appendChild(back);
  for (const t of Object.keys(stats).sort()) {
    const b = document.createElement("button");
    b.className = "rail-btn" + (t === curTopicTag ? " cur" : "");
    b.style.background = tagColor(t);
    b.textContent = t.slice(0, 2).toUpperCase();
    b.title = t;
    b.onclick = () => { curTopicTag = t; curTopicDocId = stats[t].docs[0] ? stats[t].docs[0].id : null; topicSel.clear(); topicEditing = false; topicCompareMode = false; renderTopics(); };
    rail.appendChild(b);
  }
  const docs = (stats[curTopicTag] || { docs: [] }).docs;
  const docIds = docs.map(d => d.id);
  const list = $("#topic-list"); list.innerHTML = "";
  for (const n of docs) {
    const li = document.createElement("li");
    li.classList.toggle("active", n.id === curTopicDocId);
    li.innerHTML = `<input type="checkbox" ${topicSel.has(n.id) ? "checked" : ""}><span class="name">${esc(n.name)}</span><span class="hint">${esc(n.date)}</span>`;
    li.querySelector("input").onclick = (e) => {
      e.stopPropagation();
      topicSel.has(n.id) ? topicSel.delete(n.id) : topicSel.add(n.id);
      renderTopics();
    };
    li.onclick = (e) => {
      if (rowClick(e, n.id, docIds, topicSel)) return;
      curTopicDocId = n.id; topicEditing = false; renderTopics();
    };
    li.oncontextmenu = (e) => ctxItemMenu(e, n.name,
      () => startRename(li, n.name, renameArch(n)),
      async () => {
        await api("/api/archive/" + n.id, "DELETE");
        if (curTopicDocId === n.id) curTopicDocId = null;
        topicSel.delete(n.id);
        await refresh(); renderTopics(); toast("已删除");
      });
    list.appendChild(li);
  }
  $("#btn-topic-compare").disabled = topicSel.size !== 2;
  $("#btn-topic-compare").textContent = topicSel.size > 2 ? `对比 (${topicSel.size})` : "对比";
  const doc = docs.find(d => d.id === curTopicDocId);
  $("#btn-topic-chat").disabled = topicSel.size === 0 && !doc;
  $("#btn-topic-chat").textContent = topicSel.size > 1 ? `🤖 AI Chat (${topicSel.size})` : "🤖 AI Chat";
  $("#topic-empty").classList.toggle("hidden", !!doc || topicCompareMode);
  $("#topic-main").classList.toggle("hidden", !doc || topicCompareMode);
  if (!doc || topicCompareMode) return;
  $("#topic-title").textContent = doc.name + "　(" + doc.date + ")";
  $("#topic-tags").innerHTML = (doc.tags || []).map(t => `<span class="tag-chip">${esc(t)}</span>`).join("");
  $("#topic-view").innerHTML = md(doc.content);
  $("#topic-view").classList.toggle("hidden", topicEditing);
  $("#topic-area").classList.toggle("hidden", !topicEditing);
  $("#btn-topic-edit").classList.toggle("hidden", topicEditing);
  $("#btn-topic-save").classList.toggle("hidden", !topicEditing);
  if (topicEditing && $("#topic-area").dataset.for !== doc.id) {
    $("#topic-area").value = doc.content;
    $("#topic-area").dataset.for = doc.id;
  }
  if (!topicEditing) delete $("#topic-area").dataset.for;
}

$("#btn-topic-edit").addEventListener("click", () => { topicEditing = true; renderTopics(); });
$("#btn-topic-save").addEventListener("click", async () => {
  const doc = S.archive.find(a => a.id === curTopicDocId);
  if (!doc) return;
  await api("/api/archive/" + doc.id, "PUT", { name: doc.name, content: $("#topic-area").value });
  topicEditing = false;
  await refresh(); renderTopics(); toast("已保存");
});

let topicCompareMode = false;
let topicComparePair = null;
$("#btn-topic-compare").addEventListener("click", () => {
  const [x, y] = [...topicSel].map(id => S.archive.find(n => n.id === id));
  if (!x || !y) return;
  topicComparePair = { a: x, b: y, scope: "arch" };
  topicCompareMode = true;
  $("#topic-empty").classList.add("hidden");
  $("#topic-main").classList.add("hidden");
  $("#topic-compare").classList.remove("hidden");
  renderCompare($("#topic-compare-body"), topicComparePair);
});
$("#btn-close-topic-compare").addEventListener("click", () => {
  topicCompareMode = false; topicComparePair = null;
  $("#topic-compare").classList.add("hidden");
  renderTopics();
});

$("#btn-topic-chat").addEventListener("click", () => {
  let refs;
  if (topicSel.size) {
    refs = [...topicSel].map(id => {
      const n = S.archive.find(a => a.id === id);
      return { type: "arch", id, name: n ? n.name : "" };
    });
  } else {
    const n = S.archive.find(a => a.id === curTopicDocId); if (!n) return;
    refs = [{ type: "arch", id: n.id, name: n.name }];
  }
  openChatWithRefs(refs);
});

/* ==================================================================
   SETTINGS PAGE
   ================================================================== */
let editingAlias = null;
function renderSettings() {
  const c = S.config;
  const list = $("#profiles-list"); list.innerHTML = "";
  for (const p of (c.profiles || [])) {
    const item = document.createElement("div");
    item.className = "profile-item" + (p.alias === c.activeAlias ? " active" : "");
    item.innerHTML = `<div class="info"><div class="alias">${esc(profileLabel(p))}${p.alias === c.activeAlias ? ' <span class="hint">· 当前使用</span>' : ""}</div>
      <div class="sub">${esc(p.baseUrl)} · ${p.format} · 上下文 ${p.maxContext ? (p.maxContext / 1024) + "K" : "256K"}</div></div>
      <button class="btn small" data-act="use">设为当前</button>
      <button class="btn small" data-act="edit">编辑</button>
      <button class="btn small" data-act="del">删除</button>`;
    item.querySelector('[data-act="use"]').onclick = async () => {
      S.config = await api("/api/config", "PUT", { activeAlias: p.alias });
      renderSettings(); renderChatHeader(); toast("已切换");
    };
    item.querySelector('[data-act="edit"]').onclick = () => {
      editingAlias = p.alias;
      $("#pf-alias").value = p.alias; $("#pf-alias").disabled = true;
      $("#pf-model").value = p.model; $("#pf-base").value = p.baseUrl;
      $("#pf-key").value = p.apiKey; $("#pf-ctx").value = p.maxContext || "";
      $("#pf-format").value = p.format;
      $("#profile-form-title").textContent = "编辑模型配置";
      $("#btn-profile-cancel").classList.remove("hidden");
    };
    item.querySelector('[data-act="del"]').onclick = async () => {
      if (!(await confirmDlg("删除配置", `确定删除「${p.alias}」的配置？`))) return;
      S.config = await api("/api/llm/profiles/" + encodeURIComponent(p.alias), "DELETE");
      renderSettings(); renderChatHeader();
    };
    list.appendChild(item);
  }
  renderPresets();
}

$("#btn-profile-cancel").addEventListener("click", resetProfileForm);
function resetProfileForm() {
  editingAlias = null;
  ["#pf-alias", "#pf-model", "#pf-base", "#pf-key", "#pf-ctx"].forEach(s => $(s).value = "");
  $("#pf-alias").disabled = false;
  $("#pf-format").value = "openai";
  $("#profile-form-title").textContent = "添加模型配置";
  $("#btn-profile-cancel").classList.add("hidden");
  $("#profile-test-status").textContent = "";
}

$("#btn-profile-save").addEventListener("click", async () => {
  const p = {
    alias: $("#pf-alias").value.trim(),
    model: $("#pf-model").value.trim(),
    baseUrl: $("#pf-base").value.trim(),
    apiKey: $("#pf-key").value.trim(),
    maxContext: parseInt($("#pf-ctx").value) || 0,
    format: $("#pf-format").value,
  };
  if (!p.alias || !p.model || !p.baseUrl || !p.apiKey) {
    await alertDlg("校验失败", "别名、Base URL、模型名称、API Key 均为必填项（最大上下文可留空）。");
    return;
  }
  const btn = $("#btn-profile-save"); btn.disabled = true;
  const st = $("#profile-test-status");
  st.textContent = "⏳ 正在测试连接…";
  try {
    await api("/api/llm/test", "POST", p);
  } catch (e) {
    st.textContent = "";
    btn.disabled = false;
    await alertDlg("测试失败", e.message);
    return;
  }
  st.textContent = "✅ 连接与对话测试通过，正在保存…";
  try {
    S.config = await api("/api/llm/profiles", "POST", p);
    st.textContent = "";
    btn.disabled = false;
    resetProfileForm();
    renderSettings(); renderChatHeader();
    await alertDlg("保存成功", `模型配置「${p.alias}」已保存。`);
  } catch (e) {
    st.textContent = "";
    btn.disabled = false;
    await alertDlg("保存失败", e.message);
  }
});

function renderPresets() {
  const presets = S.config.presets || [];
  const list = $("#presets-list"); list.innerHTML = "";
  presets.forEach((pr, i) => {
    const item = document.createElement("div");
    item.className = "preset-item";
    item.innerHTML = `<input class="p-label" value="${esc(pr.label)}" placeholder="AI选项">
      <textarea placeholder="提示词">${esc(pr.prompt)}</textarea>
      <button class="btn small">删除</button>`;
    item.querySelector(".p-label").onchange = (e) => { pr.label = e.target.value; savePresets(); };
    item.querySelector("textarea").onchange = (e) => { pr.prompt = e.target.value; savePresets(); };
    item.querySelector("button").onclick = async () => {
      S.config.presets.splice(i, 1); await savePresets(); renderPresets();
    };
    list.appendChild(item);
  });
}
async function savePresets() {
  S.config = await api("/api/config", "PUT", { presets: S.config.presets || [] });
}
$("#btn-preset-add").addEventListener("click", async () => {
  S.config.presets = S.config.presets || [];
  S.config.presets.push({ label: "新选项", prompt: "" });
  await savePresets(); renderPresets();
});

async function doExport() {
  const a = document.createElement("a");
  a.href = "/api/export";
  document.body.appendChild(a); a.click(); a.remove();
  toast("导出已开始下载");
}
// 按选中导出：type = "note" | "arch"
function doExportSel(type, ids) {
  if (!ids.length) { toast("请先勾选要导出的文档"); return; }
  const a = document.createElement("a");
  a.href = `/api/export?type=${encodeURIComponent(type)}&ids=${encodeURIComponent(ids.join(","))}`;
  document.body.appendChild(a); a.click(); a.remove();
  toast(`已导出 ${ids.length} 篇文档`);
}
$("#btn-export").addEventListener("click", doExport);
$("#btn-export2").addEventListener("click", doExport);
$("#btn-export-sel").addEventListener("click", () => doExportSel("note", [...notesSel]));
$("#btn-notes-chat").addEventListener("click", () => {
  const refs = [...notesSel].map(id => {
    const n = S.notes.find(x => x.id === id);
    return { type: "note", id, name: n ? n.name : "" };
  });
  if (refs.length) openChatWithRefs(refs);
});
$("#btn-arch-export").addEventListener("click", () => doExportSel("arch", [...archSel]));

/* ==================================================================
   CHAT PANEL
   ================================================================== */
function curChat() { return S.chats.find(c => c.id === curChatId) || null; }

// 讨论面板与内容区并列（挤压宽度），不再浮在上层
function openChatPanel() {
  $("#chat-panel").classList.remove("hidden");
  $("#chat-resizer").classList.remove("hidden");
}
function closeChatPanel() {
  $("#chat-panel").classList.add("hidden");
  $("#chat-resizer").classList.add("hidden");
}
(function bindResizer() {
  const bar = $("#chat-resizer");
  bar.addEventListener("mousedown", (e) => {
    e.preventDefault();
    const panel = $("#chat-panel");
    const startX = e.clientX, startW = panel.getBoundingClientRect().width;
    const onMove = (ev) => {
      const w = Math.max(320, Math.min(window.innerWidth - 520, startW + (startX - ev.clientX)));
      panel.style.flexBasis = w + "px";
    };
    const onUp = () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  });
})();

async function openChatWithRefs(refs) {
  openChatPanel();
  const chat = await api("/api/chats", "POST", { refs, alias: S.config.activeAlias, compact: refs.length > 1 });
  await refresh();
  curChatId = chat.id;
  renderChatHeader(); renderChatMessages();
  // jump left list to the referenced notes
  if (refs.length && refs[0].type === "note") { go("notes"); curNoteId = refs[0].id; renderNotes(); }
  else if (refs.length && refs[0].type === "arch") {
    const n = S.archive.find(a => a.id === refs[0].id);
    go("archive");
    if (n) { archExpanded.add(n.date); curArchId = n.id; }
    renderArchive();
  }
  $("#chat-input").focus();
}

$("#btn-chat-new").addEventListener("click", async () => {
  openChatPanel();
  const chat = await api("/api/chats", "POST", { refs: [], alias: S.config.activeAlias });
  await refresh(); curChatId = chat.id;
  renderChatHeader(); renderChatMessages();
});
$("#btn-chat-del").addEventListener("click", async () => {
  const c = curChat(); if (!c) return;
  if (!(await confirmDlg("删除对话", `确定删除对话「${c.title}」？记录将从本地清除。`))) return;
  await api("/api/chats/" + c.id, "DELETE");
  await refresh();
  curChatId = S.chats.length ? S.chats[0].id : null;
  renderChatHeader(); renderChatMessages();
});
$("#btn-chat-close").addEventListener("click", closeChatPanel);

// 对话重命名：默认 chat 1/2/3，可随时改成自己的标题
$("#btn-chat-rename").addEventListener("click", async () => {
  const c = curChat(); if (!c) { toast("还没有对话"); return; }
  showModal(`<h3>重命名对话</h3><label>标题</label><input id="rn-input" value="${esc(c.title)}">
    <div class="row-end"><button class="btn" id="rn-no">取消</button>
    <button class="btn primary" id="rn-yes">确定</button></div>`);
  $("#rn-no").onclick = closeModal;
  $("#rn-yes").onclick = async () => {
    const v = $("#rn-input").value.trim();
    closeModal();
    if (!v || v === c.title) return;
    await api("/api/chats/" + c.id, "PUT", { title: v });
    await refresh(); renderChatHeader(); toast("已重命名");
  };
  setTimeout(() => { const el = $("#rn-input"); if (el) { el.focus(); el.select(); } }, 0);
});
$("#chat-select").addEventListener("change", (e) => {
  curChatId = e.target.value;
  const c = curChat();
  if (c && c.noteIds && c.noteIds.length) {
    const ref = c.noteIds[0];
    if (ref.type === "note") { go("notes"); curNoteId = ref.id; renderNotes(); }
    else {
      const n = S.archive.find(a => a.id === ref.id);
      go("archive");
      if (n) { archExpanded.add(n.date); curArchId = n.id; }
      renderArchive();
    }
  }
  renderChatHeader(); renderChatMessages();
});

function renderChatHeader() {
  const sel = $("#chat-select"); sel.innerHTML = "";
  for (const c of S.chats) {
    const o = document.createElement("option");
    o.value = c.id; o.textContent = c.title;
    if (c.id === curChatId) o.selected = true;
    sel.appendChild(o);
  }
  if (!curChatId && S.chats.length) curChatId = S.chats[0].id;
  renderModelChip();
  const c = curChat();
  $("#chat-context").textContent = c && c.noteNames && c.noteNames.length
    ? "📎 上下文: " + c.noteNames.join("、") : "";
}

function renderModelChip() {
  const c = curChat();
  let p = null;
  if (c && c.alias) p = S.config.profiles.find(x => x.alias === c.alias) || null;
  if (!p) p = activeProfile();
  $("#model-chip").textContent = profileLabel(p);
}
$("#model-chip").addEventListener("click", (e) => {
  if (!S.config.profiles.length) { toast("请先到设置页添加模型"); return; }
  const html = S.config.profiles.map(p =>
    `<button data-alias="${esc(p.alias)}">${esc(profileLabel(p))}</button>`).join("");
  showCtxMenu(e.clientX - 100, e.clientY - 40 - S.config.profiles.length * 34, html);
  $$("#ctx-menu button").forEach(b => b.onclick = async () => {
    hideCtxMenu();
    if (curChatId) await api("/api/chats/" + curChatId, "PUT", { alias: b.dataset.alias });
    S.config = await api("/api/config", "PUT", { activeAlias: b.dataset.alias });
    await refresh(); renderChatHeader();
    toast("已切换模型");
  });
});

function tokenTotals(c) {
  let up = 0, down = 0;
  for (const m of c.messages) {
    if (m.role === "assistant") { up += m.promptTokens || 0; down += m.completionTokens || 0; }
  }
  return { up, down };
}

function renderChatMessages() {
  const box = $("#chat-messages"); box.innerHTML = "";
  const c = curChat();
  $("#chat-bubbles").innerHTML = "";
  if (!c) {
    box.innerHTML = `<div class="empty-state">点击 ＋ 新建对话，或在笔记/存档中选中文档后发起讨论</div>`;
    $("#token-stat").textContent = "↑0 ↓0";
    renderBubbles();
    return;
  }
  for (const m of c.messages) box.appendChild(msgEl(c, m));
  const t = tokenTotals(c);
  $("#token-stat").textContent = `↑${t.up} ↓${t.down}`;
  renderBubbles();
  box.scrollTop = box.scrollHeight;
}

function renderBubbles() {
  const c = curChat();
  const wrap = $("#chat-bubbles");
  if (c && c.messages.length) return; // only when empty
  for (const pr of S.config.presets) {
    const b = document.createElement("button");
    b.className = "bubble"; b.textContent = pr.label;
    b.onclick = () => streamTextToInput(pr.prompt);
    wrap.appendChild(b);
  }
}
let bubbleTyping = false;
function streamTextToInput(text) {
  if (bubbleTyping) return;
  const input = $("#chat-input");
  input.value = ""; bubbleTyping = true;
  let i = 0;
  const step = Math.max(1, Math.floor(text.length / 60));
  const timer = setInterval(() => {
    i = Math.min(text.length, i + step);
    input.value = text.slice(0, i);
    if (i >= text.length) { clearInterval(timer); bubbleTyping = false; }
    input.focus();
  }, 16);
}

function msgEl(chat, m) {
  const div = document.createElement("div");
  div.className = "msg " + m.role;
  div.dataset.id = m.id;
  if (m.role === "assistant") {
    div.innerHTML = `<div class="body">${md(m.content)}</div>`;
  } else {
    div.innerHTML = `<div class="body">${esc(m.content)}</div>
      <div class="actions">
        <button data-a="copy" title="复制">📋</button>
        <button data-a="edit" title="编辑">✏️</button>
        <button data-a="rollback" title="回退到此处">↩️</button>
        <button data-a="del" title="删除">🗑</button>
      </div>`;
  }
  if (m.role === "user") {
    div.querySelector('[data-a="copy"]').onclick = async () => {
      try { await navigator.clipboard.writeText(m.content); toast("已复制到剪贴板"); }
      catch { toast("复制失败"); }
    };
    div.querySelector('[data-a="del"]').onclick = async () => {
      if (!(await confirmDlg("删除消息", "删除该条对话（连同其 AI 回复）？"))) return;
      await api(`/api/chats/${chat.id}/messages/${m.id}`, "DELETE");
      await refresh(); renderChatMessages();
    };
    div.querySelector('[data-a="rollback"]').onclick = async () => {
      if (!(await confirmDlg("回退对话", "将删除此条及之后的所有对话，内容会放回输入框供重新编辑。继续？"))) return;
      await api(`/api/chats/${chat.id}/messages/${m.id}?from=1`, "DELETE");
      await refresh();
      $("#chat-input").value = m.content;
      $("#chat-input").focus();
      renderChatMessages();
    };
    div.querySelector('[data-a="edit"]').onclick = () => {
      const body = div.querySelector(".body");
      const ta = document.createElement("textarea");
      ta.className = "edit-area"; ta.value = m.content;
      const bar = document.createElement("div");
      bar.className = "row-end";
      bar.innerHTML = `<button class="btn small" data-a="cancel">取消</button>
        <button class="btn small primary" data-a="ok">确认并重新提交</button>`;
      body.replaceWith(ta); div.appendChild(bar);
      div.querySelector(".actions").style.display = "none";
      bar.querySelector('[data-a="cancel"]').onclick = () => renderChatMessages();
      bar.querySelector('[data-a="ok"]').onclick = () => sendChat(chat.id, ta.value.trim(), m.id);
    };
  }
  return div;
}

$("#btn-chat-send").addEventListener("click", () => {
  const v = $("#chat-input").value.trim();
  if (v && curChatId) sendChat(curChatId, v);
});
$("#chat-input").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    const v = $("#chat-input").value.trim();
    if (v && curChatId) sendChat(curChatId, v);
  }
});

let sending = false;
async function sendChat(chatId, content, fromMsgId) {
  if (sending) return;
  sending = true;
  $("#btn-chat-send").disabled = true;
  $("#chat-input").value = "";
  const box = $("#chat-messages");

  // optimistic user bubble
  const userDiv = document.createElement("div");
  userDiv.className = "msg user";
  userDiv.innerHTML = `<div class="body">${esc(content)}</div>`;
  box.appendChild(userDiv);
  const asstDiv = document.createElement("div");
  asstDiv.className = "msg assistant streaming";
  asstDiv.innerHTML = `<div class="body"></div>`;
  box.appendChild(asstDiv);
  const asstBody = asstDiv.querySelector(".body");
  box.scrollTop = box.scrollHeight;

  let acc = "";
  let errText = "";
  try {
    const resp = await fetch(`/api/chats/${chatId}/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content, messageId: fromMsgId || "" }),
    });
    if (!resp.ok) {
      const d = await resp.json().catch(() => ({}));
      throw new Error(d.error || "HTTP " + resp.status);
    }
    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const parts = buf.split("\n\n");
      buf = parts.pop();
      for (const part of parts) {
        const line = part.trim();
        if (!line.startsWith("data:")) continue;
        let ev;
        try { ev = JSON.parse(line.slice(5)); } catch { continue; }
        if (ev.type === "delta") {
          acc += ev.text;
          asstBody.innerHTML = md(acc);
          box.scrollTop = box.scrollHeight;
        } else if (ev.type === "error") {
          errText = ev.error || "未知错误";
          asstBody.innerHTML = `<div style="color:var(--danger)">⚠ ${esc(errText)}</div>`;
        } else if (ev.type === "saved") {
          const c = S.chats.find(x => x.id === chatId);
          if (c) { c.messages.push({ role: "assistant", promptTokens: ev.promptTokens, completionTokens: ev.completionTokens }); }
        } else if (ev.type === "title") {
          // 首轮回答后由 AI 自动总结出的标题
          const c = S.chats.find(x => x.id === chatId);
          if (c && ev.title) { c.title = ev.title; c.titleAuto = false; }
          renderChatHeader();
          $("#chat-select").value = chatId;
        }
      }
    }
  } catch (e) {
    errText = e.message || String(e);
    asstBody.innerHTML = `<div style="color:var(--danger)">⚠ ${esc(errText)}</div>`;
  }
  asstDiv.classList.remove("streaming");
  await refresh();
  renderChatHeader(); renderChatMessages();
  if (errText) {
    // 失败信息不能随重渲染消失：补一条常驻提示，并把原内容放回输入框方便重试
    const errDiv = document.createElement("div");
    errDiv.className = "msg assistant";
    errDiv.innerHTML = `<div class="body" style="color:var(--danger)">⚠ ${esc(errText)}</div>`;
    box.appendChild(errDiv);
    box.scrollTop = box.scrollHeight;
    if (!$("#chat-input").value) $("#chat-input").value = content;
    toast("发送失败：" + errText);
  }
  sending = false;
  $("#btn-chat-send").disabled = false;
}

/* ==================================================================
   boot
   ================================================================== */
(async function boot() {
  try {
    await refresh();
    if (S.chats.length) curChatId = S.chats[0].id;
    renderChatHeader(); renderChatMessages();
    go("notes");
  } catch (e) {
    console.error("[NoteHarness] boot failed", e);
    document.body.insertAdjacentHTML("afterbegin",
      `<div style="padding:16px;background:#fee;color:#b00;font-size:14px">初始化失败：${esc((e && e.message) || e)}<br>请确认只运行了一个 NoteHarness 实例（不要重复双击启动）。</div>`);
  }
})();
