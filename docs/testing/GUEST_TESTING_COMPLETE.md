# 🎯 Yaver Guest Testing: Mobile App → Hetzner → Codex + Chrome Workflow - COMPLETE

## ✅ Setup Verification Complete

**Hetzner Server Status:**
- **IP:** `203.0.113.10`
- **Chrome:** Chromium 149.0.7827.114 ✅
- **Chromedriver:** 149.0.7827.114 ✅
- **Status:** **Ready for automation** ✅
- **Chromedriver Port:** 9515
- **Chrome DevTools Port:** 9222 (for direct WebSocket connection)

**Guest Account:**
- **Email:** `test-guest-hetzner@yaver.io`
- **Invite Code:** `EXAMPLE-CODE`
- **Scope:** Full teammate access ✅
- **Browser Control:** Enabled ✅
- **Desktop Control:** Enabled ✅
- **Allowed Runners:** Codex ✅
- **Resource Preset:** desktop-control-with-host-keys ✅

**Test Environment:**
- **Directory:** `/tmp/guest-test/` on Hetzner
- **Sample Tests Created:**
  - `chromedp_screenshot.go` (Go + Chromedp)
  - `selenium_test.py` (Python + Selenium)
  - `puppeteer_test.js` (Node.js + Puppeteer)
- **Documentation:** Complete guides included

---

## 📱 Mobile App Testing Instructions (for Guest)

### **Phase 1: Initial Setup (One-time)**
1. Download Yaver app from:
   - **iOS:** App Store → search "Yaver"
   - **Android:** Google Play Store → search "Yaver"
2. Sign in with any method (Apple, Google, Microsoft, or email)
3. Enter invite code: **`EXAMPLE-CODE`**
4. You now have full teammate access! ✅

### **Phase 2: Machine Selection**
1. In Yaver app → **"Devices"** tab
2. Browse available devices (MacBook, Linux servers, etc.)
3. **Select any online machine** (test requires Chrome/chromedriver)
4. Wait for connection confirmation

### **Phase 3: Run Chrome Automation Task (with Codex)**
1. Tap **"Create Task"** in mobile app
2. Select **"Codex"** as the runner
3. **Copy & paste this exact prompt:**

```
"Navigate to /tmp/guest-test directory and run this Chrome automation test:

First, verify chromedriver is running:
- Run: 'ps aux | grep chromedriver' (should show process)
- Run: 'netstat -tlnp | grep 9515' (should show LISTENING)

Then run one of these tests (choose based on your preference):

1. Go Chromedp Test:
   - Run: 'go run chromedp_screenshot.go'
   - This creates screenshot.png and shows page info
   - Dependencies: go get github.com/chromedp/chromedp

2. Python Selenium Test:
   - Run: 'python3 selenium_test.py'
   - This creates google_screenshot.png and tests Google
   - Dependencies: pip3 install selenium

3. Node Puppeteer Test:
   - Run: 'node puppeteer_test.js'
   - This creates output.pdf and example_screenshot.png
   - Dependencies: npm install puppeteer

Please verify:
- The output file was created
- The file contains expected content
- Show me the file size

Return the complete test results to me with any errors encountered."
```

4. **Wait for Codex to complete** (usually 1-3 minutes)
5. **Review results** in mobile app

---

## 🔍 Expected Test Results

### **Chromedp Test (Go):**
```
✅ Successfully navigated to example.com
✅ Screenshot saved as screenshot.png
✅ Page title: Example Domain
✅ Page heading: Example Domain
🎉 Chrome automation test completed successfully!
```

### **Selenium Test (Python):**
```
🚀 Chrome automation test started
📍 Navigating to Google.com...
📄 Page title: Google
✅ Page title verification passed
📸 Screenshot saved: /tmp/google_screenshot.png
✅ Screenshot test passed
✅ Successfully interacted with search box
🎉 All Chrome automation tests passed successfully!
```

### **Puppeteer Test (Node.js):**
```
🚀 Starting Chrome automation test...
✅ Connected to Chrome on Hetzner
📍 Navigating to example.com...
📄 Page title: Example Domain
✅ Page title verification passed
📸 Screenshot saved: /tmp/example_screenshot.png
✅ Screenshot test passed
📄 PDF generated: /tmp/example.pdf
✅ PDF generation test passed
📝 Page heading: Example Domain
✅ Content extraction test passed
🎉 All Chrome automation tests passed successfully!
```

