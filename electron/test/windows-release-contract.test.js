"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const root = path.join(__dirname, "..", "..");
const read = (relative) => fs.readFileSync(path.join(root, relative), "utf8");

test("Windows release is local-SimplySign owned and CI cannot publish unsigned bytes", () => {
  const cli = read(".github/workflows/release-cli.yml");
  const gui = read(".github/workflows/release-gui.yml");
  const deploy = read("deploy/deploy.sh");

  assert.doesNotMatch(cli, /- goos:\s*windows/);
  assert.doesNotMatch(gui, /- os:[^\n]*windows-latest/);
  assert.match(cli, /scripts\/windows-signing\.ps1/);
  assert.match(gui, /scripts\/windows-signing\.ps1/);
  assert.match(deploy, /desktop-windows\)/);
});

test("direct Windows EXE build signs all PE extensions and proves silent install", () => {
  const config = read("electron/electron-builder.windows.cjs");
  const pkg = JSON.parse(read("electron/package.json"));
  const signing = read("scripts/windows-signing.ps1");

  assert.equal(pkg.author.name, "SIMKAB");
  assert.equal(pkg.build.nsis.uninstallDisplayName, "Yaver");
  assert.equal(pkg.build.nsis.shortcutName, "Yaver");
  assert.match(config, /signExts:\s*\["\.exe",\s*"\.dll",\s*"\.node"\]/);
  assert.match(config, /sign:\s*"\.\/sign-windows-hook\.js"/);
  assert.match(config, /from:\s*"resources\/bin\/yaver\.exe"/);
  assert.doesNotMatch(config, /from:\s*"resources\/bin"/);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /Expected x64 PE machine 0x8664/);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /non-Windows host agent/);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /No bundled WSL executable/);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /Native x64 agent startup passed without WSL/);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /Redistributed FSL license is missing/);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /Installed apps Publisher does not identify SIMKAB/);
  assert.match(signing, /TimeStamperCertificate/);
  assert.match(signing, /WIN_CERTIFICATE_SHA1/);
  assert.match(signing, /WIN_CSC_LINK/);
});

test("active SimplySign session can cross-sign a pinned Windows release from macOS", () => {
  const hook = read("electron/sign-windows-hook.js");
  const prepare = read("electron/scripts/prepare-jsign.sh");
  assert.match(hook, /YAVER_WINDOWS_CERT_ALIAS/);
  assert.match(hook, /YAVER_WINDOWS_CERT_SHA1/);
  assert.match(hook, /libSimplySignPKCS\.dylib/);
  assert.match(hook, /require-leaf-hash/);
  assert.match(hook, /CKR_FUNCTION_FAILED/);
  assert.match(hook, /CERTUM_PLUGIN_DIR/);
  assert.match(hook, /readdirSync\(CERTUM_PLUGIN_DIR/);
  assert.doesNotMatch(hook, /jdk-25\.0\.1/);
  assert.match(prepare, /602a51c3545a6dc4fb99bd2ea7152b26d1345916d0c93ddfbd5936cb735af91c/);
});

test("macOS cross-packaging validates the Windows agent rather than the host binary", () => {
  const pkg = JSON.parse(read("electron/package.json"));
  const preflight = read("electron/scripts/preflight-pack.mjs");
  assert.match(pkg.scripts["dist:win"], /--platform=win32 --arch=x64/);
  assert.match(preflight, /targetPlatform === "win32" \? "yaver\.exe" : "yaver"/);
});

test("canonical deploy exposes a non-publishing Windows Store build lane", () => {
  const deploy = read("deploy/deploy.sh");
  const build = read("scripts/build-windows-store.sh");
  const publish = read("scripts/publish-windows-store-package.sh");
  assert.match(deploy, /desktop-windows\)/);
  assert.match(deploy, /desktop-windows-store-package\)/);
  assert.match(deploy, /gh workflow run microsoft-store-release\.yml/);
  assert.match(build, /npm run dist:win/);
  assert.match(build, /require-leaf-hash/);
  assert.match(build, /pkcs11-tool[\s\S]*?No slots\\\.[\s\S]*?activate the virtual card/);
  assert.doesNotMatch(build, /git push|wrangler r2 object put/);
  assert.match(publish, /refusing to overwrite immutable Store object/);
  assert.match(publish, /max-redirs 0/);
  assert.match(deploy, /git remote get-url origin[\s\S]*?git remote get-url github/);
  assert.match(deploy, /git push "\$release_remote"/);
});

