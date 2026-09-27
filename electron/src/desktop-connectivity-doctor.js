"use strict";

// Native, local diagnostics for the desktop shell. This deliberately does not
// depend on the web dashboard or the Yaver agent being reachable: an agent
// outage is precisely when the desktop GUI must still explain what is wrong.

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { execFile } = require("node:child_process");

const FIREWALL_GROUP = "Yaver Private Access";

function runFile(file, args, { timeout = 5000, env = process.env } = {}) {
  return new Promise((resolve) => {
    execFile(file, args, { timeout, windowsHide: true, env }, (error, stdout, stderr) => {
      resolve({
        ok: !error,
        stdout: String(stdout || "").trim(),
        stderr: String(stderr || "").trim(),
        error: error ? String(error.message || error) : "",
      });
    });
  });
}

function tailscaleCandidates(platform, env = process.env) {
  if (platform === "darwin") {
    return [
      "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
      "/opt/homebrew/bin/tailscale",
      "/usr/local/bin/tailscale",
      "tailscale",
    ];
  }
  if (platform === "win32") {
    const roots = [env.ProgramFiles, env.ProgramW6432, env["ProgramFiles(x86)"]]
      .filter((value, index, all) => value && all.indexOf(value) === index);
    return [...roots.map((root) => path.win32.join(root, "Tailscale", "tailscale.exe")), "tailscale.exe"];
  }
  return ["/usr/bin/tailscale", "/usr/local/bin/tailscale", "tailscale"];
}

async function findTailscale(platform, env, run = runFile) {
  for (const candidate of tailscaleCandidates(platform, env)) {
    if (path.isAbsolute(candidate) && !fs.existsSync(candidate)) continue;
    const result = await run(candidate, ["status", "--json"], { timeout: 5000, env });
    if (result.ok) return { binary: candidate, result };
    if (path.isAbsolute(candidate)) return { binary: candidate, result };
  }
  return null;
}

function privateAddressSummary(interfaces = os.networkInterfaces()) {
  let lan = 0;
  let tailnet = 0;
  for (const addrs of Object.values(interfaces || {})) {
    for (const addr of addrs || []) {
      if (addr.internal || addr.family !== "IPv4") continue;
      const octets = String(addr.address || "").split(".").map(Number);
      if (octets.length !== 4 || octets.some((part) => !Number.isInteger(part))) continue;
      const [a, b] = octets;
      if (a === 100 && b >= 64 && b <= 127) tailnet += 1;
      else if (a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)) lan += 1;
    }
  }
  return { lan, tailnet };
}

function parseTailscaleStatus(raw) {
  try {
    const value = JSON.parse(raw);
    const state = String(value.BackendState || "Unknown");
    const ips = Array.isArray(value?.Self?.TailscaleIPs) ? value.Self.TailscaleIPs : [];
    return {
      state,
      online: state === "Running" && value?.Self?.Online !== false && ips.length > 0,
      addressCount: ips.length,
      health: Array.isArray(value.Health) ? value.Health.map(String).slice(0, 4) : [],
    };
  } catch {
    return { state: "InvalidStatus", online: false, addressCount: 0, health: [] };
  }
}

