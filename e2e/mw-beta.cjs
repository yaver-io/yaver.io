const { chromium } = require('playwright');
const fs = require('fs');
const TOK = fs.readFileSync('/tmp/betatest_tok.txt','utf8').trim();
const YGW = fs.readFileSync('/tmp/managed_ygw.txt','utf8').trim();
const GW = 'https://yaver-gateway.kivanccakmak.workers.dev/v1';
const sleep = ms => new Promise(r=>setTimeout(r,ms));
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport:{width:414,height:896}, recordVideo:{dir:'/tmp/beta-eval/vid', size:{width:414,height:896}} });
  const p = await ctx.newPage();
  p.on('pageerror', e => console.log('ERR', String(e).slice(0,70)));
  const tap = async (re,t=7000)=>{ try{ const el=p.getByText(re).first(); await el.waitFor({timeout:t}); await el.scrollIntoViewIfNeeded().catch(()=>{}); const x=await el.boundingBox(); if(!x)return false; await p.mouse.click(x.x+x.width/2,x.y+Math.min(x.height/2,16)); console.log('tap',re); await sleep(1100); return true;}catch(e){console.log('miss',re);return false;} };
  const type=async(loc,t)=>{try{const l=loc.first();await l.click({timeout:5000});await l.pressSequentially(t,{delay:35});await sleep(600);return true;}catch(e){return false;}};
  const shot=n=>p.screenshot({path:`/tmp/beta-eval/b-${n}.png`}).catch(()=>{});
  await p.goto('http://localhost:8082/',{waitUntil:'load',timeout:180000}); await sleep(2500);
  // inject beta-user auth + managed-GLM creds
  await p.evaluate(([tok,ygw,gw])=>{ localStorage.setItem('securestore.yaver_auth_token',tok); localStorage.setItem('securestore.yaver_user', JSON.stringify({userId:'bc784df3fd638f52aeeaea335f233b9b',email:'betatester@yaver.io',fullName:'Beta Tester'})); localStorage.setItem('securestore.yaver_key_gateway_url',gw); localStorage.setItem('securestore.yaver_key_managed_inference_token',ygw); }, [TOK,YGW,GW]);
  await p.goto('http://localhost:8082/phone-projects',{waitUntil:'load',timeout:120000}); await sleep(4000); await shot('open');
  await tap(/New mobile app/i); await sleep(1200);
  await type(p.getByPlaceholder(/app|name|My app/i),'My Todo'); await tap(/^Next$/i);    // git
  if(!await tap(/Skip git/i)) await tap(/Skip/i); await tap(/^Next$/i);                                            // where
  await tap(/This phone/i); await sleep(500); await shot('beta-radio');
  await tap(/Beta access/i); await sleep(600);                                             // managed GLM
  await tap(/^Next$/i);                                                                     // survey
  await tap(/Web and mobile/i).catch(()=>{}); await tap(/^Next$/i);                         // setting-up
  await sleep(6000); await tap(/^Next$/i);                                                  // branding
  await tap(/Ocean/i).catch(()=>{}); await tap(/^Next$/i);                                  // describe
  await sleep(600);
  await type(p.locator('textarea'),'A simple todo app: add a task, mark it done, delete it. Saved on the device.');
  await shot('describe');
  await tap(/^Create sandbox$/i, 8000);
  console.log('waiting for managed GLM generation...');
  for(let i=0;i<14;i++){ await sleep(2500); await shot('gen-'+i); }
  console.log('FINAL:', (await p.evaluate(()=>document.body.innerText).catch(()=>'')).replace(/\s+/g,' ').slice(0,240));
  await sleep(1500); await ctx.close(); await b.close();
})();
