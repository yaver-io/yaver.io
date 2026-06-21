const { chromium } = require('playwright');
const fs = require('fs');
const TOK = fs.readFileSync('/tmp/betatest_tok.txt','utf8').trim();
const YGW = fs.readFileSync('/tmp/managed_ygw.txt','utf8').trim();
const GW = 'https://yaver-gateway.kivanccakmak.workers.dev/v1';
const sleep = ms => new Promise(r=>setTimeout(r,ms));
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport:{width:414,height:896}, recordVideo:{dir:'/tmp/beta-eval/vid2', size:{width:414,height:896}} });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('PAGEERR', String(e).slice(0,80)));
  p.on('dialog', d => { console.log('DIALOG:', d.message().slice(0,90)); d.accept().catch(()=>{}); });
  // robust click: prefer element.click(), fall back to coordinate
  const click = async (re,t=8000)=>{ try{ const el=p.getByText(re).first(); await el.waitFor({timeout:t}); await el.scrollIntoViewIfNeeded().catch(()=>{}); await el.click({timeout:4000}).catch(async()=>{ const x=await el.boundingBox(); if(x) await p.mouse.click(x.x+x.width/2,x.y+x.height/2); }); console.log('click',String(re)); await sleep(1200); return true;}catch(e){console.log('MISS',String(re));return false;} };
  const type=async(loc,t)=>{try{const l=loc.first();await l.click({timeout:5000});await l.pressSequentially(t,{delay:30});await sleep(700);return true;}catch(e){console.log('type-miss');return false;}};
  const shot=n=>p.screenshot({path:`/tmp/beta-eval/f-${n}.png`}).catch(()=>{});
  await p.goto('http://localhost:8082/',{waitUntil:'load',timeout:180000}); await sleep(2500);
  await p.evaluate(([tok,ygw,gw])=>{ localStorage.setItem('securestore.yaver_auth_token',tok); localStorage.setItem('securestore.yaver_user', JSON.stringify({userId:'bc784df3fd638f52aeeaea335f233b9b',email:'betatester@yaver.io',fullName:'Beta Tester'})); localStorage.setItem('securestore.yaver_key_gateway_url',gw); localStorage.setItem('securestore.yaver_key_managed_inference_token',ygw); }, [TOK,YGW,GW]);
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:120000}); await sleep(4500); await shot('00-open');
  await click(/New mobile app/i); await sleep(1200); await shot('01-name');
  await type(p.getByPlaceholder(/app|name|My app/i),'My Todo'); await click(/^Next$/i);     // git
  await shot('02-git');
  await click(/^Next$/i);                                                                     // (yaver-managed default) → where
  await click(/This phone/i); await sleep(800); await shot('03-beta-radio');
  await click(/Beta access/i); await sleep(800); await shot('04-managed');
  await click(/^Next$/i);                                                                      // survey
  await click(/Web and mobile/i).catch(()=>{}); await click(/^Next$/i);                        // setting-up
  await sleep(6500); await shot('05-setup'); await click(/^Next$/i);                            // branding
  await click(/Ocean/i).catch(()=>{}); await sleep(600); await shot('06-branding'); await click(/^Next$/i); // describe
  await sleep(700);
  await type(p.locator('textarea'),'A simple todo app: add a task, mark it done, delete it. Saved on the device.');
  await shot('07-describe');
  await click(/^Create sandbox$/i, 9000);
  console.log('generating (managed GLM)...');
  for(let i=0;i<18;i++){ await sleep(2500); await shot('08-gen-'+i); }
  console.log('FINAL:', (await p.evaluate(()=>document.body.innerText).catch(()=>'')).replace(/\s+/g,' ').slice(0,260));
  await sleep(2000); await ctx.close(); await b.close();
})();