function windowsFirewallStatusScript(agentPath) {
  if (!agentPath) return "[pscustomobject]@{ok=$false;reason='agent path unresolved'}|ConvertTo-Json -Compress";
  return [
    "$ErrorActionPreference='Stop'",
    `$agent=${powershellLiteral(agentPath)}`,
    `$rules=@(Get-NetFirewallRule -Group '${FIREWALL_GROUP}' -ErrorAction SilentlyContinue)`,
    "$enabled=@($rules | Where-Object { $_.Enabled -eq 'True' -and $_.Direction -eq 'Inbound' -and $_.Action -eq 'Allow' })",
    "$public=@($enabled | Where-Object { ($_.Profile -band 4) -ne 0 })",
    "$tcp=@($enabled | Where-Object Name -eq 'Yaver-Agent-TCP')",
    "$udp=@($enabled | Where-Object Name -eq 'Yaver-Agent-UDP')",
    "$tcpPort=@($tcp | Get-NetFirewallPortFilter)",
    "$udpPort=@($udp | Get-NetFirewallPortFilter)",
    "$tcpApp=@($tcp | Get-NetFirewallApplicationFilter)",
    "$udpApp=@($udp | Get-NetFirewallApplicationFilter)",
    "$tcpAddr=@($tcp | Get-NetFirewallAddressFilter)",
    "$udpAddr=@($udp | Get-NetFirewallAddressFilter)",
    "$tcpPorts=($tcpPort.LocalPort -join ',')",
    "$udpPorts=($udpPort.LocalPort -join ',')",
    "$scopes=(($tcpAddr.RemoteAddress + $udpAddr.RemoteAddress) -join ',')",
    "$apps=(($tcpApp.Program + $udpApp.Program) | ForEach-Object { [string]$_ })",
    "$programOK=(@($apps | Where-Object { $_ -ieq $agent }).Count -eq 2)",
    "$portsOK=($tcpPort.Protocol -contains 'TCP' -and $udpPort.Protocol -contains 'UDP' -and $tcpPorts -match '18080' -and $tcpPorts -match '18443' -and $udpPorts -match '4433')",
    "$scopeOK=($scopes -match 'LocalSubnet' -and $scopes -match '100\\.64\\.0\\.0/10')",
    "$ok=($tcp.Count -eq 1 -and $udp.Count -eq 1 -and $public.Count -eq 0 -and $programOK -and $portsOK -and $scopeOK)",
    "[pscustomobject]@{ok=$ok;ruleCount=$rules.Count;enabledCount=$enabled.Count;publicCount=$public.Count;programOK=$programOK;portsOK=$portsOK;scopeOK=$scopeOK}|ConvertTo-Json -Compress",
  ].join(";");
}

const windowsRdpStatusScript = [
  "$deny=(Get-ItemProperty -Path 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Terminal Server' -Name fDenyTSConnections -ErrorAction Stop).fDenyTSConnections",
  "$listening=@(Get-NetTCPConnection -LocalPort 3389 -State Listen -ErrorAction SilentlyContinue).Count -gt 0",
  "[pscustomobject]@{enabled=($deny -eq 0);listening=$listening}|ConvertTo-Json -Compress",
].join(";");

function powershellLiteral(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function windowsFirewallRepairScript(agentPath) {
  const executable = powershellLiteral(agentPath);
  return [
    "$ErrorActionPreference='Stop'",
    `$agent=${executable}`,
    "if (-not (Test-Path -LiteralPath $agent -PathType Leaf)) { throw 'Yaver agent executable not found' }",
    `$old=@(Get-NetFirewallRule -Group '${FIREWALL_GROUP}' -ErrorAction SilentlyContinue)`,
    "$old | Remove-NetFirewallRule -ErrorAction Stop",
    `New-NetFirewallRule -Name 'Yaver-Agent-TCP' -DisplayName 'Yaver agent (private TCP)' -Group '${FIREWALL_GROUP}' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet,100.64.0.0/10 -Protocol TCP -LocalPort 18080,18443 -Program $agent | Out-Null`,
    `New-NetFirewallRule -Name 'Yaver-Agent-UDP' -DisplayName 'Yaver agent (private UDP)' -Group '${FIREWALL_GROUP}' -Direction Inbound -Action Allow -Enabled True -Profile Private,Domain -RemoteAddress LocalSubnet,100.64.0.0/10 -Protocol UDP -LocalPort 4433 -Program $agent | Out-Null`,
  ].join(";");
}

async function windowsPosture(agentPath, run = runFile) {
  const [firewall, rdp] = await Promise.all([
    run("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", windowsFirewallStatusScript(agentPath)], { timeout: 8000 }),
    run("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", windowsRdpStatusScript], { timeout: 8000 }),
  ]);
  const parse = (result) => {
    if (!result.ok) return null;
    try { return JSON.parse(result.stdout); } catch { return null; }
  };
  return { firewall: parse(firewall), rdp: parse(rdp) };
}

