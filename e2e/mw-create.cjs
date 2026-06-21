const { chromium } = require('playwright');
const GLM = process.env.GLM_KEY || '';
const sleep = ms => new Promise(r=>setTimeout(r,ms));
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport:{width:414,height:896}, recordVideo:{dir:'/tmp/todo-eval/createvid', size:{width:414,height:896}} });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,60)));
  const tap = async (re,t=6000)=>{ try{ const el=p.getByText(re).first(); await el.waitFor({timeout:t}); await el.scrollIntoViewIfNeeded().catch(()=>{}); const box=await el.boundingBox(); if(!box)return false; await p.mouse.click(box.x+box.width/2, box.y+Math.min(box.height/2,16)); console.log('tap',re); await sleep(1100); return true; }catch(e){ console.log('miss',re); return false; } };
  const typeInto=async(loc,txt)=>{ try{const l=loc.first(); await l.click({timeout:5000}); await l.pressSequentially(txt,{delay:42}); console.log('typed',txt.length); await sleep(700); return true;}catch(e){console.log('type-miss');return false;} };
  const shot=(n)=>p.screenshot({path:`/tmp/todo-eval/nf-${n}.png`}).catch(()=>{});
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:180000}); await sleep(3500);
  await tap(/New mobile app/i); await sleep(1200);
  await typeInto(p.getByPlaceholder(/app|name|My app/i),'Todo Notes'); await tap(/^Next$/i);   // ->git
  await tap(/Skip git/i); await tap(/^Next$/i);                                                 // ->run
  await tap(/This phone/i); await tap(/^GLM$/i); await sleep(400);
  if(GLM){ if(!await typeInto(p.getByPlaceholder(/key|api|sk-|z\.ai/i),GLM)) await typeInto(p.locator('input'),GLM); }
  await tap(/^Next$/i);                              // ->survey (3)
  await tap(/Web and mobile/i); await tap(/^Next$/i); // ->setting-up (4)
  await shot('setup-a'); await sleep(6000); await shot('setup-b');   // watch pong
  await tap(/^Next$/i);                              // ->branding (5)
  await sleep(800); await shot('branding-a');
  await tap(/Ocean/i); await sleep(800); await shot('branding-b');   // Canva palette pick
  await tap(/^Next$/i);                              // ->describe (6)
  await sleep(800);
  let d=await typeInto(p.locator('textarea'),'A simple todo notes app: add a task, mark it done, delete it; saved on the device.');
  if(!d) await typeInto(p.getByPlaceholder(/describe|building/i),'A simple todo app.');
  await shot('describe');
  await tap(/^Create sandbox$/i, 8000);
  for(let i=0;i<13;i++){ await sleep(2200); await shot('build-'+i); }
  console.log('FINAL:', JSON.stringify((await p.evaluate(()=>document.body.innerText).catch(()=>'')).replace(/\s+/g,' ').slice(0,200)));
  await sleep(2000); await ctx.close(); await b.close();
})();
