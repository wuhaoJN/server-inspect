'use strict';

/* ============================================================
 * 服务器巡检系统 - 前端单页应用（原生 JS，无构建步骤）
 * ============================================================ */

const appEl = document.getElementById('app');
const toastsEl = document.getElementById('toasts');

const state = {
  user: null,
  version: '',
  servers: [],
  route: { name: 'dashboard', id: '' },
  serverQuery: '',
  selected: new Set(),
  inspectionFilter: { serverId: '', status: '', offset: 0, limit: 20, total: 0, items: [] },
};

const STATUS = {
  ok: { text: '正常', cls: 'ok' },
  info: { text: '提示', cls: 'info' },
  warning: { text: '警告', cls: 'warning' },
  critical: { text: '严重', cls: 'critical' },
  skipped: { text: '跳过', cls: 'skipped' },
  error: { text: '失败', cls: 'critical' },
  pending: { text: '待配置', cls: 'skipped' },
  '': { text: '未巡检', cls: 'muted' },
};

/* ---------------- 工具函数 ---------------- */

const esc = (v) => String(v === null || v === undefined ? '' : v).replace(/[&<>"']/g, (c) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]));

function statusBadge(status) {
  const meta = STATUS[status] || STATUS.info;
  return `<span class="badge badge-${meta.cls}">${meta.text}</span>`;
}

function scoreClass(score, status) {
  if (status === 'error' || !score) return 'na';
  if (score >= 85) return 'good';
  if (score >= 70) return 'mid';
  return 'bad';
}

function scoreColor(score, status) {
  if (status === 'critical' || status === 'error' || score < 70) return 'var(--danger)';
  if (score < 85) return 'var(--warning)';
  return 'var(--success)';
}

function fmtTime(iso) {
  if (!iso) return '—';
  const t = new Date(iso);
  if (Number.isNaN(t.getTime()) || t.getFullYear() < 2000) return '—';
  return t.toLocaleString('zh-CN', { hour12: false });
}

function fmtRelative(iso) {
  if (!iso) return '从未巡检';
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return '—';
  const diff = (Date.now() - t.getTime()) / 1000;
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 2592000) return `${Math.floor(diff / 86400)} 天前`;
  return fmtTime(iso);
}

function fmtDuration(ms) {
  const v = Number(ms) || 0;
  if (v < 1000) return `${v} ms`;
  if (v < 60000) return `${(v / 1000).toFixed(1)} 秒`;
  return `${Math.floor(v / 60000)} 分 ${Math.round((v % 60000) / 1000)} 秒`;
}

function fmtUptime(seconds) {
  const s = Number(seconds) || 0;
  if (s <= 0) return '—';
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  return `${d ? d + ' 天 ' : ''}${d || h ? h + ' 小时 ' : ''}${m} 分`;
}

function fmtNum(v, digits) {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '—';
  return v.toFixed(digits || 0);
}

function truncate(text, n) {
  const s = String(text || '');
  return s.length > n ? s.slice(0, n) + '…' : s;
}

async function api(path, options = {}) {
  const opts = {
    method: options.method || 'GET',
    credentials: 'same-origin',
    headers: {},
  };
  if (options.body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(options.body);
  }
  if (options.signal) {
    opts.signal = options.signal;
  }
  let res;
  try {
    res = await fetch(path, opts);
  } catch (e) {
    if (e && e.name === 'AbortError') throw e;
    throw new Error('网络请求失败，请检查服务是否正在运行');
  }
  let payload = null;
  try {
    payload = await res.json();
  } catch (e) {
    payload = null;
  }
  if (res.status === 401 && !path.endsWith('/session')) {
    state.user = null;
    renderLogin('登录已过期，请重新登录');
    throw new Error('登录已过期');
  }
  if (!res.ok || !payload || payload.ok !== true) {
    throw new Error((payload && payload.error) || `请求失败（HTTP ${res.status}）`);
  }
  return payload.data;
}

/* ---------------- 提示与弹窗 ---------------- */

function toast(message, type = 'success') {
  const el = document.createElement('div');
  const icons = { success: '✓', warn: '!', error: '×', info: 'i' };
  el.className = `toast ${type}`;
  el.innerHTML = `<span class="t-icon">${icons[type] || 'i'}</span><div>${esc(message)}</div>`;
  toastsEl.appendChild(el);
  setTimeout(() => {
    el.style.opacity = '0';
    el.style.transition = 'opacity .25s';
    setTimeout(() => el.remove(), 260);
  }, 4200);
}

function openModal({ title, body, wide, size, onClose }) {
  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  const sizeClass = size === 'xl' ? 'modal-xl' : (wide ? 'modal-wide' : '');
  overlay.innerHTML = `<div class="modal ${sizeClass}" role="dialog" aria-modal="true">
    <div class="modal-head"><h3>${esc(title)}</h3><button type="button" class="icon-btn" data-close aria-label="关闭">×</button></div>
    <div class="modal-body">${body}</div>
  </div>`;
  document.body.appendChild(overlay);

  const onKey = (e) => { if (e.key === 'Escape') close(); };
  const close = () => {
    overlay.remove();
    document.removeEventListener('keydown', onKey);
    if (onClose) onClose();
  };
  document.addEventListener('keydown', onKey);
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay || e.target.closest('[data-close]')) close();
  });
  return close;
}

function openPasswordModal() {
  const body = `<form id="passForm" class="form-grid" autocomplete="off">
    <div class="field">
      <label class="label" for="pw-current">当前密码</label>
      <input class="input" id="pw-current" type="password" autocomplete="current-password" required>
    </div>
    <div class="field">
      <label class="label" for="pw-new">新密码</label>
      <input class="input" id="pw-new" type="password" autocomplete="new-password" minlength="8" required>
      <div class="help">至少 8 个字符，最多 72 字节</div>
    </div>
    <div class="field">
      <label class="label" for="pw-confirm">确认新密码</label>
      <input class="input" id="pw-confirm" type="password" autocomplete="new-password" minlength="8" required>
    </div>
    <div class="modal-foot">
      <div class="spacer"></div>
      <button type="button" class="btn btn-ghost" data-close>取消</button>
      <button type="submit" class="btn btn-primary">保存新密码</button>
    </div>
  </form>`;
  const close = openModal({ title: '修改管理员密码', body });
  const form = document.getElementById('passForm');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const current = document.getElementById('pw-current').value;
    const next = document.getElementById('pw-new').value;
    const again = document.getElementById('pw-confirm').value;
    if (next.length < 8) {
      toast('新密码至少需要 8 个字符', 'warn');
      return;
    }
    if (next !== again) {
      toast('两次输入的新密码不一致', 'warn');
      return;
    }
    const btn = form.querySelector('button[type="submit"]');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner spinner-light"></span> 保存中…';
    try {
      await api('/api/password', {
        method: 'POST',
        body: { currentPassword: current, newPassword: next },
      });
      close();
      toast('管理员密码已更新', 'success');
    } catch (err) {
      toast(err.message, 'error');
      btn.disabled = false;
      btn.textContent = '保存新密码';
    }
  });
}

function confirmAction({ title, message, confirmText = '确认删除', onConfirm }) {  const body = `<div class="confirm-text">${message}</div>
    <div class="modal-foot"><div class="spacer"></div>
      <button type="button" class="btn btn-ghost" data-close>取消</button>
      <button type="button" class="btn btn-danger" id="confirmOk">${esc(confirmText)}</button>
    </div>`;
  const close = openModal({ title, body });
  document.getElementById('confirmOk').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner spinner-light"></span> 处理中…';
    try {
      await onConfirm();
      close();
    } catch (err) {
      toast(err.message, 'error');
      btn.disabled = false;
      btn.textContent = confirmText;
    }
  });
}

