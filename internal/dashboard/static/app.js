const authInput = document.getElementById('auth');
const savedAuth = localStorage.getItem('dashboard-auth') || '';
authInput.value = savedAuth;

document.getElementById('save-auth').addEventListener('click', () => {
  localStorage.setItem('dashboard-auth', authInput.value.trim());
  loadAll();
});

document.querySelectorAll('.nav').forEach((button) => {
  button.addEventListener('click', () => {
    document.querySelectorAll('.nav').forEach((node) => { node.classList.remove('active'); });
    document.querySelectorAll('.view').forEach((node) => { node.classList.remove('active'); });
    button.classList.add('active');
    document.getElementById(button.dataset.view).classList.add('active');
  });
});

document.getElementById('load-sessions').addEventListener('click', loadSessions);

function headers() {
  const auth = localStorage.getItem('dashboard-auth') || '';
  return auth ? { 'X-Dashboard-Auth': auth, 'Content-Type': 'application/json' } : { 'Content-Type': 'application/json' };
}

async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { ...headers(), ...(options.headers || {}) } });
  if (!response.ok) {
    throw new Error(await response.text());
  }
  return response.json();
}

async function loadOverview() {
  const status = await api('/api/status');
  document.getElementById('uptime').textContent = `${status.uptime_seconds}s`;
  document.getElementById('messages').textContent = status.messages_total;
  document.getElementById('users').textContent = status.active_users;
  document.getElementById('tool-calls').textContent = status.tool_calls_total;
  document.getElementById('status-json').textContent = JSON.stringify(status, null, 2);
}

async function loadPlugins() {
  const plugins = await api('/api/plugins');
  const root = document.getElementById('plugin-list');
  root.innerHTML = plugins.map((plugin) => `
    <div class="item">
      <div>
        <h3>${plugin.name}</h3>
        <div class="meta">${plugin.description} · 工具 ${plugin.tool_count}</div>
      </div>
      <button class="toggle" data-plugin="${plugin.name}" data-enabled="${!plugin.active}">${plugin.active ? '停用' : '启用'}</button>
    </div>
  `).join('');
  root.querySelectorAll('[data-plugin]').forEach((button) => {
    button.addEventListener('click', async () => {
      await api(`/api/plugins/${button.dataset.plugin}/toggle`, { method: 'POST', body: JSON.stringify({ enabled: button.dataset.enabled === 'true' }) });
      loadPlugins();
      loadTools();
    });
  });
}

async function loadTools() {
  const tools = await api('/api/tools');
  const root = document.getElementById('tool-list');
  root.innerHTML = tools.map((tool) => `
    <div class="item">
      <div>
        <h3>${tool.name}</h3>
        <div class="meta">${tool.category || 'general'} · ${tool.description}</div>
      </div>
      <button class="toggle" data-tool="${tool.name}" data-enabled="${!tool.active}">${tool.active ? '停用' : '启用'}</button>
    </div>
  `).join('');
  root.querySelectorAll('[data-tool]').forEach((button) => {
    button.addEventListener('click', async () => {
      await api(`/api/tools/${button.dataset.tool}/toggle`, { method: 'POST', body: JSON.stringify({ enabled: button.dataset.enabled === 'true' }) });
      loadTools();
    });
  });
}

async function loadConfig() {
  const config = await api('/api/config');
  const root = document.getElementById('config-list');
  root.innerHTML = Object.entries(config).map(([key, value]) => `
    <div class="item">
      <div>
        <h3>${key}</h3>
        <textarea id="cfg-${key}">${value || ''}</textarea>
      </div>
      <button class="save-config" data-key="${key}">保存</button>
    </div>
  `).join('');
  root.querySelectorAll('[data-key]').forEach((button) => {
    button.addEventListener('click', async () => {
      const value = document.getElementById(`cfg-${button.dataset.key}`).value;
      await api(`/api/config/${button.dataset.key}`, { method: 'POST', body: JSON.stringify({ value }) });
      loadOverview();
    });
  });
}

async function loadProviders() {
  document.getElementById('provider-list').textContent = JSON.stringify(await api('/api/providers'), null, 2);
}

async function loadLogs() {
  document.getElementById('log-list').textContent = JSON.stringify(await api('/api/logs'), null, 2);
}

async function loadSessions() {
  const uid = document.getElementById('session-uid').value.trim();
  if (!uid) {
    return;
  }
  document.getElementById('session-list').textContent = JSON.stringify(await api(`/api/sessions/${uid}`), null, 2);
}

async function loadAll() {
  try {
    await Promise.all([loadOverview(), loadPlugins(), loadTools(), loadConfig(), loadProviders(), loadLogs()]);
  } catch (error) {
    document.getElementById('status-json').textContent = String(error);
  }
}

loadAll();
setInterval(() => {
  loadOverview();
  loadLogs();
}, 3000);