async function unixFirewallCheck(platform, run = runFile) {
  if (platform === "darwin") {
    const result = await run("/usr/libexec/ApplicationFirewall/socketfilterfw", ["--getglobalstate"], { timeout: 5000 });
    if (!result.ok) return { status: "info", detail: "macOS Application Firewall state could not be read. A probe from another device is still the decisive inbound test." };
    const enabled = /enabled|state\s*=\s*1/i.test(result.stdout);
    return enabled
      ? { status: "info", detail: "macOS Application Firewall is enabled. Signed-app policy is only inventory; another device must operation-probe the Yaver port.", fix: { id: "macos-firewall-settings", label: "Open firewall" } }
      : { status: "pass", detail: "macOS Application Firewall reports disabled. Agent authentication still applies; remote reachability remains operation-probed." };
  }
  if (platform !== "linux") return null;
  const ufw = await run("ufw", ["status"], { timeout: 5000 });
  if (ufw.ok && /status:\s*active/i.test(ufw.stdout)) {
    return { status: "warn", detail: "ufw is active. No broad rule is assumed safe; operation-probe from the intended LAN/Tailscale peer and use Fix with AI for a distro-specific, scoped rule." };
  }
  const firewalld = await run("firewall-cmd", ["--state"], { timeout: 5000 });
  if (firewalld.ok && /running/i.test(firewalld.stdout)) {
    return { status: "warn", detail: "firewalld is running. Zone assignment and a peer-side operation probe must confirm whether Yaver is reachable." };
  }
  return { status: "info", detail: "No active ufw/firewalld frontend was detected. nftables or provider policy may still filter inbound traffic, so a peer-side operation probe remains required." };
}

