'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

const state = {
  tab: 'clients',
  data: null,        // latest /api/state snapshot
  logsTimer: null,   // setInterval id when auto-refresh is on
};

// ---------- networking ----------
async function api(method, path, body) {
  const opts = {
    method,
    headers: body ? { 'content-type': 'application/json' } : {},
    credentials: 'same-origin',
  };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const resp = await fetch(path, opts);
  const ct = resp.headers.get('content-type') || '';
  const payload = ct.includes('application/json') ? await resp.json().catch(() => ({})) : await resp.text();
  if (!resp.ok) {
    const msg = (payload && payload.error) || `HTTP ${resp.status}`;
    throw new Error(msg);
  }
  return payload;
}

// ---------- toast ----------
let toastTimer = null;
function toast(msg, kind = 'info') {
  const el = $('#toast');
  el.textContent = msg;
  el.classList.remove('error');
  if (kind === 'error') el.classList.add('error');
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 3500);
}

// ---------- tabs ----------
function showTab(name) {
  state.tab = name;
  $$('#tabs .tab').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
  $$('.view').forEach(v => v.classList.toggle('active', v.dataset.view === name));
  if (name === 'logs') refreshLogs();
  if (name === 'activity') refreshActivity();
}
$$('#tabs .tab').forEach(b => b.addEventListener('click', () => showTab(b.dataset.tab)));

