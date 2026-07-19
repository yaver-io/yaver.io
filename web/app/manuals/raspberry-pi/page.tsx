import Link from "next/link";

export default function RaspberryPiManual() {
  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-3xl">
        <Link
          href="/manuals"
          className="mb-8 inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50"
        >
          &larr; Back to Manuals
        </Link>

        <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
          Raspberry Pi — Yaver Dev Node or IoT Edge
        </h1>
        <p className="mb-4 text-sm leading-relaxed text-surface-400">
          Use a Raspberry Pi 4 or 5 as an always-on Yaver dev node, or use a
          Raspberry Pi 3B/3B+ as a lightweight IoT edge node for machine IO.
          The Pi 3B path runs the Go agent, mesh, serial/RS485, and control
          surfaces; AI models, GPUs, local coding agents, Playwright, and mobile
          build stacks should run on remote machines.
        </p>
        <p className="mb-12 text-sm leading-relaxed text-surface-400">
          This guide covers hardware, OS install, Yaver install, headless OAuth,
          auto-start on reboot, auto-recovery after a power cut, and the
          difference between the full dev-node profile and the lightweight IoT
          edge profile.
        </p>

        <div className="mb-12 rounded-lg border border-emerald-500/20 bg-emerald-500/10 p-4 text-sm text-emerald-800 dark:text-emerald-100">
          <p className="font-semibold">What you get at the end</p>
          <ul className="mt-2 space-y-1 text-[13px] leading-relaxed text-emerald-800 dark:text-emerald-100/90">
            <li>&#8226; Headless Pi visible to the phone over LAN beacon + relay</li>
            <li>&#8226; React Native / Expo projects hot-reload onto the phone in under 2s</li>
            <li>&#8226; First Metro bundle on a Pi 4: ~30&ndash;60s; reloads after that are instant</li>
            <li>&#8226; Survives power outages: Pi powers back on, Yaver starts, phone reconnects</li>
            <li>&#8226; WiFi never sleeps, HDMI off, no screen blanking, no Bluetooth drain</li>
          </ul>
        </div>

        {/* ── Hardware ─────────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Hardware checklist</h2>
          <ul className="space-y-2 text-sm leading-relaxed text-surface-400">
            <li>
              <span className="font-semibold text-surface-200">Dev node: Raspberry Pi 4 (4 GB+) or Pi 5 (4 GB+).</span>
              {" "}
              The Pi 3 works for the agent alone but chokes on Metro bundling for any
              non-trivial RN app. Pi Zero 2 W is not recommended.
            </li>
            <li>
              <span className="font-semibold text-surface-200">IoT edge node: Raspberry Pi 3B/3B+ with Raspberry Pi OS Lite 64-bit.</span>
              {" "}
              This is the cheap path for RS485, GPIO, mesh, and Yaver control
              when heavy AI/coding/runtime work is remote. The npm agent
              bootstrap supports <code>linux-arm64</code>; do not use 32-bit
              Pi OS for this lane.
            </li>
            <li>
              <span className="font-semibold text-surface-200">32 GB+ microSD card</span> (A2
              rating). Better: plug an SSD over USB 3.0 &mdash; Node&apos;s <code>npm install</code> is
              2&ndash;3&times; faster on SSD and you stop burning through SD cards.
            </li>
            <li>
              <span className="font-semibold text-surface-200">Official 27 W USB-C power supply</span>{" "}
              (Pi 5) or <span className="font-semibold text-surface-200">5 V / 3 A USB-C PSU</span> (Pi
              4). Under-voltage kills WiFi radio first &mdash; <code>vcgencmd get_throttled</code>{" "}
              reports it but you&apos;ll just see random disconnects.
            </li>
            <li>
              <span className="font-semibold text-surface-200">Ethernet cable</span> (strongly
              recommended). WiFi works but wired is more stable for a headless box that the phone
              depends on.
            </li>
            <li>
              <span className="font-semibold text-surface-200">Case with a fan</span> (Pi 4) or the
              official active cooler (Pi 5). Metro bundles pin the CPU; thermal throttling will
              double bundle time.
            </li>
          </ul>
        </section>

        {/* ── OS install ───────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Flash the OS</h2>
          <p className="mb-3 text-sm leading-relaxed text-surface-400">
            Install <Link href="https://www.raspberrypi.com/software/" className="underline hover:text-surface-300">Raspberry Pi Imager</Link>,
            pick <span className="font-semibold text-surface-200">Raspberry Pi OS Lite (64-bit)</span> (no desktop), and open
            the Imager&apos;s advanced options (the gear icon) before writing:
          </p>
          <ul className="space-y-1 text-sm text-surface-400">
            <li>&#8226; Set hostname (e.g. <code>yaver-pi</code>)</li>
            <li>&#8226; Enable SSH, set a username + a strong password</li>
            <li>&#8226; Configure WiFi (SSID + password + country) if not using Ethernet</li>
            <li>&#8226; Set locale / timezone</li>
          </ul>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            Write the image, boot the Pi, and SSH in. Update and reboot once so the
            kernel / firmware are current:
          </p>
          <div className="mt-3 overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div><span className="text-surface-500">$</span> ssh yaver@yaver-pi.local</div>
            <div className="text-surface-500"># update Raspberry Pi OS with your normal system update flow</div>
            <div><span className="text-surface-500">$</span> sudo reboot</div>
          </div>
        </section>

        {/* ── Install Yaver ────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Install Yaver</h2>
          <p className="mb-3 text-sm leading-relaxed text-surface-400">
            Install Yaver with npm, then choose exactly one profile. The full
            dev-node profile is for Pi 4/5. The IoT edge profile is for Pi
            3B/3B+ machine IO and deliberately skips mobile build tools,
            Playwright, local voice models, sandbox packages, and local coding
            agents.
          </p>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1 text-surface-500"># Pi 3B/3B+ IoT edge: Go agent only, heavy work remote</div>
            <div className="mb-3"><span className="text-surface-500">$</span> YAVER_IOT_EDGE=1 npm install -g yaver-cli</div>
            <div className="mb-1 text-surface-500"># Pi 4/5 dev node: full mobile/coding toolchain</div>
            <div className="mb-1"><span className="text-surface-500">$</span> npm install -g yaver-cli</div>
            <div className="mb-3"><span className="text-surface-500">$</span> yaver install mobile   # pulls Node + hermesc, idempotent</div>
            <div className="mb-3"><span className="text-surface-500">$</span> yaver install pi-dev-node   # AI/dev stack for Pi 4/5, not Pi 3B edge nodes</div>
          </div>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            The <code>pi-dev-node</code> profile is the best starting point for a headless
            Raspberry Pi 4/5 Yaver dev box. It installs the economic AI stack plus
            Python/TypeScript TDD helpers and the promoted-backend toolchain:
            <code> sqlite3</code>, <code>vercel</code>, <code>convex</code>,{" "}
            <code>postgresql</code>, <code>redis</code>, <code>supabase</code>, and{" "}
            <code>mosquitto</code>. Premium runner sign-in such as Codex or Claude Code is
            still left to you. Use Yaver Relay for remote access after the base
            node comes up; upgrade to Relay Pro when that Pi becomes part of
            daily work.
          </p>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            For Pi 3B/3B+ IoT edge nodes, keep the install smaller: use
            <code> YAVER_IOT_EDGE=1</code>, then run <code>yaver auth --headless</code>
            and <code>yaver serve</code>. Treat the Pi as a gateway and control
            plane for RS485/GPIO/USB devices, while Yaver routes coding agents
            and GPU/model workloads to stronger remote machines.
          </p>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            If you use the flashable Pi image instead of a manual install, the boot partition
            includes <code>yaver-firstboot.env</code> with an <code>YAVER_AUTO_UPDATE</code>
            parameter. Supported values are <code>daily</code>, <code>weekly</code>, and{" "}
            <code>off</code>; first boot converts that into a systemd timer for Yaver, apt
            packages, and the npx-backed cloud/backend CLIs shipped with the dev stack.
          </p>
        </section>

        {/* ── Headless auth ────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Headless sign-in (no browser on the Pi)
          </h2>
          <p className="mb-3 text-sm leading-relaxed text-surface-400">
            <code>yaver auth --headless</code> prints a URL with a short code. Open that URL on your
            phone or laptop, sign in with Google / Apple / Microsoft / GitHub / GitLab /
            Discord / Slack / email, and the Pi picks up the token automatically.
            The flow is resumable &mdash; if the Pi reboots mid-approval, re-running the
            command continues the same session.
          </p>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1"><span className="text-surface-500">$</span> yaver auth --headless</div>
            <div className="mb-1 text-surface-500">Go to https://yaver.io/auth/device?code=XXXX-YYYY</div>
            <div className="mb-1 text-surface-500">Approve there; this machine will pick up the token.</div>
            <div><span className="text-surface-500">$</span> cat ~/.yaver/config.json | head -1   # auth_token persisted</div>
          </div>
        </section>

        {/* ── Systemd service ─────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Run on boot (systemd user service)
          </h2>
          <p className="mb-3 text-sm leading-relaxed text-surface-400">
            One command installs a systemd user unit, enables lingering (so the
            service keeps running after you log out), and starts the agent. Yaver
            auto-updates every 6 hours from GitHub releases and restarts itself
            with the new binary.
          </p>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1"><span className="text-surface-500">$</span> yaver serve --install-systemd</div>
            <div className="mb-2 text-surface-500"># verify:</div>
            <div className="mb-1"><span className="text-surface-500">$</span> systemctl --user status yaver</div>
            <div><span className="text-surface-500">$</span> journalctl --user -u yaver -f   # live log</div>
          </div>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            If the phone can&apos;t see the Pi yet, check the relay tunnel log. On WiFi-only Pis
            the LAN beacon may need a second to settle on the new IP after the DHCP lease.
          </p>
        </section>

        {/* ── Power on after outage ────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Auto power-on after a power cut
          </h2>
          <p className="mb-3 text-sm leading-relaxed text-surface-400">
            By default, a Pi 4 powers on when the PSU delivers voltage &mdash; but the
            Pi 5&apos;s power button behavior can leave it off after a power cycle.
            Make both models boot unconditionally:
          </p>
          <p className="mt-3 mb-1 text-[13px] font-semibold text-surface-200">Pi 5 (bootloader EEPROM):</p>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1"><span className="text-surface-500">$</span> sudo -E rpi-eeprom-config --edit</div>
            <div className="mb-1 text-surface-500"># add (or change) these lines:</div>
            <div className="mb-1"><span className="select-all">POWER_OFF_ON_HALT=0</span></div>
            <div className="mb-1"><span className="select-all">BOOT_ORDER=0xf416</span>{" "}
              <span className="text-surface-500"># try SD, then USB, then network, repeat</span>
            </div>
            <div className="mb-1"><span className="select-all">PSU_MAX_CURRENT=5000</span>{" "}
              <span className="text-surface-500"># only set on Pi 5 + 27W PSU</span>
            </div>
            <div className="mb-2 text-surface-500"># save, then:</div>
            <div><span className="text-surface-500">$</span> sudo reboot</div>
          </div>
          <p className="mt-4 mb-1 text-[13px] font-semibold text-surface-200">Pi 4:</p>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1 text-surface-500"># Pi 4 boots automatically when power is restored.</div>
            <div className="mb-1 text-surface-500"># If yours doesn&apos;t, update the bootloader:</div>
            <div><span className="text-surface-500">$</span> sudo rpi-eeprom-update -a &amp;&amp; sudo reboot</div>
          </div>
          <p className="mt-4 text-sm leading-relaxed text-surface-400">
            Test it: pull the plug, wait 30 seconds, plug it back in. The Pi should
            boot, the systemd user service should start Yaver, and the phone
            should reconnect inside ~90 seconds.
          </p>
        </section>

        {/* ── Disable power save ───────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">
            Disable power save &mdash; WiFi, Bluetooth, HDMI, screen blanking
          </h2>
          <p className="mb-3 text-sm leading-relaxed text-surface-400">
            The default Raspberry Pi OS settings aggressively power down idle
            subsystems. On a headless always-on box that means the phone
            sometimes can&apos;t reach the Pi for 5&ndash;30 seconds after a quiet
            period. Turn these off:
          </p>

          <h3 className="mt-5 mb-2 text-[13px] font-semibold text-surface-200">1. WiFi power save (biggest win)</h3>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1 text-surface-500"># check current state:</div>
            <div className="mb-2"><span className="text-surface-500">$</span> iw dev wlan0 get power_save</div>
            <div className="mb-1 text-surface-500"># disable now:</div>
            <div className="mb-2"><span className="text-surface-500">$</span> sudo iw dev wlan0 set power_save off</div>
            <div className="mb-1 text-surface-500"># persist across reboots via systemd:</div>
            <div className="mb-1"><span className="text-surface-500">$</span> sudo tee /etc/systemd/system/wifi-powersave-off.service &gt;/dev/null &lt;&lt;&apos;EOF&apos;</div>
            <div className="mb-1 whitespace-pre">{`[Unit]
Description=Disable WiFi power save
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/sbin/iw dev wlan0 set power_save off
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF`}</div>
            <div><span className="text-surface-500">$</span> sudo systemctl enable --now wifi-powersave-off.service</div>
          </div>

          <h3 className="mt-5 mb-2 text-[13px] font-semibold text-surface-200">2. Screen blanking (if an HDMI display is attached)</h3>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1"><span className="text-surface-500">$</span> sudo raspi-config nonint do_blanking 1   # disable blanking</div>
            <div className="mb-1 text-surface-500"># or manually — append to /boot/firmware/cmdline.txt (one line!):</div>
            <div><span className="select-all">consoleblank=0</span></div>
          </div>

          <h3 className="mt-5 mb-2 text-[13px] font-semibold text-surface-200">3. Turn HDMI off on a truly headless box (saves ~25 mA and heat)</h3>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1 text-surface-500"># edit /boot/firmware/config.txt:</div>
            <div className="mb-1"><span className="select-all">hdmi_blanking=2</span></div>
            <div><span className="select-all">dtoverlay=vc4-kms-v3d,noaudio,cma-256</span></div>
          </div>

          <h3 className="mt-5 mb-2 text-[13px] font-semibold text-surface-200">4. Disable Bluetooth if you&apos;re not using it</h3>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1"><span className="text-surface-500">$</span> sudo systemctl disable --now hciuart.service bluetooth.service</div>
            <div className="mb-1 text-surface-500"># append to /boot/firmware/config.txt to hard-disable:</div>
            <div><span className="select-all">dtoverlay=disable-bt</span></div>
          </div>

          <h3 className="mt-5 mb-2 text-[13px] font-semibold text-surface-200">5. USB auto-suspend (only if you notice SSD hiccups)</h3>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1 text-surface-500"># append to /boot/firmware/cmdline.txt (same line as the rest):</div>
            <div><span className="select-all">usbcore.autosuspend=-1</span></div>
          </div>

          <h3 className="mt-5 mb-2 text-[13px] font-semibold text-surface-200">6. Keep the Pi responsive when logged out</h3>
          <p className="mb-2 text-sm leading-relaxed text-surface-400">
            Default Raspberry Pi OS uses CPU governor <code>ondemand</code> which idles at the
            lowest frequency. Ramping up when the phone talks adds ~50 ms. Stick it at <code>performance</code>
            if the Pi is a dedicated server (heat &amp; draw go up slightly):
          </p>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1"><span className="text-surface-500">$</span> echo performance | sudo tee /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor</div>
            <div className="mb-1 text-surface-500"># persist via cpufrequtils:</div>
            <div className="mb-1 text-surface-500"># install cpufrequtils with your distro&apos;s standard package flow</div>
            <div><span className="text-surface-500">$</span> echo &apos;GOVERNOR=&quot;performance&quot;&apos; | sudo tee /etc/default/cpufrequtils</div>
          </div>
        </section>

        {/* ── Verify ───────────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Verify end-to-end</h2>
          <div className="overflow-x-auto rounded-xl bg-surface-950 p-4 font-mono text-[12px] text-surface-300">
            <div className="mb-1 text-surface-500"># on the Pi:</div>
            <div className="mb-1"><span className="text-surface-500">$</span> yaver doctor</div>
            <div className="mb-3 text-surface-500"># expect: Hermes Reload Stack / Node / embedded hermesc all ✓</div>
            <div className="mb-1 text-surface-500"># then on the phone:</div>
            <div className="mb-1 text-surface-500"># 1. Open the Yaver app &rarr; Devices tab</div>
            <div className="mb-1 text-surface-500"># 2. your Pi hostname appears; tap to connect</div>
            <div className="mb-1 text-surface-500"># 3. Hot Reload tab shows your RN projects on the Pi</div>
            <div className="text-surface-500"># 4. Tap &ldquo;Open in Yaver&rdquo; &rarr; Hermes bundle lands in under 2s</div>
          </div>
        </section>

        {/* ── Gotchas ──────────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Gotchas (field notes)</h2>
          <ul className="space-y-3 text-sm leading-relaxed text-surface-400">
            <li>
              <span className="font-semibold text-surface-200">Under-voltage kills WiFi silently.</span>
              {" "}
              <code>vcgencmd get_throttled</code> returns non-zero means your PSU is
              under-spec. Use the official one.
            </li>
            <li>
              <span className="font-semibold text-surface-200">SD cards wear out.</span> Log
              rotation helps. Better: boot from SSD via USB 3.0. Modern Pi bootloader
              supports USB boot out of the box.
            </li>
            <li>
              <span className="font-semibold text-surface-200">Metro OOM on 2 GB Pi 4.</span> Expo
              projects with many dependencies spike above 1.5 GB during the first
              bundle. Use 4 GB+ or add a swap file.
            </li>
            <li>
              <span className="font-semibold text-surface-200">WiFi NIC disconnects after
              idle.</span> That&apos;s the power-save setting above. Check with{" "}
              <code>iw dev wlan0 get power_save</code> after reboot; should say <code>off</code>.
            </li>
            <li>
              <span className="font-semibold text-surface-200">Router hands out a new IP.</span> The
              phone still finds the Pi via the LAN beacon + Convex registry on IP
              change, but give DHCP reservations to your Pi&apos;s MAC if you want
              stable SSH access.
            </li>
          </ul>
        </section>

        {/* ── Next steps ───────────────────────────────────────── */}
        <section className="mb-12">
          <h2 className="mb-3 text-lg font-semibold text-surface-100">Next</h2>
          <ul className="space-y-2 text-sm text-surface-400">
            <li>
              &bull;{" "}
              <Link href="/manuals/cli-setup" className="underline hover:text-surface-300">
                CLI setup &amp; usage
              </Link>{" "}
              &mdash; projects, tasks, runners, reload
            </li>
            <li>
              &bull;{" "}
              <Link href="/manuals/auto-boot" className="underline hover:text-surface-300">
                Auto-boot on any platform
              </Link>{" "}
              &mdash; same idea for Mac Mini, Linux desktop, VPS
            </li>
            <li>
              &bull;{" "}
              <Link href="/manuals/code-from-beach" className="underline hover:text-surface-300">
                Code from the beach
              </Link>{" "}
              &mdash; the full phone &rarr; Pi &rarr; TestFlight loop
            </li>
            <li>
              &bull;{" "}
              <Link href="/download#headless-auth" className="underline hover:text-surface-300">
                All 8 sign-in providers
              </Link>{" "}
              &mdash; Google, Apple, Microsoft, GitHub, GitLab, Discord, Slack, email
            </li>
          </ul>
        </section>
      </div>
    </div>
  );
}
