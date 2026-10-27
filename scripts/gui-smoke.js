(async () => {
  const sleep = ms => new Promise(resolve => setTimeout(resolve,ms));
  const wait = async (predicate, label) => { for(let i=0;i<300;i++){ if(predicate()) return; await sleep(100); } throw new Error('Timeout: '+label); };
  await wait(() => window.appReady, 'application ready');
  const { Call } = await import('/wails/runtime.js');
  const harness = (name,...args) => Call.ByName('main.GUIHarness.'+name,...args);
  const $ = id => document.getElementById(id);
  const assert = (condition,message) => { if(!condition) throw new Error(message); };
  const click = async id => { $(id).click(); await sleep(100); await wait(() => document.body.getAttribute('aria-busy') === 'false',id); };
  try {
    assert($('export').disabled, 'export enabled without database');
    await harness('Select','cancel'); await click('open'); assert($('filename').textContent === 'No database selected','cancel changed database');
    await harness('Select','normal'); await click('open');
    assert($('rows').children.length === 8,'wrong camera count');
    assert(!document.body.textContent.includes('demo-password'),'password leaked in masked view');
    await sleep(300); await harness('Capture','inventory');
    await click('reveal'); assert($('rows').textContent.includes('demo-password'),'reveal failed');
    document.querySelector('.row-details').click();
    assert($('details-dialog').open && $('details').textContent.includes('demo-password'),'camera details failed');
    $('details-dialog').close();
    await click('reveal'); assert(!document.body.textContent.includes('demo-password'),'hide failed');
    $('filter').value='Hikvision'; await click('apply-filter'); assert($('rows').children.length===2,'filter failed');
    await sleep(200); await harness('Capture','filtered');
    await click('export'); assert($('export-dialog').open,'export dialog missing');
    await sleep(200); await harness('Capture','export');
    await click('save-export'); const csv = await harness('ReadExport','csv');
    assert(csv.includes('Hikvision')&&!csv.includes('demo-password')&&!csv.includes('Reception'),'masked filtered CSV incorrect');
    await click('reveal'); await click('export'); document.querySelector('input[value=json]').checked=true;
    await click('save-export'); const exported = JSON.parse(await harness('ReadExport','json'));
    assert(exported.cameras.length===2 && exported.cameras[0].password==='demo-password','revealed JSON incorrect');
    await click('export'); await click('save-export'); assert(!$('error').hidden&&$('error').textContent.includes('already exists'),'overwrite not refused');
    await click('clear-filter'); await click('virtual'); assert($('rows').children.length===9,'virtual camera toggle failed');
    await click('refresh'); assert($('rows').children.length===9,'refresh lost options');
    await harness('Select','invalid'); await click('open'); assert(!$('error').hidden,'invalid database not reported'); assert($('rows').children.length===9,'invalid open discarded snapshot');
    await click('close'); assert($('rows').children.length===0 && $('export').disabled,'close did not clear data');
    await harness('Done','');
  } catch(error) { await harness('Done',error.stack || String(error)); }
})().catch(error => console.error(error));