// ---------- formatting ----------
function fmtBytes(n) {
  if (!n || n < 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (n >= 10 || i === 0 ? n.toFixed(0) : n.toFixed(1)) + ' ' + units[i];
}
function fmtDate(s) {
  if (!s) return '';
  const d = new Date(s);
  return isNaN(d) ? s : d.toLocaleString();
}

// ---------- state + server info ----------
async function refreshState() {
  try {
    const data = await api('GET', '/api/state');
    state.data = data;
    paintServiceStatus(data.service);
    paintClients(data);
    paintServerInfo(data);
  } catch (e) {
    toast(e.message, 'error');
  }
}
function paintServiceStatus(svc) {
  const dot = $('#service-dot'), txt = $('#service-text'), det = $('#service-detail');
  dot.classList.remove('running', 'stopped');
  if (svc && svc.Running) { dot.classList.add('running'); txt.textContent = 'xray running'; }
  else { dot.classList.add('stopped'); txt.textContent = 'xray stopped'; }
  if (det) det.textContent = svc && svc.Raw ? svc.Raw : '';
}
function paintClients(data) {
  const active = $('#clients-active'), disabled = $('#clients-disabled');
  active.innerHTML = ''; disabled.innerHTML = '';
  (data.clients || []).forEach(c => {
    const tr = document.createElement('tr');
    tr.className = 'clickable';
    tr.dataset.id = c.id;
    tr.innerHTML = `<td>${escapeHTML(c.name || '(unnamed)')}</td>
      <td><span class="uuid">${escapeHTML(c.id)}</span></td>
      <td>${escapeHTML(c.flow || '')}</td>
      <td><button data-action="show">Show link</button></td>`;
    tr.addEventListener('click', e => {
      if (e.target.tagName === 'BUTTON') return;
      openClientModal(c);
    });
    tr.querySelector('[data-action="show"]').addEventListener('click', () => openClientModal(c));
    active.appendChild(tr);
  });
  (data.disabled || []).forEach(c => {
    const tr = document.createElement('tr');
    tr.innerHTML = `<td>${escapeHTML(c.name || '(unnamed)')}</td>
      <td><span class="uuid">${escapeHTML(c.id)}</span></td>
      <td>${escapeHTML(fmtDate(c.disabledAt))}</td>
      <td>
        <button data-action="enable">Enable</button>
        <button class="danger" data-action="delete">Delete</button>
      </td>`;
    tr.querySelector('[data-action="enable"]').addEventListener('click', () => enableClient(c.id));
    tr.querySelector('[data-action="delete"]').addEventListener('click', () => deleteClient(c.id, c.name));
    disabled.appendChild(tr);
  });
}
function paintServerInfo(data) {
  const dl = $('#server-info');
  const r = (data.server && data.server.reality) || {};
  dl.innerHTML = '';
  const rows = [
    ['server_address (from panel.yaml)', data.server_address || ''],
    ['listen', data.server?.listen || ''],
    ['port', data.server?.port ?? ''],
    ['flow', data.server?.flow || ''],
    ['reality.dest', r.dest || ''],
    ['reality.serverNames', (r.serverNames || []).join(', ')],
    ['reality.shortIds', (r.shortIds || []).join(', ')],
    ['reality.fingerprint', r.fingerprint || ''],
    ['reality.publicKey', r.publicKey || '(not derived)'],
    ['stats API', data.stats_api_enabled ? 'enabled' : 'disabled'],
  ];
  rows.forEach(([k, v]) => {
    const dt = document.createElement('dt'); dt.textContent = k;
    const dd = document.createElement('dd'); dd.textContent = v;
    dl.appendChild(dt); dl.appendChild(dd);
  });
  // Prefill the Reality form placeholders with current values.
  const f = $('#form-reality');
  f.dest.placeholder = r.dest || f.dest.placeholder;
  f.serverNames.placeholder = (r.serverNames || []).join('\n') || f.serverNames.placeholder;
  f.shortIds.placeholder = (r.shortIds || []).join('\n') || f.shortIds.placeholder;
}

// ---------- client actions ----------
$('#btn-add-client').addEventListener('click', async () => {
  const name = prompt('Client name (shown in VPN apps):');
  if (!name) return;
  try {
    await api('POST', '/api/clients', { name });
    toast('Client added. xray restarted.');
    await refreshState();
  } catch (e) {
    toast(e.message, 'error');
  }
});

async function openClientModal(client) {
  const dlg = $('#client-modal');
  $('#client-modal-title').textContent = client.name || client.id;
  $('#client-link').value = 'loading…';
  $('#client-qr').removeAttribute('src');
  // Rewire the action buttons to this specific client.
  $('#btn-rename-client').onclick = () => renameClient(client);
  $('#btn-disable-client').onclick = () => disableClient(client, dlg);
  $('#btn-delete-client').onclick = () => deleteClient(client.id, client.name, dlg);
  dlg.showModal();
  try {
    const link = await api('GET', `/api/clients/${encodeURIComponent(client.id)}/link`);
    $('#client-link').value = link.url;
    $('#client-qr').src = `/api/clients/${encodeURIComponent(client.id)}/qr.png`;
  } catch (e) {
    $('#client-link').value = '';
    toast(`link: ${e.message}`, 'error');
  }
}
$('#btn-close-modal').addEventListener('click', () => $('#client-modal').close());
$('#btn-copy-link').addEventListener('click', async () => {
  try { await navigator.clipboard.writeText($('#client-link').value); toast('Link copied.'); }
  catch (e) { toast('Clipboard unavailable.', 'error'); }
});

async function renameClient(client) {
  const name = prompt('New name:', client.name || '');
  if (!name || name === client.name) return;
  try {
    await api('PATCH', `/api/clients/${encodeURIComponent(client.id)}`, { name });
    toast('Renamed.');
    $('#client-modal').close();
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
}
async function disableClient(client, dlg) {
  if (!confirm(`Disable ${client.name || client.id}? The UUID is kept; re-enable restores it.`)) return;
  try {
    await api('POST', `/api/clients/${encodeURIComponent(client.id)}/disable`);
    toast('Client disabled.');
    if (dlg) dlg.close();
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
}
async function enableClient(id) {
  try {
    await api('POST', `/api/clients/${encodeURIComponent(id)}/enable`);
    toast('Client enabled.');
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
}
async function deleteClient(id, name, dlg) {
  if (!confirm(`Permanently delete ${name || id}? This cannot be undone.`)) return;
  try {
    await api('DELETE', `/api/clients/${encodeURIComponent(id)}`);
    toast('Client deleted.');
    if (dlg) dlg.close();
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
}

// ---------- server form ----------
$('#form-reality').addEventListener('submit', async e => {
  e.preventDefault();
  const f = e.target;
  const body = {};
  if (f.dest.value) body.dest = f.dest.value.trim();
  if (f.serverNames.value.trim()) body.serverNames = f.serverNames.value.split(/\n+/).map(s => s.trim()).filter(Boolean);
  if (f.shortIds.value.trim()) body.shortIds = f.shortIds.value.split(/\n+/).map(s => s.trim()).filter(Boolean);
  if (f.fingerprint.value) body.fingerprint = f.fingerprint.value;
  if (!Object.keys(body).length) { toast('Nothing to save.'); return; }
  try {
    await api('PATCH', '/api/server/reality', body);
    toast('Reality settings saved. xray restarted.');
    // Clear the form so the user can see the updated current values.
    f.reset();
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
});
$('#btn-regen-keys').addEventListener('click', async () => {
  if (!confirm('Regenerate X25519 keypair? This will BREAK every existing vless:// link — you must redistribute all client links.')) return;
  try {
    const resp = await api('POST', '/api/server/regenerate-keys');
    toast(`New public key: ${resp.publicKey}`);
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
});
$('#btn-enable-stats').addEventListener('click', async () => {
  if (!confirm('Enable xray stats API? This patches config.json and restarts xray.')) return;
  try {
    const resp = await api('POST', '/api/server/enable-stats');
    toast(`Stats API enabled on ${resp.apiAddress}.`);
    await refreshState();
  } catch (e) { toast(e.message, 'error'); }
});

// ---------- logs ----------
async function refreshLogs() {
  const which = $('#logs-which').value;
  const tail = $('#logs-tail').value || 200;
  try {
    const data = await api('GET', `/api/logs/${which}?tail=${tail}`);
    $('#logs-body').textContent = (data.lines || []).join('\n');
    $('#logs-meta').textContent = data.path + (data.truncated ? ' (window truncated)' : '');
  } catch (e) { toast(e.message, 'error'); }
}
$('#btn-refresh-logs').addEventListener('click', refreshLogs);
$('#logs-which').addEventListener('change', refreshLogs);
$('#logs-auto').addEventListener('change', e => {
  clearInterval(state.logsTimer); state.logsTimer = null;
  if (e.target.checked) state.logsTimer = setInterval(refreshLogs, 5000);
});

// ---------- activity ----------
async function refreshActivity() {
  try {
    const data = await api('GET', '/api/activity');
    const tbody = $('#activity-body');
    tbody.innerHTML = '';
    if (!data.enabled) {
      $('#activity-meta').textContent = data.message || 'Stats not enabled.';
      return;
    }
    $('#activity-meta').textContent = `${(data.users || []).length} active clients`;
    (data.users || []).sort((a, b) => (b.uplink + b.downlink) - (a.uplink + a.downlink));
    (data.users || []).forEach(u => {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td>${escapeHTML(u.email)}</td><td>${fmtBytes(u.uplink)}</td><td>${fmtBytes(u.downlink)}</td>`;
      tbody.appendChild(tr);
    });
  } catch (e) { toast(e.message, 'error'); }
}
$('#btn-refresh-activity').addEventListener('click', refreshActivity);

// ---------- service ----------
async function serviceAction(action) {
  if (action === 'stop' && !confirm('Stop xray? All VPN connections will drop until you start it again.')) return;
  try {
    const st = await api('POST', `/api/service/${action}`);
    paintServiceStatus(st);
    toast(`service ${action}: ${st.Running ? 'running' : 'stopped'}`);
  } catch (e) { toast(e.message, 'error'); }
}
$('#btn-svc-start').addEventListener('click', () => serviceAction('start'));
$('#btn-svc-stop').addEventListener('click', () => serviceAction('stop'));
$('#btn-svc-restart').addEventListener('click', () => serviceAction('restart'));

// ---------- utils ----------
function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

// ---------- boot ----------
showTab('clients');
refreshState();
setInterval(refreshState, 15000);
