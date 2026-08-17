/* ============ OpenList Sync — frontend ============ */
"use strict";

const state = {
  connections: [],
  tasks: [],
  settings: {},
  running: [],
  logs: [],
  version: "",
  currentView: "overview",
  busy: new Set(), // task ids running in UI
};

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const esc = (s) =>
  String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
  );
const fmtBytes = (n) => {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(n < 10 && i > 0 ? 1 : 0) + " " + u[i];
};
const fmtDur = (s) => {
  if (!s) return "—";
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)?$/.exec(String(s));
  return s;
};
const fmtTime = (iso) => {
  if (!iso) return "从未";
  const d = new Date(iso);
  if (isNaN(d)) return "从未";
  const now = Date.now();
  const diff = now - d.getTime();
  if (diff < 60000) return "刚刚";
  if (diff < 3600000) return Math.floor(diff / 60000) + " 分钟前";
  if (diff < 86400000) return Math.floor(diff / 3600000) + " 小时前";
  const pad = (x) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

const ICONS = {
  play: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 5.5v13l11-6.5z" fill="currentColor" stroke="none"/></svg>',
  refresh: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.3"/><path d="M21 3v6h-6"/></svg>',
  plus: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>',
  trash: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v6M14 11v6"/></svg>',
  edit: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>',
  test: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12h4l3-9 6 18 3-9h4"/></svg>',
  check: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>',
  server: '<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/></svg>',
  task: '<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 11l3 3 8-8"/><path d="M20 12v6a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h9"/></svg>',
  arrow: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 17 17 7M8 7h9v9"/></svg>',
  paused: '<svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><rect x="6" y="5" width="4" height="14" rx="1.2"/><rect x="14" y="5" width="4" height="14" rx="1.2"/></svg>',
};

/* ---------- API ---------- */

const API_TOKEN_KEY = "openlist.apiToken";
let apiToken = localStorage.getItem(API_TOKEN_KEY) || "";
let loginPending = null;
let loginSuppressedUntil = 0;

class AuthError extends Error {}

async function api(path, method = "GET", body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  if (apiToken) opts.headers["X-API-Token"] = apiToken;
  let res = await fetch(path, opts);
  if (res.status === 401 && Date.now() >= loginSuppressedUntil) {
    const tok = await loginPrompt();
    if (tok) {
      apiToken = tok;
      localStorage.setItem(API_TOKEN_KEY, apiToken);
      opts.headers["X-API-Token"] = apiToken;
      res = await fetch(path, opts);
    } else {
      loginSuppressedUntil = Date.now() + 30000;
    }
  }
  if (res.status === 401) throw new AuthError("unauthorized: invalid API token");
  let data = null;
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) throw new Error(data?.error || `请求失败 (${res.status})`);
  return data;
}

function loginPrompt() {
  if (loginPending) return loginPending;
  const bd = $("#login-backdrop");
  const input = $("#login-token");
  const okBtn = $("#login-ok");
  const cancelBtn = $("#login-cancel");
  const close = () => {
    bd.hidden = true;
    okBtn.onclick = null;
    cancelBtn.onclick = null;
    input.onkeydown = null;
  };
  loginPending = new Promise((resolve) => {
    bd.hidden = false;
    input.value = "";
    setTimeout(() => input.focus(), 60);
    const submit = (e) => {
      e.preventDefault();
      const v = input.value.trim();
      close();
      resolve(v);
    };
    okBtn.onclick = submit;
    cancelBtn.onclick = () => { close(); resolve(""); };
    input.onkeydown = (e) => { if (e.key === "Enter") submit(e); };
  }).finally(() => { loginPending = null; });
  return loginPending;
}

async function loadState() {
  try {
    const d = await api("/api/state");
    state.connections = d.connections || [];
    state.tasks = d.tasks || [];
    state.settings = d.settings || {};
    state.running = d.running || [];
    state.version = d.version || "";
    $("#version").textContent = state.version;
    updateSidebarStatus();
    render();
  } catch (e) {
    if (e instanceof AuthError) return;
    toast(e.message, "error");
  }
}

