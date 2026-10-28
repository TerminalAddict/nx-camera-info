const $ = id => document.getElementById(id);
export const sectionNames = {cameras:'Camera inventory', users:'Users', system:'System & servers', storage:'Storage', licenses:'Licences'};
export const sectionDescriptions = {
  cameras:'Explore devices and recover saved camera credentials.',
  users:'Local, cloud and directory accounts, including inherited groups.',
  system:'Saved server addresses, versions, hardware and system settings.',
  storage:'Recording and backup locations with their stored configuration.',
  licenses:'Full licence keys, licensed channels and remaining deactivations.'
};
const labels = {
  id:'ID', name:'Name', account:'Account', type:'Type', display_name:'Display name', role:'Role', email:'Email', enabled:'Enabled',
  url:'Main server address', public_ip:'Public IP', network_addresses:'Network addresses', lan_discovery_addresses:'LAN discovery addresses', discovery:'All discovery entries',
  version:'Nx version', full_version:'Full build identifier', operating_system:'Operating system', os_info:'Stored OS details', architecture:'Architecture', cpu:'CPU',
  memory_bytes:'Reported RAM', time_zone:'Time zone', time_zone_offset_ms:'UTC offset (milliseconds)', max_cameras:'Configured camera limit', redundancy:'Failover enabled',
  local_id:'Local system ID', cloud_id:'Cloud system ID', cloud_account:'Cloud account', cloud_host:'Cloud host', latest_transaction:'Latest stored transaction (UTC)',
  properties:'Additional stored properties', path:'Storage path', server_id:'Server ID', server_name:'Server', recording_enabled:'Recording enabled', backup:'Backup storage',
  stored_space_bytes:'Stored space', space_limit_bytes:'Stored space limit', archive_mode:'Archive mode', status_flags:'Storage status flags',
  key:'Licence key', serial:'Serial', channels:'Licensed channels', class:'Licence class', brand:'Brand', hardware_id:'Hardware ID', expiration:'Expiry field',
  company:'Company', support:'Support', activations_remaining:'Activations remaining', deactivations_remaining:'Deactivations remaining',
  deactivations_used:'Deactivations used', deactivation_eligible:'Eligible licence type', deactivation_information:'Deactivation information',
  activation_information:'Activation information', fields:'All licence fields', license_block:'Original licence block', group_ids:'Group IDs', groups:'Groups (including inherited)', direct_permissions:'Direct permissions (raw bitmask)'
};
function element(tag, text, className) {
  const el = document.createElement(tag); if (text !== undefined) el.textContent = text;
  if (className) el.className = className; return el;
}
function valueText(key, value, record = {}) {
  if (value === null || value === undefined) {
    if (key === 'activations_remaining') return 'Unavailable from database';
    if (key === 'deactivations_remaining' && record.deactivation_eligible === false) return 'Not applicable';
    return key.startsWith('deactivation') ? 'Unknown' : 'Not stored';
  }
  if (value === '') return 'Not set';
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  if (key === 'memory_bytes' || key === 'stored_space_bytes' || key === 'space_limit_bytes') {
    return value < 0 ? `${value} (unknown / automatic)` : `${(value / 1073741824).toFixed(2)} GiB (${value.toLocaleString()} bytes)`;
  }
  if (typeof value === 'object') return JSON.stringify(value, null, 2);
  return String(value);
}
function fields(record, target, omit = []) {
  for (const [key, value] of Object.entries(record)) {
    if (omit.includes(key)) continue;
    target.append(element('dt', labels[key] || key.replaceAll('_', ' ')), element('dd', valueText(key, value, record)));
  }
}
function details(title, record) {
  $('details-title').textContent = title;
  $('details-subtitle').textContent = 'Stored database information. No live status check has been performed.';
  $('details').replaceChildren(); fields(record, $('details')); $('details-dialog').showModal();
}
function card(title, record, keys) {
  const el = element('section', undefined, 'report-card'), heading = element('div', undefined, 'report-heading');
  const button = element('button', 'All details', 'quiet'); button.addEventListener('click', () => details(title, record));
  heading.append(element('h2', title), button); el.append(heading);
  const dl = element('dl'); fields(Object.fromEntries(keys.map(key => [key, record[key]])), dl); el.append(dl); return el;
}
function table(records, columns, title) {
  const el = element('table'), head = element('thead'), header = element('tr'), body = element('tbody');
  for (const [key, label] of columns) header.append(element('th', label || labels[key]));
  header.append(element('th', 'Details')); head.append(header); el.append(head, body);
  for (const record of records) {
    const row = element('tr');
    for (const [key] of columns) row.append(element('td', valueText(key, record[key], record), key === 'key' ? 'mono' : ''));
    const td = element('td'), button = element('button', 'Details', 'row-details');
    button.setAttribute('aria-label', 'Details for ' + (record.account || record.key || title));
    button.addEventListener('click', () => details(record.account || title, record)); td.append(button); row.append(td); body.append(row);
  }
  return el;
}
export function renderInventory(report, section) {
  const panel = $('report-panel'); panel.replaceChildren();
  if (!report || section === 'cameras') return;
  if (report.warnings?.length) {
    const box = element('details', undefined, 'report-notes'); box.append(element('summary', 'Database compatibility notes'));
    for (const warning of report.warnings) box.append(element('p', warning)); panel.append(box);
  }
  if (section === 'users') {
    panel.append(element('p', `${report.users.length} accounts. Roles include inherited group memberships. User passwords are not recoverable from account hashes.`, 'section-note'));
    if (report.users.length) panel.append(table(report.users, [['account'], ['type'], ['display_name'], ['role'], ['email'], ['enabled']], 'User details'));
  } else if (section === 'system') {
    panel.append(card(report.system.name || 'System', report.system, ['name', 'cloud_account']));
    panel.append(element('p', `${report.servers.length} servers. Discovery addresses may be historical.`, 'section-note'));
    for (const server of report.servers) panel.append(card(server.name || 'Server', server, ['name', 'url', 'lan_discovery_addresses', 'version', 'full_version', 'operating_system', 'architecture', 'cpu', 'memory_bytes', 'time_zone']));
  } else if (section === 'storage') {
    panel.append(element('p', `${report.storage.length} storage locations. Space and status values are saved metadata, not live disk usage or recording health.`, 'section-note'));
    if (report.storage.length) panel.append(table(report.storage, [['server_name'], ['path'], ['type'], ['recording_enabled'], ['backup'], ['stored_space_bytes']], 'Storage details'));
  } else if (section === 'licenses') {
    panel.append(element('p', 'Deactivations remaining = 3 minus deactivations used, with a minimum of zero. An absent counter defaults to zero used, matching Nx. Counts reflect the database snapshot and have not been verified online.', 'section-note'));
    if (report.licenses.length) panel.append(table(report.licenses, [['key'], ['class'], ['channels'], ['expiration'], ['deactivations_used'], ['deactivations_remaining']], 'Licence details'));
    panel.append(element('p', 'Details and exports include every stored licence field and the original licence block. Licence validity has not been checked online.', 'section-note'));
  }
  if (section !== 'system' && !report[section]?.length) panel.append(element('p', 'No records available in this database.', 'section-note'));
}