test("fresh standalone Windows desktop can provision managed Node for runners", () => {
  const install = read("desktop/agent/install_cmd.go");
  const start = install.indexOf("func installNodeGlobalPackageStream");
  const end = install.indexOf("func ensureRunnerInstalledStream", start);
  const body = install.slice(start, end);

  assert.match(body, /installNodeRuntime\(ctx, progress\)/);
  assert.doesNotMatch(body, /exec\.LookPath\("npm\.cmd"\)/);
  assert.match(body, /runtime\.GOOS == "windows"[\s\S]*?npmPath \+= "\.cmd"/);
});

test("Store build does not change Windows startup or power behavior before consent", () => {
  const policy = require("../src/desktop-runtime-policy");
  const manager = read("electron/src/agent-manager.js");
  assert.equal(policy.DEFAULT_SETTINGS.launchAtLogin, false);
  assert.equal(policy.DEFAULT_SETTINGS.keepAwake, false);
  assert.match(manager, /YAVER_SKIP_AUTO_START:\s*"1"/);
});

test("direct Windows desktop stays native while Store listing discloses no bundled WSL", () => {
  const config = read("electron/electron-builder.windows.cjs");
  const listing = read("electron/store/listing-en.txt");
  assert.doesNotMatch(config, /wsl|vhdx|rootfs/i);
  assert.match(listing, /does not bundle WSL/i);
});

test("legacy EXE metadata is retained while certification notes describe the Store AppX", () => {
  const script = read("electron/scripts/microsoft-store-package-metadata.mjs");
  const notes = read("electron/store/microsoft-store-certification-notes.md");
  assert.match(script, /versions\.gui/);
  assert.match(script, /download\.yaver\.io\/windows\/\$\{version\}/);
  assert.match(script, /installerParameters:\s*"\/S"/);
  assert.match(script, /support\/windows-installer-exit-codes/);
  assert.match(read("web/app/support/windows-installer-exit-codes/page.tsx"), /code:\s*"2"/);
  assert.match(notes, /Store build does not expose Start-at-login/);
  assert.match(notes, /Partner Center signs, hosts, installs, updates, and removes/);
  assert.match(notes, /working test account in Partner Center/);
  const listing = read("electron/store/listing-en.txt");
  assert.match(listing, /publicly available source code/i);
  assert.match(listing, /live generative AI content/i);
  assert.match(listing, /report inappropriate output/i);
  const settings = read("web/components/dashboard/SettingsView.tsx");
  assert.match(settings, /Report inappropriate AI output/);
  assert.match(settings, /mailto:kivanc\.cakmak@simkab\.com\?subject=/);
  assert.doesNotMatch(listing, /encrypted relay/i);
  const searchTerms = listing.slice(listing.indexOf("Search terms"), listing.indexOf("What's new"));
  assert.doesNotMatch(searchTerms, /Codex|Claude Code|OpenCode/);
  assert.match(read("electron/store/privacy-and-declarations.txt"), /HTTP browser\/proxy lane terminates relay TLS/);
  assert.match(read("electron/store/screenshots/README.md"), /currently zero approved Windows Store screenshots/i);
  assert.match(read("electron/store/windows-store-preflight.ps1"), /Windows App Certification Kit/);
});

test("superseded EXE submission remains fail-closed", () => {
  const submit = read("scripts/microsoft-store-submission.mjs");
  const manifest = JSON.parse(read("electron/store/submission-manifest.json"));

  assert.match(submit, /process\.argv\[2\] \|\| "status"/);
  assert.match(submit, /YAVER_MS_STORE_ALLOW_SUBMIT/);
  assert.match(submit, /--confirm-product=/);
  assert.match(submit, /redirect: "error"/);
  assert.match(submit, /ongoingSubmissionId/);
  assert.equal(manifest.version, "0.1.11");
  assert.equal(manifest.releaseStatus, "superseded");
  assert.match(manifest.installerSha256, /^[a-f0-9]{64}$/);
  assert.doesNotMatch(read(".github/workflows/microsoft-store-release.yml"), /submission-manifest\.json/);
});

test("desktop updater and release metadata use the canonical GitHub organization", () => {
  const pkg = JSON.parse(read("electron/package.json"));
  assert.equal(pkg.build.publish[0].owner, "yaver-io");
  assert.equal(pkg.build.publish[0].repo, "yaver.io");
  assert.doesNotMatch(JSON.stringify(pkg.build.publish), /kivanccakmak/);
});