async function loadLogs() {
  if (state.currentView !== "logs") return;
  try {
    const d = await api("/api/logs?n=1500");
    const prev = state.logs.length;
    state.logs = d.logs;
    renderLogs(prev);
  } catch (_) {}
}

/* ---------- Toast / confirm ---------- */

let toastTimer;
function toast(msg, kind = "ok") {
  const t = $("#toast");
  t.textContent = msg;
  t.style.visibility = "visible";
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 2600);
}

function confirmDialog(title, text, onOk, okLabel = "删除") {
  $("#confirm-title").textContent = title;
  $("#confirm-text").textContent = text;
  $("#confirm-ok").textContent = okLabel;
  const open = () => { $("#confirm-backdrop").hidden = false; };
  const close = () => { $("#confirm-backdrop").hidden = true; };
  $("#confirm-cancel").onclick = close;
  $("#confirm-ok").onclick = () => { close(); onOk(); };
  open();
}

/* ---------- Sheet ---------- */

let sheetOnClose = null;
function openSheet(title, html, onClose) {
  $("#sheet-title").textContent = title;
  $("#sheet-body").innerHTML = html;
  $("#sheet-backdrop").hidden = false;
  sheetOnClose = onClose || null;
}
function closeSheet() {
  $("#sheet-backdrop").hidden = true;
  if (sheetOnClose) { const fn = sheetOnClose; sheetOnClose = null; fn(); }
}
$("#sheet-close").addEventListener("click", closeSheet);
$("#sheet-backdrop").addEventListener("click", (e) => {
  if (e.target === $("#sheet-backdrop")) closeSheet();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { closeSheet(); $("#confirm-backdrop").hidden = true; closeSidebar(); }
});

/* ---------- Mobile sidebar ---------- */

function openSidebar() {
  $("#sidebar").classList.add("open");
  const b = $("#sidebar-backdrop");
  b.hidden = false;
  requestAnimationFrame(() => b.classList.add("show"));
}
function closeSidebar() {
  $("#sidebar").classList.remove("open");
  const b = $("#sidebar-backdrop");
  b.classList.remove("show");
  setTimeout(() => { b.hidden = true; }, 320);
}
$("#menu-btn").addEventListener("click", () => {
  if ($("#sidebar").classList.contains("open")) closeSidebar();
  else openSidebar();
});
$("#sidebar-backdrop").addEventListener("click", closeSidebar);

/* ---------- Navigation ---------- */

const VIEWS = {
  overview: { title: "概览", actions: () => `
    <button class="btn btn-primary" onclick="runAll()">${ICONS.play}运行全部</button>
  ` },
  connections: { title: "连接", actions: () => `
    <button class="btn btn-primary" onclick="connForm()">${ICONS.plus}添加连接</button>
  ` },
  tasks: { title: "任务", actions: () => `
    <button class="btn btn-primary" onclick="taskForm()">${ICONS.plus}添加任务</button>
  ` },
  logs: { title: "日志", actions: () => `` },
  settings: { title: "设置", actions: () => `
    <button class="btn btn-primary" onclick="saveSettings()">${ICONS.check}保存设置</button>
  ` },
};

function navigate(view) {
  state.currentView = view;
  $$(".nav-item").forEach((el) => el.classList.toggle("active", el.dataset.view === view));
  const v = VIEWS[view];
  $("#page-title").textContent = v.title;
  $("#topbar-actions").innerHTML = v.actions();
  closeSidebar();
  render();
}

function render() {
  switch (state.currentView) {
    case "overview": renderOverview(); break;
    case "connections": renderConnections(); break;
    case "tasks": renderTasks(); break;
    case "logs": loadLogs(); break;
    case "settings": renderSettings(); break;
  }
}

function updateSidebarStatus() {
  const running = state.running.length;
  const dot = $("#status-dot");
  const text = $("#status-text");
  if (running > 0) {
    dot.className = "dot busy";
    text.textContent = `${running} 个任务运行中`;
  } else if (state.tasks.some((t) => t.enabled)) {
    dot.className = "dot";
    text.textContent = "调度已启用";
  } else {
    dot.className = "dot";
    text.textContent = "空闲";
  }
}

