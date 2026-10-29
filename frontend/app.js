import { Call } from '/wails/runtime.js';
import { initialiseAbout } from './about.js';
import { renderInventory, sectionNames, sectionDescriptions } from './inventory.js';
import { initialiseConnections } from './connections.js';

const $ = id => document.getElementById(id);
const call = (method, ...args) => Call.ByName(`main.DesktopService.${method}`, ...args);
let current = null, busy = false, sortKey = 'name', sortDirection = 1;
let activeSection = 'cameras';
const controls = ['close', 'filter', 'apply-filter', 'clear-filter', 'reveal', 'export', 'virtual', 'refresh', 'save-backup'];
function options() { return {filter: $('filter').value.trim(), reveal: current?.options.reveal || false, includeVirtual: $('virtual').checked}; }
function enable() {
  for (const id of controls) $(id).disabled = busy || !current;
  document.querySelectorAll('[data-section]').forEach(button => button.disabled = busy || !current);
  $('open').disabled = $('open-empty').disabled = busy;
  document.body.setAttribute('aria-busy', String(busy));
}
function showSection() {
  const cameras = activeSection === 'cameras';
  $('page-title').textContent = sectionNames[activeSection];
  $('page-description').textContent = sectionDescriptions[activeSection];
  document.querySelector('.stats').hidden = !cameras;
  $('filter-form').hidden = $('reveal').hidden = !cameras;
  $('virtual').closest('label').hidden = $('visibility').hidden = !cameras;
  $('table-wrap').hidden = !current || !cameras;
  $('report-panel').hidden = !current || cameras;
  document.querySelector('.inventory').setAttribute('aria-label', sectionNames[activeSection]);
  document.querySelectorAll('[data-section]').forEach(button => {
    if (button.dataset.section === activeSection) button.setAttribute('aria-current', 'page');
    else button.removeAttribute('aria-current');
  });
  if (current && !cameras) $('table-status').textContent = activeSection === 'system' ? `${current.report.servers.length} servers` : `${current.report[activeSection].length} records`;
  renderInventory(current?.report, activeSection);
}
document.querySelectorAll('[data-section]').forEach(button => button.addEventListener('click', () => {
  if (!current || busy) return;
  activeSection = button.dataset.section; render(current);
}));
function message(text, error = false) {
  $('notice').hidden = $('error').hidden = true;
  if (text) { const el = $(error ? 'error' : 'notice'); el.textContent = text; el.hidden = false; }
}
async function perform(action) {
  if (busy) return;
  busy = true; enable(); message('');
  try { await action(); } catch (error) { message(error.message || String(error), true); }
  finally { busy = false; enable(); }
}
function badge(status) {
  const el = document.createElement('span'); el.className = 'badge';
  const labels = {recovered:'Recovered', empty:'Empty', not_stored:'Not stored', decode_error:'Decode error'};
  el.textContent = labels[status] || status;
  if (status === 'recovered') el.classList.add('good');
  if (status === 'decode_error') el.classList.add('warning');
  return el;
}
function cell(row, value, className = '') {
  const td = document.createElement('td'); td.textContent = value || '—'; td.className = className; row.append(td); return td;
}
function details(camera) {
  $('details-title').textContent = camera.name || 'Unnamed camera';
  $('details-subtitle').textContent = current.options.reveal ? 'Passwords and complete URLs are visible.' : 'Passwords and sensitive URL components are hidden.';
  $('details').replaceChildren();
  const fields = {id:'Camera UUID', host:'Address', username:'Username', password:'Password', vendor:'Vendor', model:'Model', server_name:'Server', server_version:'Nx version', mac:'MAC address', serial:'Serial number', firmware:'Firmware', group:'Group', logical_id:'Logical ID', url:'Device URL', onvif_url:'ONVIF URL', stream_urls:'Stream URLs', credential_source:'Credential source', credential_status:'Credential status', credential_error:'Decode error', stored_status_code:'Stored status code', schedule_enabled:'Recording schedule', audio_enabled:'Audio enabled', warnings:'Metadata warnings'};
  for (const [key, label] of Object.entries(fields)) {
    let value = camera[key];
    if (value === null || value === undefined || value === '' || (Array.isArray(value) && !value.length)) continue;
    if (typeof value === 'object') value = JSON.stringify(value, null, 2);
    const dt = document.createElement('dt'), dd = document.createElement('dd');
    dt.textContent = label; dd.textContent = String(value); $('details').append(dt, dd);
  }
  $('details-dialog').showModal();
}
function render(view) {
  current = view;
  $('details-dialog').close(); $('details').replaceChildren(); $('details-subtitle').textContent = '';
  $('details-title').textContent = 'Details';
  $('empty').hidden = !!view; $('table-wrap').hidden = !view;
  $('filename').textContent = view?.filename || 'No database selected';
  $('loaded-at').textContent = view ? 'Opened ' + new Date(view.loadedAt).toLocaleTimeString([], {hour:'2-digit', minute:'2-digit'}) : 'Open a database to begin.';
  $('source-route').textContent = view?.source.kind === 'server' ? view.source.route + ' · Temporary snapshot' : '';
  $('save-backup').hidden = view?.source.kind !== 'server';
  $('refresh').textContent = view?.source.kind === 'server' ? '↻ Refresh from server…' : '↻ Refresh';
  $('rows').replaceChildren();
  const summary = view?.report.summary;
  $('count').textContent = summary?.matched_records ?? '—';
  $('recovered').textContent = summary?.credentials_recovered ?? '—';
  $('missing').textContent = summary ? summary.credentials_not_stored + summary.empty_credentials : '—';
  $('errors').textContent = summary?.credential_decode_errors ?? '—';
  $('record-count').textContent = summary ? `${summary.total_camera_records} records in database` : 'No database open';
  $('error-hint').textContent = summary?.credential_decode_errors ? 'Review affected camera details' : 'Credential format issues';
  $('filter').value = view?.options.filter || ''; $('virtual').checked = view?.options.includeVirtual || false;
  const reveal = view?.options.reveal || false;
  $('reveal').textContent = reveal ? 'Hide passwords' : 'Reveal passwords'; $('reveal').setAttribute('aria-pressed', String(reveal));
  $('visibility').textContent = reveal ? 'Passwords visible' : 'Passwords hidden'; $('visibility').className = reveal ? 'badge warning' : 'badge';
  $('table-status').textContent = view ? `${summary.matched_records} cameras · ${summary.virtual_records_omitted} virtual records omitted` : 'Ready to open a database';
  $('snapshot-status').textContent = view ? 'Snapshot · ' + (summary.server_versions.join(', ') || 'Nx version unavailable') : 'No network connections to cameras';
  if (!view) { activeSection = 'cameras'; showSection(); enable(); return; }
  const cameras = [...view.report.cameras].sort((a,b) => String(a[sortKey] || '').localeCompare(String(b[sortKey] || ''), undefined, {numeric:true}) * sortDirection);
  $('no-results').hidden = cameras.length !== 0;
  for (const camera of cameras) {
    const row = document.createElement('tr');
    const name = cell(row, ''); name.textContent = ''; const strong = document.createElement('strong'); strong.textContent = camera.name || 'Unnamed camera'; name.append(strong);
    if (camera.group || camera.virtual) { const small = document.createElement('small'); small.textContent = camera.virtual ? 'Virtual camera' : camera.group; name.append(small); }
    cell(row, camera.host, 'mono'); cell(row, camera.username); cell(row, camera.password === '[redacted]' ? '••••••••' : camera.password, 'mono');
    const vendor = cell(row, camera.vendor); const model = document.createElement('small'); model.textContent = camera.model; vendor.append(model);
    cell(row, camera.server_name); const status = cell(row, ''); status.textContent = ''; status.append(badge(camera.credential_status));
    const action = cell(row, ''); action.textContent = ''; const button = document.createElement('button'); button.className = 'row-details'; button.textContent = '›'; button.setAttribute('aria-label', 'Details for ' + camera.name); button.addEventListener('click', () => details(camera)); action.append(button);
    row.addEventListener('dblclick', () => details(camera)); $('rows').append(row);
  }
  showSection(); enable();
}
const connections = initialiseConnections({call, render, perform, message, setBusy: value => { busy = value; enable(); }});
async function openDatabase() { if (!busy) { try { await connections.open(); } catch(e) { message(e.message, true); } } }
$('open').addEventListener('click', openDatabase); $('open-empty').addEventListener('click', openDatabase);
$('close').addEventListener('click', () => perform(async () => { await call('CloseDatabase'); render(null); }));
$('filter-form').addEventListener('submit', event => { event.preventDefault(); perform(async () => render(await call('View', options()))); });
$('clear-filter').addEventListener('click', () => perform(async () => render(await call('View', {...options(), filter:''}))));
$('virtual').addEventListener('change', () => perform(async () => render(await call('View', options()))));
$('refresh').addEventListener('click', () => {
  if (current?.source.kind === 'server') { connections.open(current.source).catch(e => message(e.message,true)); return; }
  perform(async () => { render(await call('Refresh', options())); message('Database snapshot refreshed.'); });
});
$('save-backup').addEventListener('click', () => perform(async () => { const name = await call('SaveBackup'); if (name) message('Saved backup to ' + name); }));
$('reveal').addEventListener('click', () => perform(async () => {
  const next = {...options(), reveal: !current.options.reveal};
  if (!next.reveal) {
    // Remove secrets from the DOM immediately, including an open details dialog.
    const safe = structuredClone(current); safe.options.reveal = false;
    for (const c of safe.report.cameras) { if (c.has_password) c.password = '[redacted]'; c.url = c.server_url = c.onvif_url = '[hidden]'; c.stream_urls = {}; }
    render(safe);
  }
  render(await call('View', next));
}));
$('export').addEventListener('click', () => {
  if (!current || busy) return;
  $('export-scope').textContent = `The full report includes ${current.report.cameras.length} cameras in the current view, ${current.report.users.length} users, ${current.report.servers.length} servers, ${current.report.storage.length} storage locations and ${current.report.licenses.length} licences. Camera filters apply only to cameras.`;
  $('export-privacy').textContent = (current.options.reveal ? 'Camera passwords and complete camera URLs will be included.' : 'Camera passwords and sensitive URL components will remain hidden.') + ' Licence exports include full licence keys.';
  $('export-privacy').className = 'export-privacy' + (current.options.reveal ? ' revealed' : '');
  $('export-dialog').showModal();
});
$('save-export').addEventListener('click', () => {
  const format = document.querySelector('input[name=format]:checked').value;
  $('export-dialog').close();
  perform(async () => { const result = await call('Export', format, {...current.options, section: $('export-section').value}); if (result) message(`Exported report to ${result.filename}.`); });
});
document.querySelectorAll('[data-sort]').forEach(button => button.addEventListener('click', () => {
  if (!current || busy) return;
  const key = button.dataset.sort; sortDirection = sortKey === key ? -sortDirection : 1; sortKey = key; render(current);
}));
try {
  const info = await call('Info'); document.body.dataset.platform = info.platform;
  $('version').textContent = 'Version ' + info.version;
  await initialiseAbout(info, call);
  window.appReady = true;
} catch (error) { message('Could not connect to the database reader: ' + (error.message || error), true); }
render(null);
