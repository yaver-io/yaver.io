import {test,expect} from '@playwright/test';
import fs from 'node:fs';
test('browser SSH works without Yaver auth as a full-screen raw terminal',async({browser},info)=>{
 test.skip(!process.env.PLAIN_SSH_WEB_URL||!process.env.PLAIN_SSH_BROWSER_FIXTURE,'real SSH fixture and web URL required');
 const f=JSON.parse(fs.readFileSync(process.env.PLAIN_SSH_BROWSER_FIXTURE!,'utf8'));
 const context=await browser.newContext({viewport:{width:1280,height:850}});const page=await context.newPage();
 const opens:string[]=[];page.on('request',r=>{if(r.url().endsWith('/invoke')&&r.postDataJSON()?.op==='open')opens.push(r.postData()!)});
 try{
 await page.goto(process.env.PLAIN_SSH_WEB_URL!+'/ssh');
 await page.getByRole('button',{name:'Use local SSH companion'}).click();
 await page.getByLabel('Hostname or Tailscale address').fill(f.host);await page.getByLabel('SSH port',{exact:true}).fill(String(f.port));await page.getByLabel('SSH username',{exact:true}).fill(f.user);await page.getByLabel('SSH password (optional with a key or Tailscale SSH)').fill(f.password);
 await page.getByRole('button',{name:'Check host key'}).click();await expect(page.getByText(f.fingerprint,{exact:true})).toBeVisible();await page.getByRole('button',{name:'Trust host and connect'}).click();await page.getByRole('button',{name:/work · 0 · %/}).click();
 const terminal=page.locator('.xterm-screen');await expect(terminal).toBeVisible();await terminal.click();await page.keyboard.type('web-pane-proof');await page.keyboard.press('Enter');await expect(page.locator('.xterm-rows')).toContainText('web-pane-proof');
 await expect(page.getByRole('tab')).toHaveCount(0);await expect(page.getByLabel('Message to selected pane')).toHaveCount(0);const box=await page.getByTestId('plain-ssh-terminal').boundingBox();expect(box).not.toBeNull();expect(box!.width).toBe(1280);expect(box!.height).toBe(850);await page.screenshot({path:info.outputPath('web-pane-fullscreen.png')});expect(opens).toHaveLength(1);await page.getByRole('button',{name:'Close pane',exact:true}).click();await expect(page.getByLabel('SSH username',{exact:true})).toBeVisible();
 }finally{await context.close()}
});