function connName(id) {
  const c = state.connections.find((c) => c.id === id);
  return c ? c.name : "(已删除)";
}

function taskRunning(id) { return state.running.includes(id); }

function statusBadge(t) {
  if (taskRunning(t.id)) return '<span class="badge badge-amber"><span class="dot"></span>运行中</span>';
  switch (t.last_status) {
    case "ok": return '<span class="badge badge-green"><span class="dot"></span>成功</span>';
    case "error": return '<span class="badge badge-red"><span class="dot"></span>失败</span>';
    default: return '<span class="badge badge-gray">未运行</span>';
  }
}

function dirBadge(d) {
  const map = { both: ["双向同步", "badge-blue"], pull: ["只下载", "badge-teal"], push: ["只上传", "badge-purple"] };
  const [label, cls] = map[d] || [d, "badge-gray"];
  return `<span class="badge ${cls}">${label}</span>`;
}

/* ================= Overview ================= */

function renderOverview() {
  const running = state.running.length;
  const enabled = state.tasks.filter((t) => t.enabled).length;
  const okCount = state.tasks.filter((t) => t.last_status === "ok").length;
  const view = $("#view");

  const stats = `
    <div class="card stat-card">
      <div class="stat-grid">
        <div class="stat-cell">
          <span class="stat-label">OpenList 连接</span>
          <span class="stat-value">${state.connections.length}</span>
        </div>
        <div class="stat-cell">
          <span class="stat-label">同步任务</span>
          <span class="stat-value">${state.tasks.length} <small>启用 ${enabled}</small></span>
        </div>
        <div class="stat-cell">
          <span class="stat-label">运行中</span>
          <span class="stat-value" style="color:${running ? "var(--amber)" : "var(--text)"}">${running}</span>
        </div>
        <div class="stat-cell">
          <span class="stat-label">最近成功</span>
          <span class="stat-value">${okCount}<small>/ ${state.tasks.length}</small></span>
        </div>
      </div>
    </div>`;

  let body;
  if (state.tasks.length === 0) {
    body = `
      <div class="card">
        <div class="empty">
          <div class="empty-icon">${ICONS.task.replace('width="20"','width="28"')}</div>
          <div class="empty-title">还没有同步任务</div>
          <div class="empty-actions">
            <button class="btn btn-primary" onclick="navigate('connections')">添加连接</button>
            <button class="btn" onclick="navigate('tasks')">创建任务</button>
          </div>
        </div>
      </div>`;
  } else {
    const cards = state.tasks.map((t) => `
      <div class="card card-btn" onclick="navigate('tasks')">
        <div class="cell task-detail" style="padding:16px 20px;">
          <div class="row-body">
            <div class="row-title">
              ${esc(t.name)}
              ${t.enabled ? "" : `<span class="badge badge-gray">${ICONS.paused} 暂停</span>`}
            </div>
            <div class="row-sub">
              <span class="mono">${esc(t.remote_path)}</span> ${ICONS.arrow} <span class="mono">${esc(t.local_dir)}</span>
            </div>
          </div>
          <div style="text-align:right">
            ${statusBadge(t)}
            <div class="small muted" style="margin-top:4px">${fmtTime(t.last_run)}</div>
          </div>
        </div>
      </div>
    `).join("");
    body = `<div class="grid" style="grid-template-columns:1fr 1fr">${cards}</div>`;
  }

  view.innerHTML = stats + body;
}

async function runAll() {
  const prom = state.tasks.map((t) =>
    api("/api/tasks/run", "POST", { id: t.id }).catch((e) => { toast(e.message, "error"); })
  );
  await Promise.all(prom);
  toast("已触发全部任务");
  setTimeout(loadState, 300);
}

/* ================= Connections ================= */

