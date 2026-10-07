import {test,expect} from '@playwright/test';
import fs from 'node:fs';
test('browser SSH works without Yaver auth and preserves pane across views',async({browser},info)=>{
 test.skip(!process.env.PLAIN_SSH_WEB_URL||!process.env.PLAIN_SSH_BROWSER_FIXTURE,'real SSH fixture and web URL required');
 const f=JSON.parse(fs.readFileSync(process.env.PLAIN_SSH_BROWSER_FIXTURE!,'utf8'));
 const context=await browser.newContext({viewport:{width:1280,height:850}});const page=await context.newPage();
 const opens:string[]=[];page.on('request',r=>{if(r.url().endsWith('/invoke')&&r.postDataJSON()?.op==='open')opens.push(r.postData()!)});
 try{
 await page.goto(process.env.PLAIN_SSH_WEB_URL!+'/ssh');
 await page.getByRole('button',{name:'Use local SSH companion'}).click();
 await page.getByLabel('Hostname or Tailscale address').fill(f.host);await page.getByLabel('SSH port',{exact:true}).fill(String(f.port));await page.getByLabel('SSH username',{exact:true}).fill(f.user);await page.getByLabel('SSH password (optional with a key or Tailscale SSH)').fill(f.password);
 await page.getByRole('button',{name:'Check host key'}).click();await expect(page.getByText(f.fingerprint,{exact:true})).toBeVisible();await page.getByRole('button',{name:'Trust host and connect'}).click();await page.getByRole('button',{name:/work · 0 · %/}).click();
 await expect(page.locator('.xterm-screen')).toBeVisible();await page.getByLabel('Message to selected pane').fill('web-pane-proof');await page.getByRole('button',{name:'Send',exact:true}).click();await page.getByRole('tab',{name:'Pane chat'}).click();await expect(page.getByTestId('live-pane-output')).toContainText('web-pane-proof');
 await page.getByRole('button',{name:'Yaver',exact:true}).click();await page.getByRole('button',{name:'Sign this remote into Yaver'}).click();await expect(page.getByText('Remote test account setup complete',{exact:false})).toBeVisible();
 const sendBox=await page.getByRole('button',{name:'Send',exact:true}).boundingBox();expect(sendBox).not.toBeNull();expect(sendBox!.y+sendBox!.height).toBeLessThanOrEqual(850);await page.screenshot({path:info.outputPath('web-pane-chat.png')});await page.getByRole('tab',{name:'Raw',exact:true}).click();await expect(page.locator('.xterm-screen')).toBeVisible();expect(opens).toHaveLength(1);await page.getByRole('button',{name:'Detach',exact:true}).click();await expect(page.getByLabel('SSH username',{exact:true})).toBeVisible();
 }finally{await context.close()}
});
