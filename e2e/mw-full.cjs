const { chromium } = require('playwright');
const GLM = process.env.GLM_KEY || '';
const sleep = ms => new Promise(r=>setTimeout(r,ms));
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport:{width:414,height:896}, recordVideo:{dir:'/tmp/todo-eval/fullvid', size:{width:414,height:896}} });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,70)));
  const tap = async (re, t=6000) => { try { await p.getByText(re).first().click({timeout:t}); console.log('tap', re); await sleep(1200); return true; } catch(e){ console.log('miss', re); return false; } };
  const typeInto = async (loc, txt) => { try { const l=loc.first(); await l.scrollIntoViewIfNeeded({timeout:3000}).catch(()=>{}); await l.click({timeout:5000}); await l.pressSequentially(txt,{delay:50}); console.log('typed', txt.length+'ch'); await sleep(800);return true; } catch(e){ console.log('type-miss', String(e).slice(0,45)); return false; } };
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:120000}); await sleep(3500);
  await tap(/New mobile app/i); await sleep(1500);
  // 1. Name
  await typeInto(p.getByPlaceholder(/app|name|My app/i), 'Todo Notes');
  await tap(/^Next$/i);
  // 2. Git -> skip
  await tap(/Skip git/i); await tap(/^Next$/i);
  // 3. Where to run + GLM + key
  await tap(/This phone/i); await tap(/^GLM$/i); await sleep(600);
  if (GLM) { let ok=await typeInto(p.getByPlaceholder(/key|api|sk-|gsk|z\.ai/i), GLM); if(!ok) await typeInto(p.locator('input'), GLM); }
  await tap(/^Next$/i);
  // 4. Survey -> SKIP
  await sleep(800); await tap(/Skip survey/i); await sleep(800);
  // 5. Describe + Build
  await sleep(1000);
  let d = await typeInto(p.locator('textarea'), 'A simple todo notes app: add a task, mark it done, delete it; saved on the device.');
  if(!d) await typeInto(p.getByPlaceholder(/describe|building|app/i), 'A simple todo notes app: add, complete, delete; saved locally.');
  await p.screenshot({path:'/tmp/todo-eval/full-describe.png'});
  await tap(/Build|Generate|Create app|Finish|Make it/i, 8000);
  await sleep(18000);
  await p.screenshot({path:'/tmp/todo-eval/full-built.png'});
  console.log('FINAL:', JSON.stringify((await p.evaluate(()=>document.body.innerText).catch(()=>'')).replace(/\s+/g,' ').slice(0,220)));
  await sleep(2500); await ctx.close(); await b.close();
})();
