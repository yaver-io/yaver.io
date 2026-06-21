const { chromium } = require('playwright');
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 414, height: 896 } });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,80)));
  const dump = async (t) => { const x=(await p.evaluate(()=>document.body.innerText).catch(()=>'')).replace(/\s+/g,' ').slice(0,300); console.log(`[${t}] ${p.url().replace('http://localhost:8082','')} :: ${JSON.stringify(x)}`); };
  const clickText = async (re) => { try { await p.getByText(re).first().click({timeout:6000}); return true; } catch(e){ return false; } };
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:120000}); await p.waitForTimeout(5000);
  await clickText(/New mobile app/i); await p.waitForTimeout(2500);
  try { await p.locator('input').first().fill('Demo Todo'); } catch(e){}
  await p.waitForTimeout(800);
  // walk wizard: try common proceed labels several times
  for (let step=0; step<5; step++) {
    let clicked=false;
    for (const lbl of [/^Create$/i,/Continue/i,/^Next$/i,/Done/i,/Generate/i,/Create app/i,/Build/i,/Finish/i,/Start/i,/Blank/i,/Skip/i]) {
      if (await clickText(lbl)) { clicked=true; await p.waitForTimeout(3000); await dump('step'+step+' clicked '+lbl); await p.screenshot({path:`/tmp/todo-eval/wiz-${step}.png`}); break; }
    }
    if (!clicked) { await dump('step'+step+' no-proceed'); break; }
  }
  // back to projects, open first project
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:60000}); await p.waitForTimeout(3000); await dump('projects-after');
  // try open a project card (text "Demo Todo")
  if (await clickText(/Demo Todo/i)) { await p.waitForTimeout(3500); await dump('opened-project'); await p.screenshot({path:'/tmp/todo-eval/proj-open.png'}); }
  // find Code / Edit / Ask AI
  for (const lbl of [/Code/i,/Edit/i,/Open/i]) { if (await clickText(lbl)) { await p.waitForTimeout(3000); await dump('after '+lbl); await p.screenshot({path:'/tmp/todo-eval/editor.png'}); break; } }
  await b.close();
})();