function renderConnections() {
  const view = $("#view");

  if (state.connections.length === 0) {
    view.innerHTML = `
      <div class="card">
        <div class="empty">
          <div class="empty-icon">${ICONS.server.replace('width="20"','width="28"')}</div>
          <div class="empty-title">没有 OpenList 连接</div>
          <div class="empty-actions"><button class="btn btn-primary" onclick="connForm()">${ICONS.plus}添加连接</button></div>
        </div>
      </div>`;
    return;
  }

  const rows = state.connections.map((c) => {
    const authBadge = c.auth_type === "token"
      ? '<span class="badge badge-purple">令牌 Token</span>'
      : '<span class="badge badge-teal">用户名密码</span>';
    const mode = c.download_mode === "proxy"
      ? '<span class="badge badge-blue">代理模式</span>'
      : '<span class="badge badge-gray">直连</span>';
    const cnt = state.tasks.filter((t) => t.connection_id === c.id).length;
    return `
      <div class="row">
        <div class="row-icon colored">${ICONS.server}</div>
        <div class="row-body">
          <div class="row-title">${esc(c.name)} <span class="mono">${esc(c.base_url)}</span></div>
          <div class="row-sub">${authBadge} ${mode} <span class="muted">· ${cnt} 个任务</span></div>
        </div>
        <div class="row-actions">
          <button class="btn btn-sm btn-ghost" onclick="testConn('${c.id}')">${ICONS.test}测试</button>
          <button class="icon-btn" onclick="connForm('${c.id}')" title="编辑">${ICONS.edit}</button>
          <button class="icon-btn danger" onclick="deleteConn('${c.id}')" title="删除">${ICONS.trash}</button>
        </div>
      </div>`;
  }).join("");

  view.innerHTML = `<div class="card"><div>${rows}</div></div>`;
}

async function testConn(id) {
  const c = state.connections.find((c) => c.id === id);
  if (!c) return;
  toast("正在测试连接…", "ok");
  try {
    const d = await api("/api/connections/test", "POST", { id });
    toast(d.message || "连接正常");
  } catch (e) {
    toast(e.message, "error");
  }
}

function connForm(id) {
  const c = state.connections.find((c) => c.id === id) || {};
  openSheet(c.id ? "编辑连接" : "添加连接", `
    <div class="form-group">
      <label class="form-label">名称 <span class="req">*</span></label>
      <input class="field" id="conn-name" placeholder="例如：家庭 OpenList" value="${esc(c.name || "")}">
    </div>
    <div class="form-group">
      <label class="form-label">服务器地址 Base URL <span class="req">*</span></label>
      <input class="field" id="conn-url" placeholder="https://openlist.example.com" value="${esc(c.base_url || "")}">
    </div>
    <div class="form-group">
      <label class="form-label">认证方式 <span class="req">*</span></label>
      <div class="segment" id="conn-auth-seg">
        <button data-v="token" class="${(!c.auth_type || c.auth_type === "token") ? "active" : ""}">令牌 Token</button>
        <button data-v="password" class="${c.auth_type === "password" ? "active" : ""}">用户名密码</button>
      </div>
    </div>
    <div id="conn-auth-token">
      <div class="form-group">
        <label class="form-label">访问令牌 Token <span class="req">*</span></label>
        <input class="field mono" id="conn-token" type="password" placeholder="粘贴 OpenList token" value="${esc(c.token || "")}" autocomplete="off">
      </div>
    </div>
    <div id="conn-auth-password" class="hidden">
      <div class="field-grid">
        <div class="field-group">
          <label>用户名 <span class="req">*</span></label>
          <input class="field" id="conn-user" value="${esc(c.username || "")}" autocomplete="off">
        </div>
        <div class="field-group">
          <label>密码 <span class="req">*</span></label>
          <input class="field" id="conn-pass" type="password" value="${esc(c.password || "")}" autocomplete="off">
        </div>
      </div>
    </div>
    <div class="form-group">
      <label class="form-label">下载模式</label>
      <div class="segment" id="conn-mode-seg">
        <button data-v="direct" class="${(!c.download_mode || c.download_mode === "direct") ? "active" : ""}">直连 /d</button>
        <button data-v="proxy" class="${c.download_mode === "proxy" ? "active" : ""}">代理 /p</button>
      </div>
    </div>
    <div class="divider"></div>
    <div class="flex-between">
      <button class="btn" id="conn-test-inline" onclick="testConnInline()">${ICONS.test}测试连接</button>
      <button class="btn btn-primary btn-lg" onclick="saveConn('${c.id || ""}')">保存连接</button>
    </div>
  `);

  setupSegment($("#conn-auth-seg"), () => {
    const v = currentSeg($("#conn-auth-seg"));
    $("#conn-auth-token").classList.toggle("hidden", v !== "token");
    $("#conn-auth-password").classList.toggle("hidden", v !== "password");
  });
  setupSegment($("#conn-mode-seg"));
}

