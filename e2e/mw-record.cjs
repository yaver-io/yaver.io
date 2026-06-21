const { chromium } = require('playwright');
const TOKEN = process.env.YMH_AUTH_TOKEN || '';
const USER = JSON.stringify({ id: 'u', email: 'kivanc.cakmak@icloud.com', name: 'Kivanc' });
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 414, height: 896 }, recordVideo: { dir: '/tmp/todo-eval/appvid', size: { width: 414, height: 896 } } });
  await ctx.addInitScript(([t, u]) => { try { localStorage.setItem('securestore.yaver_auth_token', t); localStorage.setItem('securestore.yaver_user', u); } catch (e) {} }, [TOKEN, USER]);
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,90)));
  await p.goto('http://localhost:8082', { waitUntil: 'load', timeout: 180000 }).catch(e => console.log('goto', String(e).slice(0,80)));
  await p.waitForTimeout(13000);
  const home = (await p.evaluate(() => document.body.innerText).catch(()=> '')).replace(/\s+/g,' ').slice(0,200);
  console.log('HOME:', JSON.stringify(home));
  await p.screenshot({ path: '/tmp/todo-eval/app-home.png' });
  const routes = ['/sandbox-ai','/phone-projects','/agent','/local-models','/assistant','/'];
  let i=0;
  for (const r of routes) {
    try {
      await p.goto('http://localhost:8082' + r, { waitUntil: 'load', timeout: 30000 });
      await p.waitForTimeout(4500);
      const t = (await p.evaluate(() => document.body.innerText).catch(()=> '')).replace(/\s+/g,' ').slice(0,140);
      console.log('ROUTE', r, JSON.stringify(t));
      await p.screenshot({ path: `/tmp/todo-eval/app-route-${i}.png` });
    } catch (e) { console.log('ROUTE', r, 'ERR', String(e).slice(0,60)); }
    i++;
  }
  await ctx.close(); await b.close();
})();
