const { chromium } = require('playwright');
const GLM = process.env.GLM_KEY || '';
const sleep = ms => new Promise(r=>setTimeout(r,ms));
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport:{width:414,height:896}, recordVideo:{dir:'/tmp/todo-eval/fullvid', size:{width:414,height:896}} });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,60)));
  // coordinate-based tap (reliable for RN-web Pressables)
  const tap = async (re, t=6000) => {
    try { const el = p.getByText(re).first(); await el.waitFor({timeout:t}); await el.scrollIntoViewIfNeeded().catch(()=>{});
      const box = await el.boundingBox(); if(!box){console.log('nobox',re);return false;}
      await p.mouse.click(box.x+box.width/2, box.y+Math.min(box.height/2,14)); console.log('tap',re); await sleep(1100); return true;
    } catch(e){ console.log('miss',re); return false; }
  };
  const typeInto = async (loc,txt)=>{ try{const l=loc.first(); await l.click({timeout:5000}); await l.pressSequentially(txt,{delay:45}); console.log('typed',txt.length+'ch'); await sleep(700); return true;}catch(e){console.log('type-miss');return false;} };
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:120000}); await sleep(3500);
  await tap(/New mobile app/i); await sleep(1200);
  await typeInto(p.getByPlaceholder(/app|name|My app/i),'Todo Notes'); await tap(/^Next$/i);
  await tap(/Skip git/i); await tap(/^Next$/i);
  await tap(/This phone/i); await tap(/^GLM$/i); await sleep(500);
  if(GLM){ if(!await typeInto(p.getByPlaceholder(/key|api|sk-|z\.ai/i),GLM)) await typeInto(p.locator('input'),GLM); }
  await tap(/^Next$/i);
  // survey: answer 6 (coordinate clicks)
  for(let q=0;q<6;q++){ await sleep(700);
    // click first answer option then advance
    const opts=[/Web and mobile/i,/Mobile only/i,/Web only/i,/Yes/i,/No/i,/Solo/i,/Free/i,/English/i,/Simple/i,/Both/i];
    for(const o of opts){ if(await tap(o,1800)) break; }
    if(!await tap(/Next question/i,2500)){ await tap(/Skip survey/i,2000); break; }
  }
  await sleep(1000);
  // describe + build
  let d=await typeInto(p.locator('textarea'),'A simple todo notes app: add a task, mark it done, delete it; saved on the device.');
  if(!d) await typeInto(p.getByPlaceholder(/describe|building/i),'A simple todo app: add, complete, delete; saved locally.');
  await p.screenshot({path:'/tmp/todo-eval/full2-describe.png'});
  await tap(/Build|Generate|Create app|Finish|Make/i,8000);
  await sleep(20000);
  await p.screenshot({path:'/tmp/todo-eval/full2-built.png'});
  console.log('FINAL:',JSON.stringify((await p.evaluate(()=>document.body.innerText).catch(()=>'')).replace(/\s+/g,' ').slice(0,240)));
  await sleep(2500); await ctx.close(); await b.close();
})();
