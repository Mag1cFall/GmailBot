// ─── Auth ───────────────────────────────────────────────────────────────
const authInput = document.getElementById('auth');
authInput.value = localStorage.getItem('dashboard-auth') || '';

document.getElementById('save-auth').addEventListener('click', () => {
  localStorage.setItem('dashboard-auth', authInput.value.trim());
  loadAll();
});

document.getElementById('webui-link').addEventListener('click', () => {
  const p = parseInt(location.port || (location.protocol === 'https:' ? 443 : 80));
  window.open(`${location.protocol}//${location.hostname}:${p + 1}`, '_blank');
});

// ─── Nav ────────────────────────────────────────────────────────────────
document.querySelectorAll('.rail-item').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.rail-item').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    btn.classList.add('active');
    const view = document.getElementById(btn.dataset.view);
    if (view) view.classList.add('active');
    if (btn.dataset.view === 'logs') loadLogs();
    if (btn.dataset.view === 'sessions') loadSessionsOverview();
  });
});

// ─── API helper ─────────────────────────────────────────────────────────
function getHeaders() {
  const auth = localStorage.getItem('dashboard-auth') || '';
  return { 'X-Dashboard-Auth': auth, 'Content-Type': 'application/json' };
}

const toast = document.getElementById('toast');
function showToast(msg) {
  toast.textContent = msg;
  toast.style.display = 'block';
  clearTimeout(showToast._t);
  showToast._t = setTimeout(() => { toast.style.display = 'none'; }, 4000);
}

async function api(path, opts = {}) {
  try {
    const r = await fetch(path, { ...opts, headers: { ...getHeaders(), ...(opts.headers || {}) } });
    if (!r.ok) {
      if (r.status === 401) setStatus(false, '未授权');
      throw new Error(await r.text() || `HTTP ${r.status}`);
    }
    setStatus(true);
    return await r.json();
  } catch (e) {
    showToast(e.message);
    setStatus(false, e.message);
    throw e;
  }
}

const dotEl = document.getElementById('online-dot');
const statusEl = document.getElementById('system-status-text');
function setStatus(ok, msg) {
  dotEl.className = 'dot' + (ok ? ' online' : ' error');
  statusEl.textContent = ok ? (msg || '在线运行中') : (msg || '离线或异常');
}