function currentSeg(el) { return el.querySelector("button.active")?.dataset.v; }
function setupSegment(el, onChange) {
  $$("button", el).forEach((b) => b.addEventListener("click", () => {
    $$("button", el).forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    onChange && onChange();
  }));
}

async function testConnInline() {
  const c = connFromForm();
  if (!c) return;
  try {
    const d = await api("/api/connections/test", "POST", c);
    toast(d.message || "连接正常");
  } catch (e) {
    toast(e.message, "error");
  }
}

function connFromForm() {
  const c = {
    name: $("#conn-name").value.trim(),
    base_url: $("#conn-url").value.trim(),
    auth_type: currentSeg($("#conn-auth-seg")),
    token: $("#conn-token").value.trim(),
    username: $("#conn-user").value.trim(),
    password: $("#conn-pass").value,
    download_mode: currentSeg($("#conn-mode-seg")),
  };
  if (!c.name || !c.base_url) { toast("请填写名称和服务器地址", "error"); return null; }
  if (c.auth_type === "token" && !c.token) { toast("请填写令牌", "error"); return null; }
  if (c.auth_type === "password" && (!c.username || !c.password)) { toast("请填写用户名和密码", "error"); return null; }
  return c;
}

async function saveConn(id) {
  const c = connFromForm();
  if (!c) return;
  c.id = id;
  try {
    await api("/api/connections/save", "POST", c);
    closeSheet();
    toast("连接已保存");
    loadState();
  } catch (e) { toast(e.message, "error"); }
}

function deleteConn(id) {
  const c = state.connections.find((c) => c.id === id);
  confirmDialog("删除连接", `确定删除连接“${c?.name}”吗？其下的同步任务也会一并删除。`, async () => {
    try {
      await api("/api/connections/delete", "POST", { id });
      toast("连接已删除");
      loadState();
    } catch (e) { toast(e.message, "error"); }
  });
}

/* ================= Tasks ================= */

function renderTasks() {
  const view = $("#view");

  if (state.tasks.length === 0) {
    view.innerHTML = `
      <div class="card">
        <div class="empty">
          <div class="empty-icon">${ICONS.task.replace('width="20"','width="28"')}</div>
          <div class="empty-title">没有同步任务</div>
          <div class="empty-actions"><button class="btn btn-primary" onclick="taskForm()">${ICONS.plus}添加任务</button></div>
        </div>
      </div>`;
    return;
  }

  const rows = state.tasks.map((t) => {
    const running = taskRunning(t.id);
    return `
      <div class="row">
        <div class="row-icon ${t.enabled ? "green" : ""}">${ICONS.task}</div>
        <div class="row-body">
          <div class="row-title">
            ${esc(t.name)}
            ${dirBadge(t.direction)}
            ${t.enabled ? "" : '<span class="badge badge-gray">已暂停</span>'}
          </div>
          <div class="row-sub">
            <span class="mono">${esc(connName(t.connection_id))}</span>
            <span class="muted">·</span>
            <span class="mono">${esc(t.remote_path)}</span> ${ICONS.arrow} <span class="mono">${esc(t.local_dir)}</span>
            ${t.interval ? `<span class="badge badge-blue">${t.enabled ? "每 " + esc(t.interval) + " 自动同步" : "已设间隔 " + esc(t.interval)}</span>` : ""}
          </div>
          <div class="row-sub" style="margin-top:5px">
            ${statusBadge(t)}
            <span class="muted">上次 ${fmtTime(t.last_run)}</span>
            ${t.last_error && !running ? `<span class="error-text">${esc(t.last_error)}</span>` : ""}
          </div>
        </div>
        <div class="row-actions">
          <button class="btn btn-sm btn-primary" onclick="runTask('${t.id}')" ${running ? "disabled" : ""}>
            ${running ? '<span class="spinner white"></span>运行中' : ICONS.play + "运行"}
          </button>
          <button class="icon-btn" onclick="taskForm('${t.id}')" title="编辑">${ICONS.edit}</button>
          <button class="icon-btn danger" onclick="deleteTask('${t.id}')" title="删除">${ICONS.trash}</button>
        </div>
      </div>`;
  }).join("");

  view.innerHTML = `<div class="card"><div>${rows}</div></div>`;
}

