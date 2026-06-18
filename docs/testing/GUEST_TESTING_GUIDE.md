# Yaver Guest Testing Guide: Mobile App → Hetzner Server with Codex & Chrome

**🎯 Goal:** Test mobile app workflow where a guest uses Codex to run tasks on a remote Hetzner server with Chrome/chromedp browser automation.

---

## 📋 Setup Summary

**Host Machine (You):** Local Mac
- **Hetzner Server:** `example-remote-box` (203.0.113.10)
- **Guest Account:** `test-guest-hetzner@yaver.io`
- **Invite Code:** `EXAMPLE-CODE`
- **Chrome:** Chromium 149.0.7827.114 installed on Hetzner
- **Chromedriver:** Version 149.0.7827.114 installed on Hetzner

---

## 🔧 Guest Setup Instructions

### Step 1: Accept Invitation
1. Download Yaver app (iOS/Android)
2. Sign in with any method (Apple, Google, Microsoft, or email)
3. Enter invite code: **`EXAMPLE-CODE`**
4. You now have full teammate access (tasks, builds, dev server, browser control)

---

## 📱 Mobile App Usage (for Guest)

### Step 1: Open Yaver Mobile App
- Launch the Yaver app on your phone
- You should see devices available to you

### Step 2: Select Hetzner Machine
- Look for: `selected-machine` (ID: `example-device-id`)
- **Status:** Should show "online"
- Tap to connect to this machine

### Step 3: Start a Task with Codex
- **"Create Task"** → Select "Codex" as runner
- **Example task prompts:**
  - "Create a simple Go program that takes a screenshot of a webpage using chromedp"
  - "Write a Python script that opens Chrome and visits a URL"
  - "Test Chrome browser automation by opening example.com"
  - "Create a test that captures a screenshot using Chromium on Hetzner"

---

## 🌐 Chrome Automation Examples (for Guest to Try)

### Example 1: Simple Screenshot with Chromedp (Go)
**Prompt:** "Create a Go program that uses chromedp to take a screenshot of example.com and save it as screenshot.png"

**Expected Result:** The guest will write a Go program that:
1. Uses `github.com/chromedp/chromedp` package
2. Connects to Chrome on Hetzner (via remote chromedriver)
3. Navigates to `example.com`
4. Takes a screenshot and saves it

### Example 2: Python Selenium Test
**Prompt:** "Write a Python script using Selenium that opens Chrome on this machine and tests the Google homepage"

**Expected Result:** Python code using selenium that:
1. Connects to chromedriver on Hetzner
2. Opens Google homepage
3. Verifies the page title
4. Takes a screenshot

### Example 3: JavaScript Headless Chrome
**Prompt:** "Create a Node.js script that uses Puppeteer to launch Chrome headlessly and capture a PDF of a website"

**Expected Result:** Node.js code using puppeteer that:
1. Launches headless Chrome on Hetzner
2. Navigates to a URL
3. Generates a PDF of the page

---

## 🔍 Technical Details for Guest

### Hetzner Server Capabilities
- **OS:** Ubuntu 24.04 (arm64)
- **Chrome:** Chromium 149.0.7827.114 via Snap
- **Chromedriver:** Installed at `/usr/local/bin/chromedriver`
- **Headless Chrome:** Works with `--headless --no-sandbox --disable-dev-shm-usage`
- **Location:** `/snap/bin/chromium`

### Chrome Command Examples (for reference)
```bash
# Headless Chrome for automation
/snap/bin/chromium --headless --no-sandbox --disable-dev-shm-usage \
  --virtual-time-budget=5000 \
  'data:text/html,<html><body>Test</body></html>'

# Start Chromedriver
chromedriver --port=9515

# Screenshot to PDF
/snap/bin/chromium --headless --no-sandbox --disable-dev-shm-usage \
  --print-to-pdf=/tmp/test.pdf \
  'data:text/html,<html><body>Test</body></html>'
```

---

## 📊 Testing Checklist

### Basic Connectivity
- [ ] Can see Hetzner machine in device list
- [ ] Machine shows "online" status
- [ ] Can connect to machine
- [ ] Task creation works with Codex

### Chrome Testing
- [ ] Can create Chromedp-based task
- [ ] Chromedriver can connect on Hetzner
- [ ] Screenshot capture works
- [ ] PDF generation works
- [ ] Browser automation functions properly

### Integration Testing
- [ ] Mobile app → Hetzner connection works
- [ ] Codex can access Chrome on Hetzner
- [ ] Results/files can be retrieved from Hetzner
- [ ] Error handling works correctly

---

## 🚨 Troubleshooting

### Can't see Hetzner machine
- Check if host has granted access to device
- Verify machine is online
- Refresh device list in mobile app

### Chrome connection fails
- Ensure Chromedriver is running on Hetzner
- Check firewall allows chromedriver port (9515)
- Verify Snap Chrome is properly installed

### Task creation fails
- Confirm Codex is authorized on this machine
- Check that "codex" is in allowed runners list
- Verify network connectivity to Hetzner

---

## 📞 Support

If you encounter issues:
1. Check the host for device access permissions
2. Verify Hetzner server is accessible
3. Confirm Chrome/Chromedriver are working on Hetzner
4. Check invite code is still valid (2-day expiry)

---

## 🎯 Example Test Scenarios

### Scenario 1: Web Scraper Test
**Prompt:** "Create a web scraper that extracts the title and main heading from example.com using chromedp"

### Scenario 2: PDF Generator
**Prompt:** "Write a script that converts a webpage to PDF using Chrome headless mode"

### Scenario 3: Screenshot Service
**Prompt:** "Create a service that takes screenshots of any URL and saves them as images"

### Scenario 4: Browser Testing
**Prompt:** "Test mobile responsiveness of a website by taking screenshots at different viewport sizes"

---

## 💡 Tips for Guest Developers

- Use `--headless` flag for Chrome automation
- Add `--no-sandbox` for headless Chrome
- Include `--disable-dev-shm-usage` to avoid memory issues
- Chromedriver runs on port 9515 by default
- All files created on Hetzner are stored in `/tmp/` by default
- Results can be retrieved via the mobile app

---

**Ready to test! 🚀**

Invite code: **EXAMPLE-CODE**
Hetzner Machine: **selected-machine** (example-device-id)
Chrome Version: **149.0.7827.114**