/* ---------------- 登录 ---------------- */

function renderLogin(message) {
  document.title = '登录 · 服务器巡检系统';
  appEl.className = 'app';
  appEl.innerHTML = `
  <div class="login-page">
    <section class="login-brand">
      <div class="login-logo">
        <div class="login-logo-mark">⛨</div>
        <div>
          <div class="login-logo-name">服务器巡检系统</div>
          <div class="login-logo-sub">Server Inspector</div>
        </div>
      </div>
      <div class="login-hero">
        <span class="eyebrow">NON-INVASIVE · SSH</span>
        <h1>让每一台 Linux 服务器<br><em>状态尽在掌握</em></h1>
        <p>通过 SSH 只读命令采集服务器状态，无需在目标主机安装任何 Agent。自动适配 Ubuntu、CentOS、Debian、Rocky、openEuler 等发行版。</p>
        <div class="login-points">
          <div class="login-point"><span>✓</span><div><strong>非侵入式巡检</strong>　全程只读命令，不改动目标系统</div></div>
          <div class="login-point"><span>✓</span><div><strong>多发行版适配</strong>　自动识别系统并降级兼容采集命令</div></div>
          <div class="login-point"><span>✓</span><div><strong>健康评分与历史</strong>　CPU、内存、磁盘、服务、安全一屏掌握</div></div>
        </div>
      </div>
      <div class="login-foot">v${esc(state.version || '1.0.0')} · 单二进制部署 · 数据本地存储</div>
    </section>
    <section class="login-panel">
      <div class="login-card">
        <h2>管理员登录</h2>
        <p class="sub">使用启动时设置的管理员账号访问巡检中心</p>
        <form class="login-form" id="loginForm" autocomplete="on">
          <div class="login-error ${message ? 'show' : ''}" id="loginError">${esc(message || '')}</div>
          <div class="field">
            <label for="loginUser">用户名</label>
            <input class="input" id="loginUser" name="username" autocomplete="username" aria-label="用户名" value="admin" required>
          </div>
          <div class="field">
            <label for="loginPass">密码</label>
            <input class="input" id="loginPass" name="password" type="password" autocomplete="current-password" aria-label="密码" required autofocus>
          </div>
          <button class="btn btn-primary" type="submit" id="loginBtn">登录</button>
        </form>
        <p class="login-note">忘记密码？在服务器上使用 <span class="mono">-reset-admin-pass</span> 参数重启服务即可重置。</p>
      </div>
    </section>
  </div>`;

  const form = document.getElementById('loginForm');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const btn = document.getElementById('loginBtn');
    const errBox = document.getElementById('loginError');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner spinner-light"></span> 登录中…';
    errBox.classList.remove('show');
    try {
      const data = await api('/api/login', {
        method: 'POST',
        body: { username: form.elements.username.value.trim(), password: form.elements.password.value },
      });
      state.user = data.user;
      state.version = data.version || state.version;
      if (!location.hash) location.hash = '#/dashboard';
      renderShell();
      onRoute();
    } catch (err) {
      errBox.textContent = err.message;
      errBox.classList.add('show');
      btn.disabled = false;
      btn.textContent = '登录';
    }
  });
}

/* ---------------- 主框架 ---------------- */

function renderShell() {
  document.title = '服务器巡检系统';
  appEl.className = 'app';
  appEl.innerHTML = `
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-logo">⛨</div>
        <div>
          <div class="brand-name">巡检中心</div>
          <div class="brand-sub">Server Inspector</div>
        </div>
      </div>
      <nav class="nav">
        <a href="#/dashboard" data-nav="dashboard"><span>▦</span>仪表盘</a>
        <a href="#/servers" data-nav="servers"><span>▤</span>服务器</a>
        <a href="#/inspections" data-nav="inspections"><span>≣</span>巡检记录</a>
      </nav>
      <div class="sidebar-foot"><span class="dot"></span> v${esc(state.version || '1.0.0')}</div>
    </aside>
    <div class="main">
      <header class="topbar">
        <div>
          <div class="page-title" id="pageTitle">仪表盘</div>
          <div class="page-sub" id="pageSub"></div>
        </div>
        <div class="topbar-actions">
          <span class="user-chip"><span class="avatar">${esc((state.user || '?').slice(0, 1).toUpperCase())}</span>${esc(state.user || '')}</span>
          <button class="btn btn-ghost btn-sm" id="changePassBtn">修改密码</button>
          <button class="btn btn-ghost btn-sm" id="logoutBtn">退出</button>
        </div>
      </header>
      <main id="view" class="view"></main>
    </div>
  </div>`;

  document.getElementById('changePassBtn').addEventListener('click', openPasswordModal);
  document.getElementById('logoutBtn').addEventListener('click', async () => {
    try { await api('/api/logout', { method: 'POST' }); } catch (e) { /* ignore */ }
    state.user = null;
    state.servers = [];
    state.selected.clear();
    renderLogin();
  });
}