async function runTask(id) {
  try {
    await api("/api/tasks/run", "POST", { id });
    toast("任务已开始运行");
    setTimeout(loadState, 500);
  } catch (e) { toast(e.message, "error"); }
}

function taskForm(id) {
  const t = state.tasks.find((t) => t.id === id) || {};
  const connOptions = state.connections.map((c) =>
    `<option value="${c.id}" ${c.id === t.connection_id ? "selected" : ""}>${esc(c.name)} · ${esc(c.base_url)}</option>`
  ).join("");
  const hasConn = state.connections.length > 0;

  if (!hasConn) {
    openSheet("添加任务", `
      <div class="empty">
        <div class="empty-title">请先添加连接</div>
        <div class="empty-actions"><button class="btn btn-primary" onclick="closeSheet();navigate('connections')">去添加连接</button></div>
      </div>`);
    return;
  }

  const types = ["video", "audio", "image", "text"];
  const typeChips = types.map((tp) => {
    const on = (t.types || []).includes(tp) ? "on" : "";
    const label = { video: "视频", audio: "音频", image: "图片", text: "文本" }[tp];
    return `<span class="chip ${on}" data-type="${tp}" onclick="this.classList.toggle('on')">${label}</span>`;
  }).join("");

  openSheet(t.id ? "编辑任务" : "添加任务", `
    <div class="form-group">
      <label class="form-label">任务名称 <span class="req">*</span></label>
      <input class="field" id="task-name" placeholder="例如：影视库备份" value="${esc(t.name || "")}">
    </div>
    <div class="form-group">
      <label class="form-label">OpenList 连接 <span class="req">*</span></label>
      <select class="field" id="task-conn">
        <option value="">选择连接…</option>
        ${connOptions}
      </select>
    </div>
    <div class="form-group">
      <label class="form-label">远端路径 <span class="req">*</span></label>
      <input class="field mono" id="task-remote" placeholder="/backup 或 /" value="${esc(t.remote_path || "")}">
    </div>
    <div class="form-group">
      <label class="form-label">本地目录 <span class="req">*</span></label>
      <input class="field mono" id="task-local" placeholder="/data/backup" value="${esc(t.local_dir || "")}">
    </div>
    <div class="form-group">
      <label class="form-label">同步方向</label>
      <div class="segment" id="task-dir">
        <button data-v="both" class="${(!t.direction || t.direction === "both") ? "active" : ""}">双向同步</button>
        <button data-v="pull" class="${t.direction === "pull" ? "active" : ""}">只下载</button>
        <button data-v="push" class="${t.direction === "push" ? "active" : ""}">只上传</button>
      </div>
    </div>
    <div class="field-grid">
      <div class="field-group">
        <label>冲突策略</label>
        <select class="field" id="task-conflict">
          ${[["newest", "保留较新的"], ["remote", "远端优先"], ["local", "本地优先"], ["skip", "跳过不处理"]].map(([v, l]) =>
            `<option value="${v}" ${(t.conflict || "newest") === v ? "selected" : ""}>${l}</option>`).join("")}
        </select>
      </div>
      <div class="field-group">
        <label>清理模式</label>
        <select class="field" id="task-cleanup">
          ${[["none", "不清理"], ["local", "删除本地多余文件"], ["remote", "删除远端多余文件"], ["both", "两侧都删除"]].map(([v, l]) =>
            `<option value="${v}" ${(t.cleanup || "none") === v ? "selected" : ""}>${l}</option>`).join("")}
        </select>
      </div>
    </div>
    <div class="form-group">
      <label class="form-label">文件类型（留空 = 全部）</label>
      <div class="chip-row">${typeChips}</div>
    </div>
    <div class="form-group">
      <label class="form-label">扩展名包含（逗号分隔，留空 = 全部）</label>
      <input class="field mono" id="task-include" placeholder="*.mp4, *.mkv" value="${esc((t.include_ext || []).join(", "))}">
    </div>
    <div class="form-group">
      <label class="form-label">扩展名排除（逗号分隔）</label>
      <input class="field mono" id="task-exclude" placeholder="*.part, *.tmp" value="${esc((t.exclude_ext || []).join(", "))}">
    </div>
    <div class="field-grid">
      <div class="field-group">
        <label>自动同步间隔（每任务单独设置）</label>
        <input class="field mono" id="task-interval" placeholder="30s / 1h / 1d" value="${esc(t.interval || "")}">
      </div>
      <div class="field-group">
        <label>限速 (B/s, 0 = 不限)</label>
        <input class="field mono" id="task-rate" type="number" min="0" placeholder="0" value="${t.rate_limit || 0}">
      </div>
    </div>
    <div class="flex-between" style="margin-top:8px">
      <div class="flex">
        <span class="muted small">启用自动同步</span>
        <label class="switch">
          <input type="checkbox" id="task-enabled" ${t.enabled ? "checked" : ""}>
          <span class="track"></span>
        </label>
      </div>
      <button class="btn btn-primary btn-lg" onclick="saveTask('${t.id || ""}')">保存任务</button>
    </div>
  `);

  setupSegment($("#task-dir"));
}