async function runDesktopConnectivityDiagnostics({
  platform = process.platform,
  env = process.env,
  agentStatus = "unknown",
  agentStatusDetail = "",
  localDeviceId = "",
  clientOnly = false,
  agentPath = "",
  probeAgent,
  interfaces,
  run = runFile,
} = {}) {
  const checks = [];
  const health = typeof probeAgent === "function" ? await probeAgent() : { ok: false };
  checks.push({
    id: "desktop-agent",
    name: "Local Yaver agent",
    status: clientOnly ? "info" : health?.ok ? "pass" : "fail",
    detail: clientOnly
      ? "This Store build is a client surface and cannot host the local agent. Install the signed direct desktop build to make this computer remotely discoverable."
      : health?.ok
      ? "The real /health operation answers on localhost:18080."
      : `The desktop agent is not answering on localhost:18080${agentStatusDetail ? `: ${agentStatusDetail}` : ` (${agentStatus})`}.`,
    fix: clientOnly ? { id: "open-download", label: "Get host build" } : health?.ok ? undefined : { id: "restart-agent", label: "Try restart" },
    aiEligible: !clientOnly && !health?.ok,
  });

  checks.push({
    id: "device-registration",
    name: "Signed-in device identity",
    status: clientOnly ? "info" : localDeviceId ? "pass" : "warn",
    detail: clientOnly
      ? "Client-only builds use the signed-in account's remote device rows and do not register this app as an agent host."
      : localDeviceId
      ? "A local device identity exists; the signed-in dashboard can match it to the owner-scoped Convex row."
      : "No local device ID is present yet, so this desktop cannot be matched to its Convex registration.",
    aiEligible: !clientOnly && !localDeviceId,
  });

  const addresses = privateAddressSummary(interfaces || os.networkInterfaces());
  checks.push({
    id: "private-addresses",
    name: "Private discovery addresses",
    status: addresses.lan + addresses.tailnet > 0 ? "pass" : "warn",
    detail: `${addresses.lan} LAN and ${addresses.tailnet} Tailscale/CGNAT IPv4 candidate${addresses.lan + addresses.tailnet === 1 ? "" : "s"} found. Convex registration is inventory; clients must still operation-probe a candidate before calling it usable.`,
    aiEligible: addresses.lan + addresses.tailnet === 0,
  });

  const tailscale = await findTailscale(platform, env, run);
  if (!tailscale) {
    checks.push({
      id: "tailscale",
      name: "Tailscale route",
      status: "warn",
      detail: "Tailscale is not installed or its CLI cannot be reached. Relay remains the remote fallback.",
      fix: { id: "open-tailscale", label: "Set up Tailscale" },
      aiEligible: true,
    });
  } else {
    const state = parseTailscaleStatus(tailscale.result.stdout);
    checks.push({
      id: "tailscale",
      name: "Tailscale route",
      status: state.online ? "pass" : "warn",
      detail: state.online
        ? `Tailscale daemon is Running with ${state.addressCount} address${state.addressCount === 1 ? "" : "es"}. A remote peer still has to answer the target operation.`
        : `Tailscale is installed but not usable (backend ${state.state}${state.health.length ? `; ${state.health.join("; ")}` : ""}).`,
      fix: state.online ? undefined : { id: "open-tailscale", label: "Open Tailscale" },
      aiEligible: !state.online,
    });
  }

  if (platform === "win32") {
    const posture = await windowsPosture(agentPath, run);
    checks.push({
      id: "windows-firewall",
      name: "Windows Firewall — Yaver private access",
      status: posture.firewall?.ok ? "pass" : "warn",
      detail: posture.firewall?.ok
        ? "Program-scoped inbound rules are enabled for Private/Domain networks only; Public profile is excluded."
        : "The exact Yaver private-access rules were not verified. The repair requests UAC and allows only the Yaver agent on Private/Domain networks from LocalSubnet and 100.64.0.0/10.",
      fix: posture.firewall?.ok ? undefined : { id: "windows-firewall", label: "Try firewall fix" },
      aiEligible: !posture.firewall?.ok,
    });
    checks.push({
      id: "windows-rdp",
      name: "Microsoft Remote Desktop host",
      status: posture.rdp?.enabled && posture.rdp?.listening ? "pass" : "warn",
      detail: posture.rdp?.enabled && posture.rdp?.listening
        ? "Windows Remote Desktop is enabled and TCP 3389 is listening. Reachability over Tailscale still must be probed from the controller."
        : "Windows Remote Desktop is not proven enabled and listening. Yaver will not enable RDP or create a broad 3389 rule without the local owner's explicit Windows consent.",
      fix: posture.rdp?.enabled && posture.rdp?.listening ? undefined : { id: "windows-rdp-settings", label: "Open RDP settings" },
      aiEligible: !(posture.rdp?.enabled && posture.rdp?.listening),
    });
  } else {
    const firewall = await unixFirewallCheck(platform, run);
    if (firewall) {
      checks.push({
        id: `${platform}-firewall`,
        name: platform === "darwin" ? "macOS Application Firewall" : "Linux firewall",
        status: firewall.status,
        detail: firewall.detail,
        fix: firewall.fix,
        aiEligible: firewall.status !== "pass",
      });
    }
  }

  return { ok: checks.every((check) => check.status !== "fail"), platform, checks };
}

async function repairWindowsFirewall(agentPath, run = runFile) {
  if (!agentPath || !path.win32.isAbsolute(agentPath)) {
    return { ok: false, error: "The exact Yaver agent executable could not be resolved; no firewall rule was changed." };
  }
  const encoded = Buffer.from(windowsFirewallRepairScript(agentPath), "utf16le").toString("base64");
  const launcher = [
    "$p=Start-Process -FilePath 'powershell.exe' -Verb RunAs -Wait -PassThru -ArgumentList @('-NoProfile','-NonInteractive','-EncodedCommand',",
    powershellLiteral(encoded),
    ")",
    "exit $p.ExitCode",
  ].join("");
  const result = await run("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", launcher], { timeout: 120000 });
  return result.ok ? { ok: true } : { ok: false, error: result.stderr || result.error || "Firewall repair was cancelled or failed." };
}

module.exports = {
  FIREWALL_GROUP,
  parseTailscaleStatus,
  privateAddressSummary,
  repairWindowsFirewall,
  runDesktopConnectivityDiagnostics,
  tailscaleCandidates,
  unixFirewallCheck,
  windowsFirewallRepairScript,
  windowsFirewallStatusScript,
};
