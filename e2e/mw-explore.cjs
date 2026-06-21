const { chromium } = require('playwright');
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 414, height: 896 } });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,90)));
  const dump = async (tag) => {
    const txt = (await p.evaluate(()=>document.body.innerText).catch(()=> '')).replace(/\s+/g,' ').slice(0,260);
    const inputs = await p.evaluate(()=>[...document.querySelectorAll('input,textarea')].map(e=>e.placeholder||e.type||'input').slice(0,12)).catch(()=>[]);
    console.log(`[${tag}] url=${p.url().replace('http://localhost:8082','')}`);
    console.log(`   text: ${JSON.stringify(txt)}`);
    console.log(`   inputs: ${JSON.stringify(inputs)}`);
  };
  await p.goto('http://localhost:8082/phone-projects', { waitUntil:'load', timeout:120000 });
  await p.waitForTimeout(6000); await dump('phone-projects');
  // click "New mobile app"
  try { await p.getByText(/New mobile app/i).first().click({timeout:8000}); await p.waitForTimeout(4000); await dump('after-new'); await p.screenshot({path:'/tmp/todo-eval/exp-new.png'}); } catch(e){ console.log('newclick', String(e).slice(0,80)); }
  // try fill a name + create
  try { const inp = p.locator('input').first(); if (await inp.count()) { await inp.fill('Demo App'); await p.waitForTimeout(800); } } catch(e){}
  // dump any buttons available
  const btns = await p.evaluate(()=>[...document.querySelectorAll('[role=button],button,[data-testid]')].map(e=>(e.innerText||e.getAttribute('aria-label')||'').trim()).filter(Boolean).slice(0,20)).catch(()=>[]);
  console.log('BUTTONS:', JSON.stringify(btns));
  await b.close();
})();