function onRoute() {
  if (!state.user) return;
  const raw = (location.hash || '').replace(/^#\/?/, '');
  const [name, id] = raw.split('/');
  const page = name || 'dashboard';
  state.route = { name: page, id: id || '' };

  document.querySelectorAll('[data-nav]').forEach((a) => {
    a.classList.toggle('active', a.dataset.nav === (page === 'inspections' ? 'inspections' : page));
  });

  const meta = (page === 'inspections' && id)
    ? ['巡检详情', '完整巡检报告']
    : {
        dashboard: ['仪表盘', '全局巡检概览与服务器健康状态'],
        servers: ['服务器管理', '纳管 Linux 服务器并执行非侵入式巡检'],
        inspections: ['巡检记录', '查看历史巡检结果与健康评分'],
      }[page] || ['巡检详情', '完整巡检报告'];
  const titleEl = document.getElementById('pageTitle');
  const subEl = document.getElementById('pageSub');
  if (titleEl) titleEl.textContent = meta[0];
  if (subEl) subEl.textContent = meta[1];

  if (page === 'servers') viewServers();
  else if (page === 'inspections' && id) viewInspection(id);
  else if (page === 'inspections') viewInspections();
  else viewDashboard();
}

/* ---------------- 仪表盘 ---------------- */

function loadingRows(count = 4) {
  return Array.from({ length: count }, () => '<div class="skeleton skeleton-row"></div>').join('');
}

async function viewDashboard() {
  const view = document.getElementById('view');
  view.innerHTML = `
    <div class="stat-grid">
      ${Array.from({ length: 4 }, () => `<div class="stat-card">
        <div class="skeleton" style="height:13px;width:45%"></div>
        <div class="skeleton" style="height:30px;width:55%;margin-top:14px"></div>
        <div class="skeleton" style="height:11px;width:70%;margin-top:12px"></div>
      </div>`).join('')}
    </div>
    <div class="dash-grid">
      <div class="card"><div class="card-head"><h3>服务器状态</h3></div><div class="card-body">${loadingRows(5)}</div></div>
      <div class="card"><div class="card-head"><h3>最近巡检</h3></div><div class="card-body">${loadingRows(5)}</div></div>
    </div>`;

  let d;
  try {
    d = await api('/api/dashboard');
  } catch (e) {
    view.innerHTML = `<div class="card error-card"><div class="error-icon">!</div><h3>加载概览失败</h3><p>${esc(e.message)}</p></div>`;
    return;
  }

  const avg = typeof d.avgScore === 'number' && d.avgScore > 0 ? d.avgScore.toFixed(1) : '—';
  const pendingCount = (d.servers || []).filter((v) => !v.configured).length;
  const cards = [
    { label: '纳管服务器', value: d.serverCount, hint: pendingCount ? `${pendingCount} 台待配置登录方式` : `${d.uninspectedCount || 0} 台尚未巡检`, cls: 'indigo', icon: '▤' },
    { label: '存在异常', value: d.issueCount, hint: '最近一次巡检为警告 / 严重 / 失败', cls: d.issueCount ? 'danger' : 'ok', icon: '!' },
    { label: '今日巡检', value: d.todayCount, hint: '今日累计执行次数', cls: 'blue', icon: '↻' },
    { label: '近 7 天平均健康分', value: avg, hint: '满分 100 分', cls: 'ok', icon: '◎' },
  ];
  const statHtml = `<div class="stat-grid">${cards.map((c) => `
    <div class="stat-card ${c.cls}">
      <div class="stat-icon">${c.icon}</div>
      <div class="stat-label">${c.label}</div>
      <div class="stat-value">${esc(c.value)}</div>
      <div class="stat-hint">${esc(c.hint)}</div>
    </div>`).join('')}</div>`;

  const serverRows = (d.servers || []).length
    ? d.servers.map((v) => {
      const displayStatus = v.configured ? v.lastStatus : 'pending';
      const hasReport = v.configured && v.lastInspectionId;
      return `
      <a class="mini-item" href="${hasReport ? `#/inspections/${v.lastInspectionId}` : '#/servers'}">
        <div class="mini-main">
          <div class="mini-title">${esc(v.name)}</div>
          <div class="mini-sub">${esc(v.username || '未配置账号')}@${esc(v.host)}:${v.port}${v.group ? ' · ' + esc(v.group) : ''}</div>
        </div>
        <div>${statusBadge(displayStatus)}</div>
        <div class="mini-side"><strong>${hasReport ? v.lastScore + ' 分' : '—'}</strong>${v.configured ? fmtRelative(v.lastAt) : '待配置登录'}</div>
      </a>`;
    }).join('')
    : `<div class="empty">还没有纳管服务器<br><br><a class="btn btn-primary" href="#/servers">＋ 添加服务器</a></div>`;

  const recentRows = (d.recent || []).length
    ? d.recent.map((r) => `
      <a class="mini-item" href="#/inspections/${r.id}">
        <div class="mini-main">
          <div class="mini-title">${esc(r.serverName)} <span class="muted mono" style="font-weight:400">#${r.id}</span></div>
          <div class="mini-sub">${fmtTime(r.startedAt)} · 耗时 ${fmtDuration(r.durationMs)}</div>
        </div>
        <div>${statusBadge(r.status)}</div>
        <div class="mini-side"><strong>${r.status === 'error' ? '—' : r.score + ' 分'}</strong>${fmtRelative(r.startedAt)}</div>
      </a>`).join('')
    : `<div class="empty">暂无巡检记录</div>`;

  const counts = d.statusCounts || {};
  const dist = [
    ['ok', '正常', 'var(--success)'],
    ['warning', '警告', 'var(--warning)'],
    ['critical', '严重', 'var(--danger)'],
    ['error', '失败', '#991b1b'],
  ];
  const distTotal = dist.reduce((sum, [k]) => sum + (counts[k] || 0), 0) || 1;
  const distHtml = dist.map(([key, label, color]) => {
    const n = counts[key] || 0;
    return `<div class="dist-row">
      <span class="muted">${label}</span>
      <div class="dist-bar"><i style="width:${(n / distTotal) * 100}%;background:${color}"></i></div>
      <span class="count">${n}</span>
    </div>`;
  }).join('');

  view.innerHTML = `
    ${statHtml}
    <div class="dash-grid">
      <div class="card">
        <div class="card-head"><h3>服务器状态</h3><a class="link" href="#/servers">全部服务器 →</a></div>
        <div class="mini-list">${serverRows}</div>
      </div>
      <div>
        <div class="card">
          <div class="card-head"><h3>近 7 天巡检分布</h3></div>
          <div class="card-body"><div class="dist-list">${distHtml}</div></div>
        </div>
        <div class="card" style="margin-top:16px">
          <div class="card-head"><h3>最近巡检</h3><a class="link" href="#/inspections">全部记录 →</a></div>
          <div class="mini-list">${recentRows}</div>
        </div>
      </div>
    </div>`;
}

/* ---------------- 服务器管理 ---------------- */

async function viewServers() {
  const view = document.getElementById('view');
  view.innerHTML = `
    <div class="toolbar">
      <input class="input" id="serverSearch" placeholder="搜索名称 / 主机 / 分组 / 标签" value="${esc(state.serverQuery)}">
      <div class="toolbar-right">
        <div class="batch-progress" id="batchProgress">
          <span class="spinner"></span><span id="batchText">0 / 0</span>
          <div class="bar"><i id="batchBar" style="width:0"></i></div>
        </div>
        <button class="btn" data-action="scan-network">网络扫描</button>
        <button class="btn" data-action="batch-inspect">批量巡检</button>
        <button class="btn btn-primary" data-action="add-server">＋ 添加服务器</button>
      </div>
    </div>
    <div class="card table-wrap" id="serversTableWrap"><div class="card-body">${loadingRows(6)}</div></div>`;

  document.getElementById('serverSearch').addEventListener('input', (e) => {
    state.serverQuery = e.target.value;
    renderServersTable();
  });

  await loadServers();
  renderServersTable();
  updateBatchUI();
}

async function loadServers() {
  try {
    state.servers = (await api('/api/servers')) || [];
  } catch (e) {
    state.servers = [];
    toast(e.message, 'error');
  }
}

function filteredServers() {
  const q = state.serverQuery.trim().toLowerCase();
  if (!q) return state.servers;
  return state.servers.filter((v) =>
    [v.name, v.host, v.group, v.tags, v.username, v.note].some((x) => (x || '').toLowerCase().includes(q)));
}

function renderServersTable() {
  const wrap = document.getElementById('serversTableWrap');
  if (!wrap) return;

  if (!state.servers.length) {
    wrap.innerHTML = `<div class="empty">暂未添加服务器<br><br><button class="btn btn-primary" data-action="add-server">＋ 添加第一台服务器</button></div>`;
    updateBatchUI();
    return;
  }
  const list = filteredServers();
  if (!list.length) {
    wrap.innerHTML = `<div class="empty">没有匹配「${esc(state.serverQuery)}」的服务器</div>`;
    return;
  }

  const allSelected = list.filter((v) => v.configured);
  const allChecked = allSelected.length > 0 && allSelected.every((v) => state.selected.has(v.id));
  const rows = list.map((v) => {
    const displayStatus = v.configured ? v.lastStatus : 'pending';
    const hasReport = v.configured && v.lastInspectionId;
    const score = hasReport
      ? `<span class="score-pill ${scoreClass(v.lastScore, v.lastStatus)}">${v.lastScore}</span>`
      : '<span class="muted">—</span>';
    const lastSeen = !v.configured ? '待配置登录' : (v.lastAt ? fmtRelative(v.lastAt) : '从未巡检');
    return `<tr>
      <td class="check-cell"><input type="checkbox" class="row-check" value="${v.id}" ${state.selected.has(v.id) ? 'checked' : ''} ${v.configured ? '' : 'disabled title="请先配置登录方式"'} aria-label="选择 ${esc(v.name)}"></td>
      <td>
        ${hasReport
          ? `<a class="cell-main" href="#/inspections/${v.lastInspectionId}">${esc(v.name)}</a>`
          : `<div class="cell-main">${esc(v.name)}</div>`}
        <div class="cell-sub">${esc(v.username || '未配置账号')}@${esc(v.host)}:${v.port}</div>
      </td>
      <td>${v.group ? `<span class="chip"><i style="background:var(--primary)"></i>${esc(v.group)}</span>` : '<span class="muted">—</span>'}</td>
      <td>${statusBadge(displayStatus)}</td>
      <td>${score}</td>
      <td class="muted nowrap">${lastSeen}</td>
      <td class="right">
        <div class="row-actions">
          ${v.configured
            ? `<button class="btn btn-xs btn-primary" data-action="inspect" data-id="${v.id}">巡检</button>
               <button class="btn btn-xs" data-action="edit-server" data-id="${v.id}">编辑</button>`
            : `<button class="btn btn-xs btn-primary" data-action="edit-server" data-id="${v.id}">配置登录</button>`}
          <button class="btn btn-xs btn-danger-ghost" data-action="delete-server" data-id="${v.id}">删除</button>
        </div>
      </td>
    </tr>`;
  }).join('');

  wrap.innerHTML = `<table class="data-table">
    <thead><tr>
      <th class="check-cell"><input type="checkbox" id="selectAll" ${allChecked ? 'checked' : ''} aria-label="全选"></th>
      <th>服务器</th>
      <th>分组</th>
      <th>最近巡检</th>
      <th>健康分</th>
      <th>巡检时间</th>
      <th class="right">操作</th>
    </tr></thead>
    <tbody>${rows}</tbody>
  </table>`;
  updateBatchUI();
}

function updateBatchUI() {
  const btn = document.querySelector('[data-action="batch-inspect"]');
  if (!btn) return;
  const n = state.selected.size;
  btn.textContent = n ? `批量巡检 (${n})` : '批量巡检';
}

function parsePortList(value) {
  const raw = String(value || '').trim();
  if (!raw) return [22];
  const parts = raw.split(/[\s,，;；]+/).filter(Boolean);
  if (!parts.length) return [22];
  const out = [];
  for (const part of parts) {
    const port = Number(part);
    if (!Number.isInteger(port) || port < 1 || port > 65535) return null;
    if (!out.includes(port)) out.push(port);
  }
  if (out.length > 16) return null;
  return out;
}

function openScanModal() {
  const body = `
  <form id="scanForm" class="form-grid" autocomplete="off">
    <div class="form-row">
      <div class="field">
        <label class="label" for="scanCidr">扫描网段（CIDR）<span class="req">*</span></label>
        <input class="input" id="scanCidr" placeholder="192.168.1.0/24" required>
        <div class="help">支持 192.168.1.0/24 或单个 IP，单次最多扫描 4096 个地址</div>
      </div>
      <div class="field">
        <label class="label" for="scanPorts">扫描端口</label>
        <input class="input" id="scanPorts" value="22" placeholder="22 或 22,2222">
        <div class="help">多个端口用逗号分隔，最多 16 个</div>
      </div>
    </div>
    <div class="form-row">
      <div class="field">
        <label class="label" for="scanGroup">加入后的分组（可选）</label>
        <input class="input" id="scanGroup" maxlength="50" placeholder="例如：扫描发现">
      </div>
      <div class="field">
        <label class="label" for="scanTags">加入后的标签（可选）</label>
        <input class="input" id="scanTags" maxlength="200" placeholder="例如：auto,内网">
      </div>
    </div>
    <div class="scan-tip">扫描仅进行 TCP 连接探测并读取 SSH 服务标识，不会尝试登录或修改目标系统。请仅扫描已获授权的网段。</div>
    <div class="modal-foot">
      <button type="submit" class="btn btn-primary" id="scanBtn">开始扫描</button>
      <div class="spacer"></div>
      <button type="button" class="btn btn-ghost" data-close>关闭</button>
    </div>
    <div id="scanStatus"></div>
    <div id="scanResults"></div>
  </form>`;

  let scanController = null;
  const close = openModal({
    title: '网络扫描',
    body,
    size: 'xl',
    onClose: () => {
      if (scanController) {
        scanController.abort();
        scanController = null;
      }
    },
  });
  const form = document.getElementById('scanForm');
  const statusBox = document.getElementById('scanStatus');
  const resultsBox = document.getElementById('scanResults');
  const scanState = { hosts: [], selected: new Set(), result: null, scanning: false };

  const portForHost = (h) => (h.ports && h.ports.includes(22) ? 22 : (h.ports && h.ports[0]) || 22);
  const keyForHost = (h) => `${h.ip}:${portForHost(h)}`;
  const isDuplicate = (h) => state.servers.some((v) =>
    String(v.host).toLowerCase() === String(h.ip).toLowerCase() && Number(v.port) === portForHost(h));
  const canAdd = (h) => !!h.ssh && !isDuplicate(h);

  const renderResults = () => {
    const hosts = scanState.hosts;
    if (!hosts.length) {
      resultsBox.innerHTML = '<div class="empty">未发现开放端口的主机</div>';
      return;
    }
    const selectable = hosts.filter(canAdd);
    const allChecked = selectable.length > 0 && selectable.every((h) => scanState.selected.has(keyForHost(h)));

    const rows = hosts.map((h) => {
      const key = keyForHost(h);
      const port = portForHost(h);
      const duplicate = isDuplicate(h);
      const available = canAdd(h);
      const checked = scanState.selected.has(key);
      let tag = '<span class="badge badge-ok">可添加</span>';
      if (duplicate) tag = '<span class="badge badge-skipped">已在列表</span>';
      else if (!h.ssh) tag = '<span class="badge badge-warning">未检测到 SSH</span>';
      const hostname = h.hostname ? esc(h.hostname) : '<span class="muted">无反向解析</span>';
      return `<tr>
        <td class="check-cell"><input type="checkbox" class="scan-check" data-key="${esc(key)}" ${checked ? 'checked' : ''} ${available ? '' : 'disabled'} aria-label="选择 ${esc(h.ip)}"></td>
        <td>
          <div class="cell-main mono">${esc(h.ip)}</div>
          <div class="cell-sub">${hostname}</div>
        </td>
        <td>${(h.ports || []).map((p) => `<span class="chip scan-port">${p}${p === port ? ' · 待加入' : ''}</span>`).join(' ')}</td>
        <td class="scan-banner">${h.sshBanner ? esc(h.sshBanner) : '<span class="muted">—</span>'}</td>
        <td>${tag}</td>
      </tr>`;
    }).join('');

    resultsBox.innerHTML = `
      <div class="scan-result-head">
        <label class="scan-select-all"><input type="checkbox" id="scanSelectAll" ${allChecked ? 'checked' : ''} ${selectable.length ? '' : 'disabled'}> 全选可添加主机（${selectable.length}）</label>
        <span class="muted">已选 ${scanState.selected.size} 台</span>
      </div>
      <div class="table-wrap scan-table-wrap">
        <table class="data-table scan-table">
          <thead><tr><th class="check-cell"></th><th>IP / 主机名</th><th>开放端口</th><th>SSH 标识</th><th>状态</th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
      <div class="modal-foot">
        <span class="muted scan-hint">加入后需逐台配置登录用户名与密码</span>
        <div class="spacer"></div>
        <button type="button" class="btn btn-primary" id="addScannedBtn" ${scanState.selected.size ? '' : 'disabled'}>加入服务器列表 (${scanState.selected.size})</button>
      </div>`;

    resultsBox.querySelectorAll('.scan-check').forEach((cb) => {
      cb.addEventListener('change', () => {
        const key = cb.dataset.key;
        if (cb.checked) scanState.selected.add(key);
        else scanState.selected.delete(key);
        renderResults();
      });
    });

    const selectAll = document.getElementById('scanSelectAll');
    if (selectAll) {
      selectAll.addEventListener('change', () => {
        if (selectAll.checked) selectable.forEach((h) => scanState.selected.add(keyForHost(h)));
        else scanState.selected.clear();
        renderResults();
      });
    }

    const addBtn = document.getElementById('addScannedBtn');
    if (addBtn) addBtn.addEventListener('click', importSelected);
  };

  const importSelected = async () => {
    const selectedHosts = scanState.hosts.filter((h) => scanState.selected.has(keyForHost(h)));
    if (!selectedHosts.length) return;
    const group = document.getElementById('scanGroup').value.trim();
    const tags = document.getElementById('scanTags').value.trim();
    const servers = selectedHosts.map((h) => ({
      name: h.hostname || h.ip,
      host: h.ip,
      port: portForHost(h),
    }));

    const addBtn = document.getElementById('addScannedBtn');
    if (addBtn) {
      addBtn.disabled = true;
      addBtn.innerHTML = '<span class="spinner spinner-light"></span> 正在加入…';
    }
    try {
      const data = await api('/api/servers/bulk', { method: 'POST', body: { servers, group, tags } });
      close();
      const skipped = (data.skipped || []).length;
      toast(`已加入 ${data.created} 台服务器${skipped ? `，跳过 ${skipped} 台重复主机` : ''}`,
        data.created ? 'success' : 'warn');
      await loadServers();
      renderServersTable();
      const first = data.firstServer;
      if (first) {
        const srv = state.servers.find((v) => v.id === first.id);
        if (srv) {
          toast('请配置该服务器的登录方式', 'info');
          openServerForm(srv);
        }
      }
    } catch (err) {
      toast(err.message, 'error');
      renderResults();
    }
  };

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (scanState.scanning) return;
    const cidr = document.getElementById('scanCidr').value.trim();
    const ports = parsePortList(document.getElementById('scanPorts').value);
    if (!cidr) {
      toast('请输入扫描网段', 'warn');
      return;
    }
    if (!ports) {
      toast('端口格式不正确，例如：22 或 22,2222', 'warn');
      return;
    }

    scanState.scanning = true;
    scanState.hosts = [];
    scanState.selected.clear();
    scanState.result = null;
    resultsBox.innerHTML = '';
    const scanBtn = document.getElementById('scanBtn');
    scanBtn.disabled = true;
    scanBtn.innerHTML = '<span class="spinner spinner-light"></span> 扫描中…';
    statusBox.innerHTML = `<div class="scan-status"><span class="spinner"></span> 正在扫描 ${esc(cidr)}（端口 ${ports.join(', ')}），请稍候…</div>`;

    try {
      scanController = new AbortController();
      const requestController = scanController;
      const data = await api('/api/scan', {
        method: 'POST',
        body: { cidr, ports },
        signal: requestController.signal,
      });
      scanState.result = data;
      scanState.hosts = data.hosts || [];
      scanState.hosts.filter(canAdd).forEach((h) => scanState.selected.add(keyForHost(h)));
      const addable = scanState.hosts.filter(canAdd).length;
      statusBox.innerHTML = `<div class="scan-status">共扫描 ${data.scanned} 个地址，发现 <strong>${scanState.hosts.length}</strong> 台开放主机，其中 <strong>${addable}</strong> 台可加入，耗时 ${(data.durationMs / 1000).toFixed(1)} 秒。</div>`;
      renderResults();
    } catch (err) {
      if (err && err.name === 'AbortError') return;
      statusBox.innerHTML = `<div class="scan-error">${esc(err.message)}</div>`;
    } finally {
      scanController = null;
      scanState.scanning = false;
      scanBtn.disabled = false;
      scanBtn.textContent = '开始扫描';
    }
  });
}

function openServerForm(server) {
  const isEdit = !!server;
  const needsCredential = !isEdit || !server.configured;
  const submitText = needsCredential ? (isEdit ? '保存登录配置' : '添加服务器') : '保存修改';
  const body = `
  <form id="serverForm" class="form-grid" autocomplete="off">
    ${isEdit && !server.configured ? '<div class="form-note">该服务器由网络扫描加入，请填写登录用户名与密码，保存后即可执行巡检。</div>' : ''}
    <div class="form-row">
      <div class="field">
        <label class="label" for="sf-name">服务器名称<span class="req">*</span></label>
        <input class="input" id="sf-name" name="name" value="${esc(server ? server.name : '')}" placeholder="例如：生产 Web-01" required maxlength="100">
      </div>
      <div class="field">
        <label class="label" for="sf-group">分组</label>
        <input class="input" id="sf-group" name="group" value="${esc(server ? server.group : '')}" placeholder="例如：生产环境" maxlength="50">
      </div>
    </div>
    <div class="form-row">
      <div class="field">
        <label class="label" for="sf-host">主机地址<span class="req">*</span></label>
        <input class="input" id="sf-host" name="host" value="${esc(server ? server.host : '')}" placeholder="192.168.1.10 或 host.example.com" required maxlength="255">
      </div>
      <div class="field">
        <label class="label" for="sf-port">SSH 端口</label>
        <input class="input" id="sf-port" name="port" type="number" min="1" max="65535" value="${server ? server.port : 22}">
      </div>
    </div>
    <div class="form-row">
      <div class="field">
        <label class="label" for="sf-username">登录用户名<span class="req">*</span></label>
        <input class="input" id="sf-username" name="username" value="${esc(server ? server.username : 'root')}" placeholder="root" required maxlength="64">
      </div>
      <div class="field">
        <label class="label" for="sf-password">登录密码${needsCredential ? '<span class="req">*</span>' : ''}</label>
        <div class="password-wrap">
          <input class="input" id="sf-password" name="password" type="password" placeholder="${needsCredential ? '请输入 SSH 登录密码' : '留空则保持原密码'}" ${needsCredential ? 'required' : ''} autocomplete="new-password">
          <button type="button" class="password-toggle" id="toggleServerPassword" aria-label="显示密码" aria-pressed="false">显示</button>
        </div>
      </div>
    </div>
    <div class="form-row">
      <div class="field">
        <label class="label" for="sf-tags">标签</label>
        <input class="input" id="sf-tags" name="tags" value="${esc(server ? server.tags : '')}" placeholder="以逗号分隔，例如：nginx,核心" maxlength="200">
      </div>
      <div class="field">
        <label class="label" for="sf-fp">主机密钥指纹</label>
        <input class="input mono" id="sf-fp" value="${esc(server && server.hostKey ? server.hostKey : '首次连接时自动记录')}" readonly>
      </div>
    </div>
    <div class="field">
      <label class="label" for="sf-note">备注</label>
      <textarea class="textarea" id="sf-note" name="note" maxlength="500" placeholder="可选，记录用途或负责人">${esc(server ? server.note : '')}</textarea>
    </div>
    ${isEdit ? `<label class="field" style="display:flex;gap:8px;align-items:center;font-size:12.5px;color:#374151">
      <input type="checkbox" name="resetHostKey" style="width:15px;height:15px;accent-color:var(--primary)">
      重新信任主机密钥（服务器重装或更换后使用）
    </label>` : ''}
    <div class="test-result" id="testResult"></div>
    <div class="modal-foot">
      <button type="button" class="btn" id="testBtn">测试连接</button>
      <div class="spacer"></div>
      <button type="button" class="btn btn-ghost" data-close>取消</button>
      <button type="submit" class="btn btn-primary">${submitText}</button>
    </div>
  </form>`;

  const close = openModal({ title: isEdit ? (needsCredential ? '配置登录方式' : '编辑服务器') : '添加服务器', body, wide: true });
  const form = document.getElementById('serverForm');
  const result = document.getElementById('testResult');

  const passwordInput = document.getElementById('sf-password');
  const passwordToggle = document.getElementById('toggleServerPassword');
  if (passwordInput && passwordToggle) {
    passwordToggle.addEventListener('click', () => {
      const visible = passwordInput.type === 'text';
      passwordInput.type = visible ? 'password' : 'text';
      passwordToggle.textContent = visible ? '显示' : '隐藏';
      passwordToggle.setAttribute('aria-label', visible ? '显示密码' : '隐藏密码');
      passwordToggle.setAttribute('aria-pressed', visible ? 'false' : 'true');
      passwordInput.focus();
    });
  }

  const collect = () => ({
    name: form.elements.name.value.trim(),
    host: form.elements.host.value.trim(),
    port: Number(form.elements.port.value) || 22,
    username: form.elements.username.value.trim(),
    password: form.elements.password.value,
    group: form.elements.group.value.trim(),
    tags: form.elements.tags.value.trim(),
    note: form.elements.note.value.trim(),
    serverId: server ? server.id : 0,
    resetHostKey: !!(form.elements.resetHostKey && form.elements.resetHostKey.checked),
  });

  const hostInput = form.elements.host;
  const portInput = form.elements.port;
  const targetChanged = () => isEdit && server.configured &&
    (hostInput.value.trim() !== server.host || (Number(portInput.value) || 22) !== server.port);
  const syncPasswordPlaceholder = () => {
    if (!passwordInput) return;
    if (targetChanged()) {
      passwordInput.placeholder = '主机/端口已修改，请输入新的登录密码';
    } else {
      passwordInput.placeholder = needsCredential ? '请输入 SSH 登录密码' : '留空则保持原密码';
    }
  };
  hostInput.addEventListener('input', syncPasswordPlaceholder);
  portInput.addEventListener('input', syncPasswordPlaceholder);
  syncPasswordPlaceholder();

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const payload = collect();
    if (isEdit && server.configured && !payload.password &&
      (payload.host !== server.host || payload.port !== server.port)) {
      toast('修改了主机地址或端口，请重新输入登录密码', 'warn');
      passwordInput.focus();
      return;
    }
    const btn = form.querySelector('button[type="submit"]');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner spinner-light"></span> 保存中…';
    try {
      if (isEdit) await api(`/api/servers/${server.id}`, { method: 'PUT', body: payload });
      else await api('/api/servers', { method: 'POST', body: payload });
      close();
      toast(isEdit
        ? (needsCredential ? '登录方式已配置，可执行巡检' : '服务器信息已更新')
        : '服务器已添加', 'success');
      if (state.route.name === 'servers') {
        await loadServers();
        renderServersTable();
      } else {
        onRoute();
      }
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
      btn.textContent = submitText;
    }
  });

  document.getElementById('testBtn').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const payload = collect();
    if (isEdit && server.configured && !payload.password &&
      (payload.host !== server.host || payload.port !== server.port)) {
      toast('修改了主机地址或端口，请重新输入登录密码', 'warn');
      passwordInput.focus();
      return;
    }
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span> 测试中…';
    result.className = 'test-result';
    try {
      const data = await api('/api/servers/test', { method: 'POST', body: payload });
      result.className = 'test-result show ok';
      result.innerHTML = `<div class="tr-title">✓ 连接成功 <span class="muted">${data.latencyMs} ms</span></div>
        <div class="tr-meta">主机指纹：<span class="mono">${esc(data.fingerprint)}</span>${data.hostKeySaved ? ' · 已保存' : ''}</div>
        ${data.output ? `<pre>${esc(data.output)}</pre>` : ''}`;
    } catch (err) {
      result.className = 'test-result show fail';
      result.innerHTML = `<div class="tr-title">✕ 连接失败</div>
        <div class="tr-meta">${esc(err.message)}</div>
        ${isEdit && /指纹/.test(err.message) ? `<button type="button" class="btn btn-sm" data-action="trust-server" data-id="${server.id}">重新信任主机密钥</button>` : ''}`;
    } finally {
      btn.disabled = false;
      btn.textContent = '测试连接';
    }
  });
}

async function trustServer(id, btn) {
  if (btn) btn.disabled = true;
  try {
    const data = await api(`/api/servers/${id}/trust`, { method: 'POST' });
    toast('已更新主机密钥指纹，请重新测试连接', 'success');
    if (btn) btn.remove();
    if (state.route.name === 'servers') {
      await loadServers();
      renderServersTable();
    }
  } catch (err) {
    toast(err.message, 'error');
    if (btn) btn.disabled = false;
  }
}

/* ---------------- 巡检操作 ---------------- */

async function inspectServer(id, btn) {
  if (btn) {
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span>';
  }
  try {
    const data = await api(`/api/servers/${id}/inspect`, { method: 'POST' });
    const rec = data.inspection;
    const meta = STATUS[rec.status] || { text: rec.status };
    toast(`${rec.serverName} 巡检完成：${meta.text}${rec.status !== 'error' ? ` · ${rec.score} 分` : ''}`,
      rec.status === 'ok' ? 'success' : (rec.status === 'error' ? 'error' : 'warn'));
    const target = `#/inspections/${rec.id}`;
    if (location.hash === target) onRoute();
    else location.hash = target;
  } catch (err) {
    toast(err.message, 'error');
    if (btn) {
      btn.disabled = false;
      btn.textContent = '巡检';
    }
  }
}

async function batchInspect(btn) {
  const selectedIDs = [...state.selected].filter((id) => state.servers.some((v) => v.id === id));
  const ids = selectedIDs.filter((id) => {
    const srv = state.servers.find((v) => v.id === id);
    return srv && srv.configured;
  });
  const skipped = selectedIDs.length - ids.length;
  if (skipped > 0) {
    toast(`已跳过 ${skipped} 台未配置登录方式的服务器`, 'warn');
  }
  if (!ids.length) {
    toast('请先勾选已配置登录方式的服务器', 'warn');
    if (btn) btn.disabled = false;
    return;
  }
  if (btn) btn.disabled = true;
  const box = document.getElementById('batchProgress');
  const text = document.getElementById('batchText');
  const bar = document.getElementById('batchBar');
  if (box) box.classList.add('show');

  let done = 0;
  let ok = 0;
  let fail = 0;
  let cursor = 0;
  const update = () => {
    if (text) text.textContent = `${done} / ${ids.length}`;
    if (bar) bar.style.width = `${(done / ids.length) * 100}%`;
  };
  update();

  const worker = async () => {
    while (cursor < ids.length) {
      const id = ids[cursor++];
      try {
        const data = await api(`/api/servers/${id}/inspect`, { method: 'POST' });
        if (data.inspection.status === 'error') fail += 1;
        else ok += 1;
      } catch (e) {
        fail += 1;
      }
      done += 1;
      update();
    }
  };
  await Promise.all(Array.from({ length: Math.min(3, ids.length) }, worker));

  if (box) box.classList.remove('show');
  if (btn) btn.disabled = false;
  toast(`批量巡检完成：成功 ${ok} 台${fail ? `，失败 ${fail} 台` : ''}`, fail ? 'warn' : 'success');
  state.selected.clear();
  await loadServers();
  renderServersTable();
}

/* ---------------- 巡检记录列表 ---------------- */

async function viewInspections() {
  if (!state.servers.length) await loadServers();
  state.inspectionFilter = { serverId: '', status: '', offset: 0, limit: 20, total: 0, items: [] };

  const view = document.getElementById('view');
  view.innerHTML = `
    <div class="toolbar">
      <select class="select" id="filterServer">
        <option value="">全部服务器</option>
        ${state.servers.map((v) => `<option value="${v.id}">${esc(v.name)}</option>`).join('')}
      </select>
      <select class="select" id="filterStatus">
        <option value="">全部状态</option>
        <option value="ok">正常</option>
        <option value="warning">警告</option>
        <option value="critical">严重</option>
        <option value="error">失败</option>
      </select>
      <div class="toolbar-right">
        <button class="btn" data-action="refresh-inspections">刷新</button>
        <button class="btn btn-primary" data-action="goto-servers">执行巡检</button>
      </div>
    </div>
    <div class="card table-wrap">
      <table class="data-table">
        <thead><tr>
          <th>记录</th><th>服务器</th><th>状态</th><th>健康分</th><th>巡检时间</th><th>耗时</th><th class="right">操作</th>
        </tr></thead>
        <tbody id="inspectionRows">
          <tr><td colspan="7"><div class="view-loading"><span class="spinner"></span>正在加载巡检记录…</div></td></tr>
        </tbody>
      </table>
      <div class="pager" id="inspectionPager"></div>
    </div>`;

  document.getElementById('filterServer').addEventListener('change', (e) => {
    state.inspectionFilter.serverId = e.target.value;
    state.inspectionFilter.offset = 0;
    loadInspections(true);
  });
  document.getElementById('filterStatus').addEventListener('change', (e) => {
    state.inspectionFilter.status = e.target.value;
    state.inspectionFilter.offset = 0;
    loadInspections(true);
  });

  await loadInspections(true);
}

async function loadInspections(reset) {
  const f = state.inspectionFilter;
  const tbody = document.getElementById('inspectionRows');
  if (!tbody) return;
  if (reset) {
    f.offset = 0;
    tbody.innerHTML = `<tr><td colspan="7"><div class="view-loading"><span class="spinner"></span>正在加载巡检记录…</div></td></tr>`;
  }

  const qs = new URLSearchParams({ limit: String(f.limit), offset: String(f.offset) });
  if (f.serverId) qs.set('serverId', f.serverId);
  if (f.status) qs.set('status', f.status);

  try {
    const data = await api(`/api/inspections?${qs.toString()}`);
    f.total = data.total || 0;
    f.items = reset ? (data.items || []) : (f.items || []).concat(data.items || []);
    renderInspectionRows();
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="7"><div class="empty">${esc(e.message)}</div></td></tr>`;
  }
}

function renderInspectionRows() {
  const f = state.inspectionFilter;
  const tbody = document.getElementById('inspectionRows');
  if (!tbody) return;

  tbody.innerHTML = (f.items || []).length
    ? f.items.map((r) => `<tr>
        <td class="mono muted nowrap">#${r.id}</td>
        <td>
          <div class="cell-main">${esc(r.serverName)}</div>
          <div class="cell-sub">${esc(r.host)}</div>
          ${r.status === 'error' && r.error ? `<div class="cell-sub" title="${esc(r.error)}">${esc(truncate(r.error, 42))}</div>` : ''}
        </td>
        <td>${statusBadge(r.status)}</td>
        <td>${r.status === 'error' ? '<span class="muted">—</span>' : `<span class="score-pill ${scoreClass(r.score, r.status)}">${r.score}</span>`}</td>
        <td class="muted nowrap">${fmtTime(r.startedAt)}</td>
        <td class="muted nowrap">${fmtDuration(r.durationMs)}</td>
        <td class="right">
          <div class="row-actions">
            <button class="btn btn-xs" data-action="open-inspection" data-id="${r.id}">查看</button>
            <button class="btn btn-xs btn-danger-ghost" data-action="delete-inspection" data-id="${r.id}">删除</button>
          </div>
        </td>
      </tr>`).join('')
    : `<tr><td colspan="7"><div class="empty">暂无巡检记录<br><br><a class="btn btn-primary" href="#/servers">前往服务器列表执行巡检</a></div></td></tr>`;

  const pager = document.getElementById('inspectionPager');
  if (!pager) return;
  if ((f.items || []).length < f.total) {
    pager.innerHTML = `<button class="btn" id="loadMore">加载更多（${f.items.length} / ${f.total}）</button>`;
    const more = document.getElementById('loadMore');
    more.addEventListener('click', async (e) => {
      e.currentTarget.disabled = true;
      f.offset = f.items.length;
      await loadInspections(false);
    });
  } else {
    pager.innerHTML = f.total ? `<span class="muted" style="font-size:12px">共 ${f.total} 条记录</span>` : '';
  }
}

/* ---------------- 巡检详情 ---------------- */

async function viewInspection(id) {
  const view = document.getElementById('view');
  view.innerHTML = '<div class="view-loading"><span class="spinner"></span>正在加载巡检报告…</div>';

  let data;
  try {
    data = await api(`/api/inspections/${id}`);
  } catch (e) {
    view.innerHTML = `<div class="card error-card"><div class="error-icon">!</div><h3>无法加载巡检记录</h3><p>${esc(e.message)}</p><button class="btn" data-action="back-inspections">返回巡检记录</button></div>`;
    return;
  }

  const ins = data.inspection;
  const rep = data.report;
  const titleEl = document.getElementById('pageTitle');
  if (titleEl) titleEl.textContent = '巡检详情';
  const subEl = document.getElementById('pageSub');
  if (subEl) subEl.textContent = `${ins.serverName} · ${fmtTime(ins.startedAt)}`;

  if (!rep) {
    view.innerHTML = `
      <div class="report-head">
        <div>
          <div class="report-title">${esc(ins.serverName)}<span class="muted">${esc(ins.host)}</span></div>
          <div class="report-meta">开始于 ${fmtTime(ins.startedAt)} · 耗时 ${fmtDuration(ins.durationMs)}</div>
        </div>
        <div class="report-actions">
          <button class="btn btn-ghost" data-action="back-inspections">返回记录</button>
        </div>
      </div>
      <div class="card error-card">
        <div class="error-icon">!</div>
        <h3>本次巡检未能完成</h3>
        <p>${esc(ins.error || '未知错误')}</p>
        <button class="btn btn-primary" data-action="inspect" data-id="${ins.serverId}">重新巡检</button>
      </div>`;
    return;
  }

  const summary = rep.summary || {};
  const m = rep.metrics || {};
  const totalItems = (summary.ok || 0) + (summary.info || 0) + (summary.warning || 0) + (summary.critical || 0) + (summary.skipped || 0);

  const ring = `<div class="score-ring" style="--value:${Number(rep.score) || 0};--ring:${scoreColor(rep.score, rep.overall)}">
      <div class="score-inner"><strong>${Number(rep.score) || 0}</strong><span>健康分</span></div>
    </div>`;

  const chips = [
    ['ok', 'var(--success)', summary.ok || 0],
    ['warning', 'var(--warning)', summary.warning || 0],
    ['critical', 'var(--danger)', summary.critical || 0],
    ['skipped', 'var(--muted-2)', summary.skipped || 0],
  ].map(([key, color, n]) => `<span class="chip"><i style="background:${color}"></i>${STATUS[key].text} <b>${n}</b></span>`).join('');

  const warnings = (rep.warnings || []).length
    ? `<div class="warn-box"><h4>需要关注（${rep.warnings.length}）</h4><ul>${rep.warnings.map((w) => `<li>${esc(w)}</li>`).join('')}</ul></div>`
    : '';

  const head = `
    <div class="report-head">
      <div>
        <div class="report-title">${esc(rep.serverName || ins.serverName)}<span class="muted">${esc(rep.host || ins.host)}</span></div>
        <div class="report-meta">${esc(rep.detectedOs || '未知系统')}${rep.osFamily ? ` · ${esc(rep.osFamily)} 系` : ''} · 内核 ${esc(rep.kernel || '—')}${rep.arch ? ` · ${esc(rep.arch)}` : ''} · 运行 ${fmtUptime(rep.uptimeSeconds)}</div>
        <div class="report-meta">巡检 #${ins.id} · 开始于 ${fmtTime(rep.startedAt || ins.startedAt)} · 耗时 ${fmtDuration(rep.durationMs || ins.durationMs)}</div>
      </div>
      <div class="report-actions">
        <button class="btn btn-ghost" data-action="back-inspections">返回记录</button>
        <button class="btn btn-primary" data-action="inspect" data-id="${ins.serverId}">重新巡检</button>
        <button class="btn btn-danger-ghost" data-action="delete-inspection" data-id="${ins.id}">删除记录</button>
      </div>
    </div>`;

  const summaryHtml = `
    <div class="report-summary">
      ${ring}
      <div class="summary-side">
        <div class="overall-line">${statusBadge(rep.overall)}<span>共完成 ${totalItems} 项检查，其中提示 ${summary.info || 0} 项</span></div>
        <div class="summary-chips">${chips}</div>
        ${warnings}
      </div>
    </div>`;

  const metrics = [
    { label: 'CPU 使用率', value: `${fmtNum(m.cpuPercent, 1)}%`, sub: `${m.cpuCores || '—'} 个逻辑核心` },
    { label: '系统负载（1 分钟）', value: fmtNum(m.load1, 2), sub: `5 分钟 ${fmtNum(m.load5, 2)} · 15 分钟 ${fmtNum(m.load15, 2)}` },
    { label: '内存使用率', value: `${fmtNum(m.memPercent, 1)}%`, sub: `交换分区 ${fmtNum(m.swapPercent, 1)}%` },
    { label: '磁盘最高使用率', value: `${fmtNum(m.diskPercent, 1)}%`, sub: `inode 最高 ${fmtNum(m.inodePercent, 1)}%` },
    { label: '进程数', value: fmtNum(m.processCount), sub: '包含全部用户进程' },
    { label: '监听端口', value: fmtNum(m.listenPorts), sub: 'TCP / UDP 监听' },
    { label: 'TCP 已建立连接', value: fmtNum(m.established), sub: '当前 established 状态' },
    { label: '系统运行时长', value: fmtUptime(rep.uptimeSeconds), sub: `自 ${fmtRelative(rep.startedAt)} 巡检` },
  ];
  const metricHtml = `<div class="metric-grid">${metrics.map((c) => `
    <div class="metric-card">
      <div class="metric-label">${c.label}</div>
      <div class="metric-value">${esc(c.value)}</div>
      <div class="metric-sub">${esc(c.sub)}</div>
    </div>`).join('')}</div>`;

  const sectionsHtml = (rep.sections || []).map((sec) => {
    const items = sec.items || [];
    const rows = items.map((it) => {
      const st = it.status || 'info';
      return `<tr class="st-${st}">
        <td><div class="item-label"><span class="status-dot ${st}"></span><span>${esc(it.label)}</span></div></td>
        <td class="item-value ${st === 'skipped' ? 'is-skipped' : ''}">${esc(it.value)}</td>
        <td class="item-detail">${it.detail ? esc(it.detail) : '<span class="muted">—</span>'}</td>
      </tr>`;
    }).join('');
    return `<section class="card section-card">
      <div class="section-head"><h3>${esc(sec.title)}</h3><span class="count">${items.length} 项</span></div>
      <table class="items-table">
        <colgroup><col class="c-label"><col class="c-value"><col></colgroup>
        <tbody>${rows}</tbody>
      </table>
    </section>`;
  }).join('');

  view.innerHTML = head + summaryHtml + metricHtml + sectionsHtml;
}

/* ---------------- 全局交互 ---------------- */

document.addEventListener('change', (event) => {
  const target = event.target;
  if (target.id === 'selectAll') {
    const list = filteredServers().filter((v) => v.configured);
    if (target.checked) list.forEach((v) => state.selected.add(v.id));
    else list.forEach((v) => state.selected.delete(v.id));
    renderServersTable();
    return;
  }
  if (target.classList && target.classList.contains('row-check')) {
    const id = Number(target.value);
    if (target.checked) state.selected.add(id);
    else state.selected.delete(id);
    const all = document.getElementById('selectAll');
    if (all) {
      const list = filteredServers().filter((v) => v.configured);
      all.checked = list.length > 0 && list.every((v) => state.selected.has(v.id));
    }
    updateBatchUI();
  }
});

document.addEventListener('click', async (event) => {
  const el = event.target.closest('[data-action]');
  if (!el) return;
  const action = el.dataset.action;
  const id = Number(el.dataset.id || 0);

  switch (action) {
    case 'inspect': {
      const srv = state.servers.find((v) => v.id === id);
      if (srv && !srv.configured) {
        toast('请先配置登录方式', 'info');
        openServerForm(srv);
        break;
      }
      inspectServer(id, el);
      break;
    }
    case 'scan-network':
      openScanModal();
      break;
    case 'add-server':
      openServerForm(null);
      break;
    case 'edit-server': {
      const srv = state.servers.find((v) => v.id === id);
      if (srv) openServerForm(srv);
      break;
    }
    case 'delete-server': {
      const srv = state.servers.find((v) => v.id === id);
      confirmAction({
        title: '删除服务器',
        message: `确定删除服务器 <strong>${esc(srv ? srv.name : '#' + id)}</strong> 吗？<br>该服务器的历史巡检记录会保留，但无法再执行巡检。`,
        onConfirm: async () => {
          await api(`/api/servers/${id}`, { method: 'DELETE' });
          state.selected.delete(id);
          toast('服务器已删除', 'success');
          await loadServers();
          renderServersTable();
        },
      });
      break;
    }
    case 'view-server': {
      const srv = state.servers.find((v) => v.id === id);
      if (srv && srv.lastInspectionId) location.hash = `#/inspections/${srv.lastInspectionId}`;
      else toast('该服务器尚无巡检记录，请先执行巡检', 'info');
      break;
    }
    case 'batch-inspect':
      batchInspect(el);
      break;
    case 'open-inspection':
      location.hash = `#/inspections/${id}`;
      break;
    case 'delete-inspection':
      confirmAction({
        title: '删除巡检记录',
        message: `确定删除巡检记录 <strong>#${id}</strong> 吗？删除后无法恢复。`,
        onConfirm: async () => {
          await api(`/api/inspections/${id}`, { method: 'DELETE' });
          toast('巡检记录已删除', 'success');
          if (state.route.name === 'inspections' && state.route.id) location.hash = '#/inspections';
          else if (state.route.name === 'inspections') loadInspections(true);
          else onRoute();
        },
      });
      break;
    case 'refresh-inspections':
      loadInspections(true);
      break;
    case 'goto-servers':
      location.hash = '#/servers';
      break;
    case 'back-inspections':
      location.hash = '#/inspections';
      break;
    case 'trust-server':
      trustServer(id, el);
      break;
    default:
      break;
  }
});

window.addEventListener('hashchange', () => {
  if (state.user) onRoute();
});

/* ---------------- 启动 ---------------- */

(async function boot() {
  let data = null;
  try {
    data = await api('/api/session');
  } catch (e) {
    data = null;
  }
  if (data && data.authenticated) {
    state.user = data.user;
    state.version = data.version || '';
    if (!location.hash) location.hash = '#/dashboard';
    renderShell();
    onRoute();
  } else {
    state.version = (data && data.version) || '';
    renderLogin();
  }
})();