---

## 🛠️ Technical Details

### **Hetzner Server Configuration:**
- **OS:** Ubuntu 24.04 (arm64)
- **Chrome Binary:** `/snap/bin/chromium`
- **Chromedriver:** `/usr/local/bin/chromedriver` → `/snap/chromium/current/usr/lib/chromium-browser/chromedriver`
- **Chrome Version:** 149.0.7827.114
- **Headless Mode:** Supported with `--headless --no-sandbox --disable-dev-shm-usage --disable-gpu`

### **Remote Connection Details:**
- **Chromedriver:** `http://203.0.113.10:9515`
- **Chrome DevTools:** `ws://203.0.113.10:9222`
- **Network:** Hetzner needs to be accessible from guest machine

### **Library Dependencies:**
```bash
# Go (for Chromedp test)
go get github.com/chromedp/chromedp

# Python (for Selenium test)
pip3 install selenium

# Node.js (for Puppeteer test)
npm install puppeteer
```

### **Test File Locations:**
- **Go:** `/tmp/guest-test/chromedp_screenshot.go`
- **Python:** `/tmp/guest-test/selenium_test.py`
- **Node:** `/tmp/guest-test/puppeteer_test.js`
- **Output Files:** `/tmp/guest-test/*.png`, `/tmp/guest-test/*.pdf`

---

## 🧪 Test Variations for Advanced Testing

### **Test 1: Custom URL Testing**
**Prompt:** "Modify the chromedp test to navigate to https://yaver.io instead of example.com, extract the first heading text, and verify the page title contains 'Yaver'."

### **Test 2: Multiple Screenshots**
**Prompt:** "Create a loop in chromedp that takes screenshots of 3 different URLs (https://example.com, https://httpbin.org, https://example.org) and saves them as test1.png, test2.png, test3.png. Return the file sizes."

### **Test 3: PDF Generation**
**Prompt:** "In the puppeteer test, modify it to generate PDFs for all 3 URLs with proper filenames (page1.pdf, page2.pdf, page3.pdf) and return a summary with file sizes."

### **Test 4: Content Extraction**
**Prompt:** "Extract all links from the page and save them to a links.txt file, one per line. Count how many links were found."

### **Test 5: Mobile Viewport Testing**
**Prompt:** "Test mobile responsiveness by setting viewport size to 375x667 (iPhone SE) and taking screenshots at each size. Save them as mobile1.png, mobile2.png, mobile3.png."

### **Test 6: Form Interaction**
**Prompt:** "Create a test that fills out a search form on example.com, submits it, and captures the results page as screenshot.png."

---

## 🔧 Troubleshooting Guide

### **Issue: "Cannot connect to machine"**
**Solutions:**
1. Check if device is online in mobile app
2. Verify host granted you device access
3. Try a different available device
4. Check network connectivity

### **Issue: "Chromedriver not found"**
**Solutions:**
1. Verify chromedriver is installed: `which chromedriver`
2. Check if process is running: `ps aux | grep chromedriver`
3. Start chromedriver: `chromedriver --port=9515 &`

### **Issue: "Chrome connection timeout"**
**Solutions:**
1. Check Hetzner server is online: `ssh root@203.0.113.10 "yaver status"`
2. Verify chromedriver port is listening: `netstat -tlnp | grep 9515`
3. Check network firewall settings
4. Try different connection method

### **Issue: "Missing Go/Python/Node modules"**
**Solutions:**
```bash
# Go modules
go get github.com/chromedp/chromedp

# Python packages
pip3 install selenium

# Node packages
npm install puppeteer
```

### **Issue: "Task fails with '404 page not found'"**
**Solutions:**
1. Verify URL is accessible
2. Check internet connectivity on Hetzner
3. Try a different test URL (example.com is reliable)

### **Issue: "Screenshot is blank or corrupt"**
**Solutions:**
1. Ensure Chrome has enough memory
2. Add `--disable-dev-shm-usage` flag to Chrome
3. Check disk space on Hetzner
4. Try taking screenshot of different page

### **Issue: "PDF generation fails"**
**Solutions:**
1. Verify Chrome version supports PDF generation
2. Check font libraries are installed
3. Try with different page content
4. Add `--print-to-pdf-no-header` flag

---

## 📊 Performance Monitoring

