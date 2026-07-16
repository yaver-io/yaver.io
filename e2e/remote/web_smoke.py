"""Closed-loop check of the yaver.io dashboard, run from a remote box.

Unauthenticated by design: CLAUDE.md forbids secrets on the disposable Hetzner
box, so this must never carry a real session token.

That bound shapes HOW it verifies the build. The version chip only renders
inside the authed dashboard, so asserting it against the DOM fails for the wrong
reason. But navigating to /dashboard still makes the browser load that route's
JS chunk — which is where WEB_VERSION (from web/package.json, the file that was
stranded at 1.1.154) is baked at build time. So: enumerate every script the page
actually loaded and grep those. curl cannot do this — the chunk is lazy and only
a JS-executing browser resolves which one to fetch.
"""
import sys, time, json
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.chrome.service import Service

EXPECT = sys.argv[1] if len(sys.argv) > 1 else "1.1.159"
STALE = ["1.1.154", "1.1.155", "1.1.156", "1.1.157", "1.1.158"]

opts = Options()
opts.binary_location = "/usr/bin/chromium-browser"
for f in ("--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--window-size=1400,1000"):
    opts.add_argument(f)

d = webdriver.Chrome(service=Service("/usr/bin/chromedriver"), options=opts)
checks, failures = [], []
try:
    d.set_page_load_timeout(60)
    d.get("https://yaver.io/dashboard")
    time.sleep(6)

    checks.append(("page loads", bool(d.title), d.title))

    body = d.find_element("tag name", "body").text
    gated = any(w in body.lower() for w in ("sign in", "log in", "continue with"))
    checks.append(("reaches auth gate (no token)", gated, body.strip()[:60].replace("\n", " ")))
    if not body.strip():
        failures.append("empty body — the client bundle failed to boot")

    # Fetch every script the browser actually loaded, in-page, and look for the
    # build's version string.
    found = d.execute_async_script("""
      const done = arguments[arguments.length - 1];
      const want = arguments[0], stale = arguments[1];
      const urls = performance.getEntriesByType('resource')
        .map(e => e.name).filter(n => n.endsWith('.js'));
      (async () => {
        const hits = {want: [], stale: []};
        for (const u of urls) {
          try {
            const t = await (await fetch(u)).text();
            if (t.includes(want)) hits.want.push(u.split('/').pop());
            for (const s of stale) if (t.includes(s)) hits.stale.push(s + ' @ ' + u.split('/').pop());
          } catch (e) {}
        }
        done({scanned: urls.length, ...hits});
      })();
    """, EXPECT, STALE)

    ok = len(found["want"]) > 0
    checks.append((f"build serves v{EXPECT}", ok,
                   f"{found['scanned']} chunks scanned; " + (", ".join(found["want"][:2]) if ok else "NOT in any chunk")))
    if not ok:
        failures.append(f"v{EXPECT} not found in {found['scanned']} loaded chunks; stale hits: {found['stale'][:3] or 'none'}")

    # The drift bug itself: versions.json and web/package.json disagreeing.
    no_stale = len(found["stale"]) == 0
    checks.append(("no stale version baked in", no_stale, found["stale"][:2] or "clean"))
    if not no_stale:
        failures.append(f"stale version still in the build: {found['stale'][:3]}")

    d.save_screenshot("/tmp/yaver-dashboard.png")
finally:
    d.quit()

for name, ok, detail in checks:
    print(f"{'PASS' if ok else 'FAIL'}  {name:<30} {detail}")
if failures:
    print("\nFAILURES:")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("\nclosed-loop: OK")
