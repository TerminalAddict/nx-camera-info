const $ = id => document.getElementById(id);
const size = bytes => `${(bytes / 1048576).toFixed(1)} MB`;

export async function initialiseAbout(info, call) {
  const platform = {linux: 'Linux', windows: 'Windows', darwin: 'macOS'}[info.platform] || info.platform;
  const arch = info.architecture === 'arm64' ? (info.platform === 'darwin' ? 'Apple Silicon' : 'ARM64') : (info.platform === 'darwin' ? 'Intel' : 'Intel / AMD 64-bit');
  $('about-version').textContent = info.version;
  $('about-platform').textContent = `${platform} · ${arch}`;
  $('about-build').textContent = `NX Camera Info ${info.version} · ${info.goVersion} · Wails ${info.wailsVersion} · ${info.platform}/${info.architecture}`;
  let state = await call('UpdateState'), active = false;

  function render(next) {
    state = next;
    const pending = ['checking', 'choosing', 'downloading'].includes(state.status);
    $('check-updates').disabled = active || pending;
    $('check-updates').textContent = state.status === 'checking' ? 'Checking…' : 'Check for updates';
    $('update-status').textContent = state.message;
    $('update-error').textContent = state.error || '';
    $('update-error').hidden = !state.error;
    const downloads = state.downloads || [];
    const offer = !pending && state.status === 'available' && downloads.length;
    $('update-offer').hidden = !offer;
    // Keep the chosen package while polling and after a failed download.
    const previous = $('update-package').value;
    $('update-package').replaceChildren(...downloads.map(asset => {
      const option = document.createElement('option');
      option.value = asset.name; option.textContent = `${asset.label} · ${size(asset.size)}`;
      return option;
    }));
    if (downloads.some(d => d.name === previous)) $('update-package').value = previous;
    $('download-update').disabled = active || !offer;
    $('update-progress-wrap').hidden = state.status !== 'downloading';
    $('update-progress').value = state.total ? Math.min(100, state.received / state.total * 100) : 0;
    $('update-progress-label').textContent = `${size(state.received)} / ${size(state.total)}`;
    $('update-saved').hidden = state.status !== 'ready';
    $('update-path').textContent = state.savedPath || '';
    $('release-link').hidden = !state.releaseURL;
  }

  function showAbout() {
    // Native menu actions can arrive while another modal is open.
    for (const dialog of document.querySelectorAll('dialog[open]')) {
      if (dialog.id !== 'about-dialog') dialog.close();
    }
    if (!$('about-dialog').open) $('about-dialog').showModal();
  }

  async function action(method, ...args) {
    if (active) return;
    active = true;
    $('about-error').hidden = true;
    render({...state, status: method === 'CheckForUpdates' ? 'checking' : 'choosing', message: method === 'CheckForUpdates' ? 'Checking GitHub…' : 'Choose where to save the installer.', error: ''});
    let polling = true;
    // Poll serially so a slow bridge cannot queue overlapping status requests.
    const poll = async () => {
      while (polling) {
        await new Promise(resolve => setTimeout(resolve, 350));
        if (!polling) break;
        try { const next = await call('UpdateState'); if (polling) render(next); } catch { /* The action reports bridge errors below. */ }
      }
    };
    void poll();
    try {
      const next = await call(method, ...args);
      polling = false; active = false; render(next);
    } catch (error) {
      polling = false; active = false;
      render({...state, status: 'error', message: 'The update action did not complete.', error: error.message || String(error)});
    }
  }

  async function external(method, ...args) {
    $('about-error').hidden = true;
    try { await call(method, ...args); }
    catch (error) { $('about-error').textContent = error.message || String(error); $('about-error').hidden = false; }
  }

  $('about-open').addEventListener('click', showAbout);
  $('updates-open').addEventListener('click', () => { showAbout(); void action('CheckForUpdates'); });
  $('check-updates').addEventListener('click', () => action('CheckForUpdates'));
  $('download-update').addEventListener('click', () => action('DownloadUpdate', $('update-package').value));
  $('cancel-update').addEventListener('click', () => external('CancelUpdateDownload'));
  $('show-update').addEventListener('click', () => external('ShowUpdateDownload'));
  document.querySelectorAll('[data-link]').forEach(button => button.addEventListener('click', () => external('OpenLink', button.dataset.link)));
  render(state);
}