// ─── Utils ──────────────────────────────────────────────────────────────
function fmtDur(sec) {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  if (d) return `${d}天 ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}
function fmtDate(s) { return s ? new Date(s).toLocaleString('zh-CN') : '—'; }
function platformPill(p) {
  const cls = { telegram:'pill-telegram', webui:'pill-webui', lark:'pill-lark', qq:'pill-qq' };
  return `<span class="pill ${cls[p] || 'pill-default'}">${p}</span>`;
}
function esc(s) { return String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }

// ─── Overview ───────────────────────────────────────────────────────────
async function loadOverview() {
  try {
    const s = await api('/api/status');
    document.getElementById('uptime').textContent = fmtDur(s.uptime_seconds || 0);
    document.getElementById('messages').textContent = s.messages_total || 0;
    document.getElementById('users').textContent = s.active_users || 0;
    document.getElementById('tool-calls').textContent = s.tool_calls_total || 0;
    document.getElementById('status-json').textContent = JSON.stringify(s, null, 2);
  } catch {}
}

// ─── Plugins ────────────────────────────────────────────────────────────
async function loadPlugins() {
  try {
    const plugins = await api('/api/plugins');
    const filterSel = document.getElementById('tool-plugin-filter');
    const cur = filterSel.value;
    filterSel.innerHTML = '<option value="all">全部插件</option>' +
      plugins.map(p => `<option value="${esc(p.name)}">${esc(p.name)}</option>`).join('');
    filterSel.value = cur || 'all';

    document.getElementById('plugin-list').innerHTML = plugins.map(p => `
      <tr>
        <td style="font-family:var(--mono);font-weight:600">${esc(p.name)}</td>
        <td style="color:var(--muted)">${esc(p.description)}</td>
        <td style="font-family:var(--mono)">${p.tool_count}</td>
        <td style="text-align:right">
          <button class="btn-toggle ${p.active ? 'on' : 'off'}"
            data-plugin="${esc(p.name)}" data-active="${p.active}">
            ${p.active ? '已启用' : '已停用'}
          </button>
        </td>
      </tr>
    `).join('');

    document.querySelectorAll('[data-plugin]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const enabled = btn.dataset.active === 'true';
        btn.textContent = '…';
        try {
          await api(`/api/plugins/${btn.dataset.plugin}`, {
            method: 'POST', body: JSON.stringify({ enabled: !enabled })
          });
          await loadPlugins(); await loadTools();
        } catch { await loadPlugins(); }
      });
    });
  } catch {}
}

// ─── Tools ──────────────────────────────────────────────────────────────
let allTools = [];
async function loadTools() {
  try { allTools = await api('/api/tools'); renderTools(); } catch {}
}
document.getElementById('tool-plugin-filter').addEventListener('change', renderTools);
function renderTools() {
  const filter = document.getElementById('tool-plugin-filter').value;
  const list = filter === 'all' ? allTools : allTools.filter(t => t.category === filter);
  document.getElementById('tool-list').innerHTML = list.map(t => `
    <tr>
      <td style="font-family:var(--mono);font-weight:600">${esc(t.name)}</td>
      <td style="color:var(--muted)">${esc(t.description)}</td>
      <td><span class="pill pill-default">${esc(t.category || 'general')}</span></td>
      <td style="text-align:right">
        <button class="btn-toggle ${t.active ? 'on' : 'off'}"
          data-tool="${esc(t.name)}" data-active="${t.active}">
          ${t.active ? '已启用' : '停用'}
        </button>
      </td>
    </tr>
  `).join('');
  document.querySelectorAll('[data-tool]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const enabled = btn.dataset.active === 'true';
      btn.textContent = '…';
      try { await api(`/api/tools/${btn.dataset.tool}`, { method: 'POST', body: JSON.stringify({ enabled: !enabled }) }); await loadTools(); }
      catch { await loadTools(); }
    });
  });
}

// ─── Config (fix: use DOM value property, not innerHTML attr) ───────────
async function loadConfig() {
  try {
    const config = await api('/api/config');
    const tbody = document.getElementById('config-list');
    tbody.innerHTML = Object.entries(config).map(([k, v]) => {
      const isSensitive = /KEY|TOKEN|SECRET|AUTH|PASSWORD/i.test(k);
      return `
        <tr data-cfg-key="${esc(k)}">
          <td style="font-family:var(--mono);font-size:12px;color:var(--muted)">${esc(k)}</td>
          <td>
            <input class="cfg-input${isSensitive ? ' masked' : ''}"
              type="${isSensitive ? 'password' : 'text'}"
              data-cfg-input="${esc(k)}">
          </td>
          <td style="text-align:right">
            <button class="btn-sm" data-cfg-save="${esc(k)}">保存</button>
          </td>
        </tr>
      `;
    }).join('');
    // 正确设置 input.value（DOM 属性），而非 HTML attribute
    Object.entries(config).forEach(([k, v]) => {
      const inp = tbody.querySelector(`[data-cfg-input="${k}"]`);
      if (inp) inp.value = v || '';
    });
    tbody.querySelectorAll('[data-cfg-save]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const key = btn.dataset.cfgSave;
        const inp = tbody.querySelector(`[data-cfg-input="${key}"]`);
        const orig = btn.textContent;
        btn.textContent = '…';
        try {
          await api(`/api/config/${key}`, { method: 'POST', body: JSON.stringify({ value: inp.value }) });
          btn.textContent = '✓';
          setTimeout(() => { btn.textContent = '保存'; }, 2000);
          loadOverview();
        } catch { btn.textContent = '✗'; setTimeout(() => { btn.textContent = orig; }, 2000); }
      });
    });
  } catch {}
}

// ─── Providers ──────────────────────────────────────────────────────────
async function loadProviders() {
  try {
    const providers = await api('/api/providers');
    document.getElementById('provider-list').innerHTML = (providers || []).map((p, i) => `
      <tr>
        <td style="font-family:var(--mono);font-weight:600">${esc(p.name)}</td>
        <td style="color:var(--muted)">${esc(p.type || 'openai_compat')}</td>
        <td style="font-family:var(--mono)">${esc(p.model || '—')}</td>
        <td>${i === 0 ? '<span class="pill pill-primary">主要</span>' : '<span class="pill pill-default">备用</span>'}</td>
      </tr>
    `).join('');
  } catch {}
}

// ─── Logs ───────────────────────────────────────────────────────────────
let logsCache = [];
async function loadLogs() {
  try { logsCache = await api('/api/logs'); renderLogs(); } catch {}
}
document.getElementById('log-level-filter').addEventListener('change', renderLogs);
function renderLogs() {
  const filter = document.getElementById('log-level-filter').value.toUpperCase();
  const entries = Array.isArray(logsCache) ? logsCache : [];
  const filtered = filter === 'ALL' ? entries : entries.filter(e => (e.level||'').toUpperCase() === filter);
  const pre = document.getElementById('log-list');
  if (!filtered.length) { pre.innerHTML = '<span style="color:var(--muted)">暂无日志</span>'; return; }
  pre.innerHTML = [...filtered].reverse().map(e => {
    const lvl = (e.level||'INFO').toUpperCase();
    const cls = lvl === 'ERROR' ? 'l-error' : lvl === 'WARN' || lvl === 'WARNING' ? 'l-warn' : 'l-info';
    const t = e.time ? new Date(e.time).toTimeString().slice(0,8) : '';
    const attrs = e.attrs && Object.keys(e.attrs).length
      ? ' ' + Object.entries(e.attrs).map(([k,v]) => `${k}=${JSON.stringify(v)}`).join(' ') : '';
    return `<span class="l-time">${t}</span> <span class="${cls}">[${lvl}] ${esc(e.message||'')}${esc(attrs)}</span>`;
  }).join('\n');
  pre.scrollTop = pre.scrollHeight;
}

// ─── Sessions ───────────────────────────────────────────────────────────
async function loadSessionsOverview() {
  try {
    document.getElementById('session-detail').style.display = 'none';
    document.getElementById('session-table').style.display = 'table';
    document.getElementById('back-sessions').style.display = 'none';
    const sessions = await api('/api/sessions');
    const tbody = document.getElementById('session-list');
    if (!sessions || !sessions.length) {
      tbody.innerHTML = '<tr><td colspan="6" style="color:var(--muted);text-align:center;padding:24px">暂无会话</td></tr>';
      return;
    }
    tbody.innerHTML = sessions.map(s => {
      const pct = Math.min(100, ((s.token_estimate || 0) / 128000) * 100);
      const fc = pct > 78 ? 'danger' : pct > 50 ? 'warn' : 'safe';
      return `
        <tr class="session-row" data-uid="${esc(s.user_id)}" data-platform="${esc(s.platform)}" style="cursor:pointer">
          <td>${platformPill(s.platform)}</td>
          <td style="font-family:var(--mono);font-size:12px">${esc(s.user_id)}</td>
          <td style="font-family:var(--mono)">${s.message_count}</td>
          <td style="min-width:120px">
            <div style="display:flex;align-items:center;gap:8px">
              <span style="font-family:var(--mono);font-size:12px">${(s.token_estimate || 0).toLocaleString()}</span>
              <div class="prog" style="flex:1"><div class="prog-fill ${fc}" style="width:${pct}%"></div></div>
            </div>
          </td>
          <td style="color:var(--muted);font-size:12px">${fmtDate(s.last_active)}</td>
          <td style="text-align:right">
            <button class="btn-sm danger session-clear"
              data-uid="${esc(s.user_id)}" data-platform="${esc(s.platform)}">清空</button>
          </td>
        </tr>
      `;
    }).join('');

    tbody.querySelectorAll('.session-row').forEach(row => {
      row.addEventListener('click', e => {
        if (e.target.classList.contains('session-clear')) return;
        loadSessionDetail(row.dataset.uid, row.dataset.platform);
      });
    });
    tbody.querySelectorAll('.session-clear').forEach(btn => {
      btn.addEventListener('click', async e => {
        e.stopPropagation();
        if (!confirm(`清空 ${btn.dataset.platform}/${btn.dataset.uid} 的会话？`)) return;
        try {
          await fetch(`/api/sessions/${btn.dataset.uid}?platform=${btn.dataset.platform}`, {
            method: 'DELETE', headers: getHeaders()
          });
          await loadSessionsOverview();
        } catch {}
      });
    });
  } catch {}
}

document.getElementById('load-sessions').addEventListener('click', () => {
  const uid = document.getElementById('session-uid').value.trim();
  uid ? loadSessionDetail(uid, 'telegram') : loadSessionsOverview();
});
document.getElementById('back-sessions').addEventListener('click', () => {
  document.getElementById('session-uid').value = '';
  loadSessionsOverview();
});

async function loadSessionDetail(uid, platform) {
  try {
    const data = await api(`/api/sessions/${uid}?platform=${platform||'telegram'}`);
    document.getElementById('session-table').style.display = 'none';
    document.getElementById('session-detail').style.display = 'block';
    document.getElementById('back-sessions').style.display = 'inline-block';
    const msgs = Array.isArray(data) ? data.flatMap(s => s.messages || []) : (data.messages || []);
    document.getElementById('session-messages').innerHTML = msgs.length
      ? msgs.filter(m => m.role === 'user' || m.role === 'assistant' || m.role === 'tool').map(m => {
          const content = typeof m.content === 'string' ? m.content : JSON.stringify(m.content || '');
          return `<div class="msg-row">
            <span class="msg-role ${m.role}">${m.role}</span>
            <div class="msg-content">${esc(content.slice(0, 600))}${content.length > 600 ? '…' : ''}</div>
          </div>`;
        }).join('')
      : '<div style="color:var(--muted);padding:16px">空会话</div>';
  } catch {}
}

// ─── Load all ───────────────────────────────────────────────────────────
async function loadAll() {
  await Promise.all([loadOverview(), loadPlugins(), loadTools(), loadConfig(), loadProviders(), loadLogs(), loadSessionsOverview()]);
}
loadAll();

// ─── Auto-refresh ───────────────────────────────────────────────────────
let arInterval = null;
function startAR() {
  if (arInterval) clearInterval(arInterval);
  let tick = 0;
  arInterval = setInterval(() => {
    tick++;
    if (tick % 3 === 0 && document.getElementById('logs').classList.contains('active')) loadLogs();
    if (tick % 10 === 0 && document.getElementById('overview').classList.contains('active')) loadOverview();
    if (tick % 20 === 0 && document.getElementById('sessions').classList.contains('active')) loadSessionsOverview();
  }, 1000);
}
function stopAR() { if (arInterval) { clearInterval(arInterval); arInterval = null; } }
const arToggle = document.getElementById('auto-refresh');
arToggle.addEventListener('change', e => e.target.checked ? startAR() : stopAR());
if (arToggle.checked) startAR();