async function saveTask(id) {
  const name = $("#task-name").value.trim();
  const conn = $("#task-conn").value;
  const remote = $("#task-remote").value.trim();
  const local = $("#task-local").value.trim();
  if (!name && !remote) { toast("请填写任务名称或远端路径", "error"); return; }
  if (!conn) { toast("请选择连接", "error"); return; }
  if (!remote) { toast("请填写远端路径", "error"); return; }
  if (!local) { toast("请填写本地目录", "error"); return; }

  const t = {
    id,
    name: name || remote,
    connection_id: conn,
    remote_path: remote,
    local_dir: local,
    direction: currentSeg($("#task-dir")),
    conflict: $("#task-conflict").value,
    cleanup: $("#task-cleanup").value,
    include_ext: ($("#task-include").value || "").split(",").map((s) => s.trim()).filter(Boolean),
    exclude_ext: ($("#task-exclude").value || "").split(",").map((s) => s.trim()).filter(Boolean),
    types: $$(".chip.on", $("#sheet-body")).map((c) => c.dataset.type),
    interval: $("#task-interval").value.trim(),
    rate_limit: parseInt($("#task-rate").value, 10) || 0,
    enabled: $("#task-enabled").checked,
  };
  try {
    await api("/api/tasks/save", "POST", t);
    closeSheet();
    toast("任务已保存");
    loadState();
  } catch (e) { toast(e.message, "error"); }
}

function deleteTask(id) {
  const t = state.tasks.find((t) => t.id === id);
  confirmDialog("删除任务", `确定删除任务“${t?.name}”吗？`, async () => {
    try {
      await api("/api/tasks/delete", "POST", { id });
      toast("任务已删除");
      loadState();
    } catch (e) { toast(e.message, "error"); }
  });
}

/* ================= Logs ================= */