### **For Host (You):**
```bash
# Monitor Hetzner CPU/Memory
ssh root@203.0.113.10 "top -b -n 1 | head -15"

# Check Chrome memory usage
ssh root@203.0.113.10 "ps aux | grep chrome"

# Check disk space
ssh root@203.0.113.10 "df -h /tmp"

# Check chromedriver connections
ssh root@203.0.113.10 "netstat -an | grep 9515"
```

### **For Guest:**
- Note task completion time
- Measure network latency
- Check file creation success
- Monitor mobile app response time

---

## 🎯 Success Indicators

### **Task Completion:**
- ✅ Task completes without errors
- ✅ Output files are created
- ✅ Files contain expected content
- ✅ Task can be repeated successfully
- ✅ Error messages are displayed properly

### **Browser Automation:**
- ✅ Chrome connects successfully
- ✅ Navigation works correctly
- ✅ Screenshot capture works
- ✅ PDF generation works
- ✅ Content extraction works
- ✅ Page interaction works

### **Mobile App Experience:**
- ✅ Can see available devices
- ✅ Can connect to machine
- ✅ Can create tasks with Codex
- ✅ Can view task results
- ✅ Can retrieve output files
- ✅ Error messages are clear

---

## 🚀 Ready to Test!

**Guest Instructions:**
1. **Download Yaver app** (App Store / Play Store)
2. **Sign in** with any method
3. **Enter invite code:** `EXAMPLE-CODE`
4. **Select a device** and connect
5. **Create task** with Codex runner
6. **Use the test prompt** from "Phase 3" above
7. **Review results** in mobile app

**Host (You) can:**
- Monitor Hetzner server: `ssh root@203.0.113.10 "yaver status"`
- Check Chrome status: `ssh root@203.0.113.10 "/snap/bin/chromium --version"`
- Check Chromedriver: `ssh root@203.0.113.10 "ps aux | grep chromedriver"`
- View logs: `ssh root@198.51.100.10 "journalctl -u yaver-agent -f"`
- Revoke guest: `yaver guests remove test-guest-hetzner@yaver.io`

**Test Commands (on Hetzner):**
```bash
# Check Chrome and Chromedriver versions
/snap/bin/chromium --version
chromedriver --version

# Check Chromedriver status
curl http://localhost:9515/status

# Check available tests
ls -la /tmp/guest-test/

# View test results
ls -la /tmp/guest-test/*.png /tmp/guest-test/*.pdf 2>/dev/null
```

---

## 💡 Pro Tips for Guest Testing

1. **Start Simple:** Begin with basic navigation, add complexity gradually
2. **Use Timeouts:** Set reasonable timeouts for page loads and operations
3. **Handle Errors:** Always include proper error handling in test scripts
4. **Cleanup:** Ensure browser is properly closed after tests
5. **Verify Results:** Always check output files exist and contain expected content
6. **Iterate Fast:** Use mobile app feedback to refine tests quickly
7. **Monitor Resources:** Watch for memory issues during testing
8. **Test Different Scenarios:** Try various URLs, viewports, and content types

---

## 📞 Support Resources

**If issues arise:**
1. Check Hetzner server is online and accessible
2. Verify Chrome/Chromedriver are installed and running
3. Confirm network connectivity between guest and Hetzner
4. Check invite code is still valid (2-day expiry)
5. Review test logs for specific error messages

**Host Commands (You can run):**
- `yaver status` - Check local agent status
- `yaver guests list` - View guest list and status
- `yaver guests remove <email>` - Revoke guest access
- `ssh root@203.0.113.10 "<command>"` - Run commands on Hetzner

**Guest Commands (You can run in mobile app):**
- View available devices
- Connect to machines
- Create tasks with various runners
- View task results
- Retrieve output files
- Monitor task progress

---

## 🎉 Testing Workflow Complete!

**System Status:**
- ✅ Hetzner server online and accessible
- ✅ Chrome and Chromedriver installed and working
- ✅ Chromedriver running on port 9515
- ✅ Guest account created with full permissions
- ✅ Test scripts prepared and ready
- ✅ Documentation complete

**Invite Code:** `EXAMPLE-CODE`
**Hetzner IP:** `203.0.113.10`
**Chromedriver Port:** 9515
**Test Directory:** `/tmp/guest-test/`
**Chrome Version:** 149.0.7827.114

**The guest testing workflow is now fully operational!** 🚀