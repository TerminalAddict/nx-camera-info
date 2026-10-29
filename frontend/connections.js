const $ = id => document.getElementById(id);

export function initialiseConnections({call, render, perform, message, setBusy}) {
  let timer = null, polling = false, active = false, pending = false, localOpening = false, tab = 'file';
  let sites = [], selectedServer = null, lastList = '', promptKey = '', wantedSite = '';
  const dialog = $('open-dialog');
  const clearSecrets = () => { for (const id of ['server-password', 'cloud-password', 'mfa-code']) $(id).value = ''; };
  const error = text => { $('connection-error').textContent = text || ''; $('connection-error').hidden = !text; };
  function enable(busy) {
    pending = busy; setBusy(busy);
    dialog.querySelectorAll('[data-open-tab], .connection-form input, .connection-form select, .connection-form button, .server-choice, #discover-servers, #open-local-file, #cloud-switch').forEach(el => el.disabled = busy);
    $('connection-cancel').textContent = busy ? 'Cancel connection' : 'Cancel';
  }
  async function switchTab(next) {
    if (pending) return;
    if (tab !== next) {
      await call('ConnectionCancel'); sites = []; lastList = ''; clearSecrets();
      $('cloud-form').hidden = false; $('cloud-sites-panel').hidden = true;
      $('connection-progress').hidden = true; error('');
    }
    tab = next;
    for (const name of ['file','server','cloud']) {
      $('open-panel-' + name).hidden = name !== tab;
      $('open-tab-' + name).setAttribute('aria-selected', String(name === tab));
      $('open-tab-' + name).tabIndex = name === tab ? 0 : -1;
    }
  }
  for (const button of dialog.querySelectorAll('[data-open-tab]')) {
    button.addEventListener('click', () => switchTab(button.dataset.openTab).catch(e => error(e.message)));
    button.addEventListener('keydown', event => {
      const keys = ['ArrowLeft','ArrowRight','Home','End'];
      if (!keys.includes(event.key) || pending) return;
      event.preventDefault();
      const tabs = ['file','server','cloud'];
      const index = event.key === 'Home' ? 0 : event.key === 'End' ? 2 : (tabs.indexOf(tab) + (event.key === 'ArrowRight' ? 1 : 2)) % 3;
      switchTab(tabs[index]).then(() => $('open-tab-' + tabs[index]).focus());
    });
  }
  function renderServers(servers) {
    const list = $('nearby-servers'); list.replaceChildren();
    if (!servers.length) { const p = document.createElement('p'); p.className = 'subtle'; p.textContent = 'No servers found. Enter an address below, or use Nx Cloud.'; list.append(p); }
    for (const server of servers) {
      const button = document.createElement('button'); button.className = 'server-choice';
      const name = document.createElement('strong'), detail = document.createElement('small');
      name.textContent = server.systemName || server.name || 'Nx Witness server';
      detail.textContent = [server.name, server.address, server.version].filter(Boolean).join(' · ');
      button.append(name, detail);
      button.addEventListener('click', () => {
        selectedServer = server; $('server-address').value = server.address;
        $('server-auth').value = server.cloudSystemId ? 'cloud' : 'local'; updateAuth();
        $('server-username').focus();
      }); list.append(button);
    }
  }
  function renderSites() {
    const list = $('cloud-sites'); list.replaceChildren();
    const query = $('cloud-site-filter').value.trim().toLocaleLowerCase();
    const visible = sites.filter(s => s.name.toLocaleLowerCase().includes(query));
    if (!visible.length) { const p = document.createElement('p'); p.textContent = sites.length ? 'No matching sites.' : 'This account has no accessible sites.'; list.append(p); }
    for (const site of visible) {
      const button = document.createElement('button'); button.className = 'server-choice'; button.dataset.siteId = site.id;
      if (site.id === wantedSite) button.classList.add('selected-site');
      const name = document.createElement('strong'), detail = document.createElement('small');
      name.textContent = site.name;
      detail.textContent = [site.shared ? 'Shared / organisation' : 'Owned', site.version, site.system2faEnabled ? 'MFA required' : '', 'Open backup →'].filter(Boolean).join(' · ');
      button.append(name, detail); button.disabled = pending;
      button.addEventListener('click', () => start({action:'open', siteId:site.id, auth:'cloud'})); list.append(button);
    }
  }
  function updateAuth() {
    const cloud = $('server-auth').value === 'cloud';
    $('server-username-label').textContent = cloud ? 'Nx Cloud email' : 'Username';
    $('server-username').type = cloud ? 'email' : 'text';
    $('server-auth-hint').textContent = cloud ? 'Signs in at nxvms.com, then downloads directly from this server. Internet access is required for cloud sign-in.' : 'Use a local account with permission to create site backups.';
  }
  $('server-auth').addEventListener('change', updateAuth);
  $('server-address').addEventListener('input', () => { selectedServer = null; });
  $('cloud-site-filter').addEventListener('input', renderSites);
  async function poll() {
    if (!active || polling || localOpening) return;
    polling = true;
    try {
      const state = await call('ConnectionState');
      if (!active) return;
      if (state.stage === 'idle') return;
      enable(state.busy);
      $('connection-progress').hidden = ['idle'].includes(state.stage);
      $('connection-status').textContent = state.message;
      $('connection-meter').hidden = !state.busy || ['mfa','certificate','relay'].includes(state.stage);
      if (state.total > 0) { $('connection-meter').max = state.total; $('connection-meter').value = state.received; }
      else $('connection-meter').removeAttribute('value');
      $('connection-bytes').textContent = state.received > 0 ? (state.received / 1048576).toFixed(1) + ' MiB' + (state.total > 0 ? ' / ' + (state.total / 1048576).toFixed(1) + ' MiB' : '') : '';
      error(state.error || state.warning);
      $('certificate-prompt').hidden = state.stage !== 'certificate';
      $('mfa-form').hidden = state.stage !== 'mfa';
      $('relay-prompt').hidden = state.stage !== 'relay';
      const key = state.stage + ':' + state.promptId;
      if (key !== promptKey) {
        promptKey = key;
        $('submit-mfa').disabled = $('trust-certificate').disabled = $('use-relay').disabled = false;
        if (state.stage === 'mfa') { $('mfa-code').value = ''; $('mfa-code').focus(); }
        if (state.certificate) {
          $('certificate-details').replaceChildren();
          for (const [label, value] of [['Server',state.certificate.address],['SHA-256',state.certificate.fingerprint],['Issued to',state.certificate.subject],['Issued by',state.certificate.issuer],['Expires',state.certificate.expires]]) {
            const dt = document.createElement('dt'), dd = document.createElement('dd'); dt.textContent = label; dd.textContent = value; $('certificate-details').append(dt,dd);
          }
        }
      }
      if (state.stage === 'discovered') {
        const signature = JSON.stringify(state.servers);
        if (signature !== lastList) { renderServers(state.servers); lastList = signature; }
      }
      if (state.stage === 'sites') {
        const signature = JSON.stringify(state.sites);
        if (signature !== lastList) { sites = state.sites; renderSites(); lastList = signature; }
        $('cloud-form').hidden = true; $('cloud-sites-panel').hidden = false;
      }
      if (state.stage === 'error' && tab === 'cloud') {
        sites = []; lastList = ''; $('cloud-form').hidden = false; $('cloud-sites-panel').hidden = true;
      }
      if (state.stage === 'ready' && !state.busy) {
        const view = await call('View', {});
        if (!active) return;
        render(view); message(state.warning || 'Opened a temporary site backup. Use Save backup as… to keep a copy.');
        await close();
      }
    } catch (e) { error(e.message || String(e)); enable(false); }
    finally { polling = false; }
  }
  async function start(request) {
    if (pending) return;
    error(''); enable(true); lastList = ''; promptKey = '';
    try { await call('ConnectionStart', request); clearSecrets(); await poll(); }
    catch (e) { clearSecrets(); error(e.message || String(e)); enable(false); }
    finally { request.password = ''; }
  }
  async function close() {
    active = false; clearInterval(timer); timer = null; clearSecrets();
    sites = []; selectedServer = null; $('cloud-sites').replaceChildren(); $('certificate-details').replaceChildren();
    $('cloud-email').value = $('server-username').value = '';
    await call('ConnectionCancel'); dialog.close(); enable(false);
  }
  async function open(source) {
    if (dialog.open) return;
    active = true; pending = false; promptKey = ''; lastList = ''; wantedSite = source?.siteId || '';
    error(''); clearSecrets(); $('connection-progress').hidden = true;
    for (const id of ['certificate-prompt','mfa-form','relay-prompt','cloud-sites-panel']) $(id).hidden = true;
    $('cloud-form').hidden = false;
    await switchTab(source?.kind === 'server' ? source.address ? 'server' : 'cloud' : 'file');
    if (source?.address) { $('server-address').value = source.address; $('server-auth').value = source.auth; updateAuth(); }
    dialog.showModal(); timer = setInterval(poll, 300);
  }
  $('open-local-file').addEventListener('click', () => perform(async () => {
    localOpening = true;
    try { const view = await call('OpenDatabase'); if (view) { render(view); await close(); } }
    catch (e) { error(e.message || String(e)); }
    finally { localOpening = false; }
  }));
  $('discover-servers').addEventListener('click', () => start({action:'discover'}));
  $('server-form').addEventListener('submit', event => { event.preventDefault(); start({action:'open',address:$('server-address').value,auth:$('server-auth').value,username:$('server-username').value,password:$('server-password').value,siteId:selectedServer?.cloudSystemId || ''}); });
  $('cloud-form').addEventListener('submit', event => { event.preventDefault(); start({action:'cloud',username:$('cloud-email').value,password:$('cloud-password').value}); });
  $('cloud-switch').addEventListener('click', async () => { await call('ConnectionCancel'); sites=[]; lastList=''; $('cloud-sites-panel').hidden=true; $('cloud-form').hidden=false; $('connection-progress').hidden=true; $('cloud-email').focus(); });
  async function answer(button, value) { button.disabled = true; try { await call('ConnectionAnswer', value); } catch (e) { error(e.message); button.disabled = false; } }
  $('trust-certificate').addEventListener('click', () => answer($('trust-certificate'),'trust'));
  $('use-relay').addEventListener('click', () => answer($('use-relay'),'relay'));
  $('mfa-form').addEventListener('submit', event => { event.preventDefault(); const code = $('mfa-code').value; $('mfa-code').value = ''; answer($('submit-mfa'),code); });
  $('connection-cancel').addEventListener('click', () => close().catch(e => error(e.message)));
  dialog.addEventListener('cancel', event => { event.preventDefault(); close().catch(e => error(e.message)); });
  return {open, close};
}