let logStick = true;
function renderLogs(prevCount) {
  const view = $("#view");
  const lines = state.logs.map((l) => {
    let cls = "";
    const msg = l.msg;
    if (msg.includes("failed") || /失败|error|错误/i.test(msg)) cls = "error";
    else if (/完成|成功|ok$/i.test(msg) || msg.startsWith("[") && msg.match(/task/)) cls = "ok";
    return `<div class="log-line ${cls}"><span class="log-time">${esc(l.time)}</span><span class="log-msg">${esc(msg)}</span></div>`;
  }).join("") || '<div class="log-line"><span class="muted">暂无日志</span></div>';

  view.innerHTML = `
    <div class="card">
      <div class="card-head">
        <div>
          <h2>同步日志</h2>
          <div class="sub">共 ${state.logs.length} 条记录 · 自动刷新 ${logStick ? "· 跟随最新" : ""}</div>
        </div>
        <button class="ghost icon-btn" id="log-clear" title="清空日志">${ICONS.trash}</button>
        <label class="switch" title="自动滚动跟随最新日志">
          <input type="checkbox" id="log-stick" checked>
          <span class="track"></span>
        </label>
      </div>
      <div class="log-console" id="log-console">${lines}</div>
    </div>`;

  $("#log-stick").checked = logStick;
  $("#log-stick").addEventListener("change", (e) => { logStick = e.target.checked; });
  $("#log-clear").onclick = async () => {
    await api("/api/logs/clear", "POST", {});
    state.logs = [];
    renderLogs();
    toast("日志已清空");
  };

  const consoleEl = $("#log-console");
  const shouldStick = logStick || state.logs.length <= prevCount;
  if (shouldStick) consoleEl.scrollTop = consoleEl.scrollHeight;
}

/* ================= Settings ================= */

function renderSettings() {
  const s = state.settings;
  const view = $("#view");
  view.innerHTML = `
    <div class="card">
      <div class="card-head">
        <h2>全局同步参数</h2>
      </div>
      <div style="padding: 6px 20px 22px">
        <div class="field-grid">
          <div class="field-group">
            <label>默认调度间隔</label>
            <input class="field mono" id="set-interval" placeholder="1h" value="${esc(s.interval || "1h")}">
          </div>
          <div class="field-group">
            <label>并发数</label>
            <input class="field mono" id="set-concurrency" type="number" min="1" max="64" value="${s.concurrency || 4}">
          </div>
          <div class="field-group">
            <label>全局限速 (B/s)</label>
            <input class="field mono" id="set-rate" type="number" min="0" value="${s.rate_limit || 0}">
          </div>
          <div class="field-group">
            <label>失败重试次数</label>
            <input class="field mono" id="set-retries" type="number" min="0" max="20" value="${s.retries || 3}">
          </div>
        </div>
      </div>
    </div>

    <div class="card mt-24">
      <div class="card-head">
        <h2>调度概览</h2>
        <div class="sub">自动同步由后台调度器执行</div>
      </div>
      <div style="padding: 6px 20px 22px">
        <div class="task-detail">
          <div class="row-body">
            <div class="row-title">
              当前状态
              <span class="badge ${state.tasks.some(t=>t.enabled) ? "badge-green":"badge-gray"}">
                <span class="dot"></span>${state.tasks.some(t=>t.enabled) ? "调度已启用" : "无启用任务"}
              </span>
            </div>
            <div class="row-sub">启用中的任务将按各自间隔自动同步，也可随时手动运行。</div>
          </div>
        </div>
      </div>
    </div>
  `;
}

async function saveSettings() {
  const st = {
    interval: $("#set-interval").value.trim() || "1h",
    concurrency: parseInt($("#set-concurrency").value, 10) || 4,
    rate_limit: parseInt($("#set-rate").value, 10) || 0,
    retries: parseInt($("#set-retries").value, 10) || 3,
  };
  try {
    await api("/api/settings/save", "POST", st);
    toast("设置已保存");
    loadState();
  } catch (e) { toast(e.message, "error"); }
}

/* ---------- init ---------- */

$$(".nav-item").forEach((el) => el.addEventListener("click", () => navigate(el.dataset.view)));

// auto-refresh
setInterval(loadState, 4000);
setInterval(loadLogs, 2000);

navigate("overview");
loadState();
loadLogs();