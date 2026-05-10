import { internalAction } from "./_generated/server";
import { v } from "convex/values";

const RESEND_API_KEY = process.env.RESEND_API_KEY ?? "";

/**
 * Send an email via Resend. Fire-and-forget — failures are logged but don't
 * break the calling flow.
 */
export const send = internalAction({
  args: {
    from: v.string(),
    to: v.string(),
    subject: v.string(),
    html: v.string(),
    replyTo: v.optional(v.string()),
  },
  handler: async (_ctx, args) => {
    if (!RESEND_API_KEY) {
      console.warn("RESEND_API_KEY not configured — skipping email");
      return;
    }

    try {
      const body: Record<string, unknown> = {
        from: args.from,
        to: [args.to],
        subject: args.subject,
        html: args.html,
      };
      if (args.replyTo) body.reply_to = args.replyTo;

      const resp = await fetch("https://api.resend.com/emails", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${RESEND_API_KEY}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(body),
      });

      if (!resp.ok) {
        const text = await resp.text();
        console.error(`Resend API ${resp.status}: ${text}`);
      }
    } catch (e: any) {
      console.error(`Resend fetch error: ${e.message ?? e}`);
    }
  },
});

// ── Email Templates ─────────────────────────────────────────────────

export function passwordResetHtml(resetUrl: string): string {
  return `
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:480px;margin:0 auto;padding:32px 16px;color:#1a1a1a;">
  <h2 style="margin:0 0 8px;">Reset your password</h2>
  <p style="margin:0 0 24px;color:#666;font-size:14px;">
    We received a request to reset your Yaver account password.
  </p>

  <div style="text-align:center;margin:0 0 24px;">
    <a href="${resetUrl}"
       style="display:inline-block;background:#1a1a1a;color:#fff;padding:14px 32px;border-radius:8px;font-size:15px;font-weight:600;text-decoration:none;">
      Reset Password
    </a>
  </div>

  <p style="font-size:13px;line-height:1.6;color:#666;">
    This link expires in 1 hour. If you didn't request a password reset, you can safely ignore this email.
  </p>

  <p style="font-size:12px;color:#999;margin:32px 0 0;">
    If the button doesn't work, copy and paste this URL into your browser:<br>
    <span style="color:#444;word-break:break-all;">${resetUrl}</span>
  </p>
</body>
</html>`.trim();
}

export function welcomeHtml(name: string): string {
  const firstName = name.split(" ")[0] || name;
  return `
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:520px;margin:0 auto;padding:32px 16px;color:#1a1a1a;">
  <p style="font-size:15px;line-height:1.7;margin:0 0 20px;">Hi ${firstName},</p>

  <p style="font-size:15px;line-height:1.7;margin:0 0 20px;">
    Welcome to Yaver. Your account is now active. Yaver gives you:
  </p>

  <ul style="font-size:14px;line-height:2;color:#333;padding-left:20px;margin:0 0 20px;">
    <li>Use any AI coding agent (Claude Code, Codex, Aider, Ollama) from your phone</li>
    <li>P2P connection to your dev machine — no code touches our servers</li>
    <li>Push React Native apps to your phone for real-device testing</li>
    <li>Hot reload on the same Wi-Fi or remotely through our relay — works from anywhere, even on 5G</li>
    <li>Voice input, visual feedback, and autonomous bug fixing</li>
    <li>Feedback SDK — drop into any React Native, Flutter, or web app to capture screenshots, voice reports, and crash logs that go straight to your AI agent for automatic fixes</li>
  </ul>

  <p style="font-size:15px;line-height:1.7;margin:0 0 20px;">
    <strong>Get started:</strong> Install the desktop agent with <code style="background:#f0f0f0;padding:2px 6px;border-radius:4px;font-size:13px;">npm install -g yaver-cli</code>,
    run <code style="background:#f0f0f0;padding:2px 6px;border-radius:4px;font-size:13px;">yaver auth</code>, and connect from the mobile app.
    Add the <a href="https://yaver.io/docs/feedback-sdk" style="color:#1a1a1a;">Feedback SDK</a> to your app for visual bug reports.
  </p>

  <p style="font-size:15px;line-height:1.7;margin:0 0 8px;">
    Please don't hesitate to reach out if there's anything we can do to improve Yaver for you.
  </p>

  <p style="font-size:15px;line-height:1.7;margin:24px 0 0;">
    Best,<br>
    Kivanc from Yaver
  </p>

  <p style="font-size:12px;color:#999;margin:24px 0 0;">
    <a href="https://yaver.io" style="color:#666;">yaver.io</a> &middot;
    <a href="https://github.com/kivanccakmak/yaver.io" style="color:#666;">GitHub</a>
  </p>
</body>
</html>`.trim();
}

// ── Security-event templates ────────────────────────────────────────
// Every destructive auth operation (link a provider, unlink one, merge
// another account) triggers a heads-up email. Keeps users in the loop
// so a stolen session can't silently alter their account without them
// noticing.

function securityShell(title: string, body: string): string {
  return `
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:520px;margin:0 auto;padding:32px 16px;color:#1a1a1a;">
  <h2 style="margin:0 0 8px;">${title}</h2>
  ${body}
  <p style="font-size:12px;color:#999;margin:32px 0 0;">
    If this wasn't you, reply to this email or contact <a href="mailto:kivanc@yaver.io" style="color:#666;">kivanc@yaver.io</a> right away.
  </p>
  <p style="font-size:12px;color:#999;margin:8px 0 0;">
    <a href="https://yaver.io" style="color:#666;">yaver.io</a>
  </p>
</body>
</html>`.trim();
}

export function providerLinkedHtml(name: string, provider: string): string {
  return securityShell(
    `Sign-in method added: ${provider}`,
    `
  <p style="margin:0 0 16px;color:#444;font-size:15px;">Hi ${name || "there"},</p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    We linked <strong>${provider}</strong> to your Yaver account. You can now
    sign in to the same account using any of your linked providers.
  </p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    Manage your sign-in methods from the Yaver dashboard → Settings.
  </p>`
  );
}

export function providerUnlinkedHtml(name: string, provider: string): string {
  return securityShell(
    `Sign-in method removed: ${provider}`,
    `
  <p style="margin:0 0 16px;color:#444;font-size:15px;">Hi ${name || "there"},</p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    We removed <strong>${provider}</strong> from your Yaver account. You
    won't be able to sign in with that provider anymore.
  </p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    Add it back any time from the Yaver dashboard → Settings.
  </p>`
  );
}

export function accountsMergedHtml(name: string, mergedFrom: string, mergedInto: string): string {
  return securityShell(
    "Accounts merged",
    `
  <p style="margin:0 0 16px;color:#444;font-size:15px;">Hi ${name || "there"},</p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    We merged <strong>${mergedFrom}</strong> into <strong>${mergedInto}</strong>.
    Everything from ${mergedFrom} — devices, linked providers, settings,
    projects, guest access, and history — now lives on ${mergedInto}. The
    old account has been deleted.
  </p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    Use any of ${mergedInto}'s sign-in methods to access the combined account.
  </p>`
  );
}

export function mergeStartedHtml(name: string, targetEmail: string): string {
  return securityShell(
    "Account-merge request started",
    `
  <p style="margin:0 0 16px;color:#444;font-size:15px;">Hi ${name || "there"},</p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    A merge request was started on <strong>${targetEmail}</strong>. If
    approved from the other Yaver account, that other account will be
    deleted and its data will move onto ${targetEmail}.
  </p>
  <p style="margin:0 0 16px;color:#444;font-size:15px;">
    If you didn't do this, sign into the Yaver dashboard → Settings →
    "Merge another account" → Cancel.
  </p>`
  );
}

export function guestInviteHtml(hostName: string, inviteCode: string): string {
  return `
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:520px;margin:0 auto;padding:32px 16px;color:#1a1a1a;">
  <h2 style="margin:0 0 8px;">You're invited to Yaver</h2>
  <p style="margin:0 0 20px;color:#666;font-size:15px;">
    <strong>${hostName}</strong> invited you to access their machine through Yaver.
  </p>

  <div style="background:#f5f5f5;border-radius:12px;padding:24px;text-align:center;margin:0 0 24px;">
    <p style="margin:0 0 8px;font-size:13px;color:#666;">Your invite code</p>
    <p style="margin:0;font-size:32px;font-weight:700;letter-spacing:6px;font-family:monospace;">${inviteCode}</p>
  </div>

  <p style="font-size:15px;line-height:1.7;margin:0 0 20px;">
    As a guest, you can:
  </p>

  <ul style="font-size:14px;line-height:2;color:#333;padding-left:20px;margin:0 0 20px;">
    <li>Send tasks to AI coding agents running on ${hostName}'s machine</li>
    <li>Hot reload apps on your phone — same Wi-Fi or remotely over 5G</li>
    <li>Submit visual feedback with screenshots and voice notes</li>
    <li>Test apps with push-to-device (React Native)</li>
    <li>All traffic is P2P — your code never touches our servers</li>
  </ul>

  <p style="font-size:15px;line-height:1.7;margin:0 0 20px;">
    <strong>To accept:</strong>
  </p>

  <ol style="font-size:14px;line-height:2;color:#333;padding-left:20px;margin:0 0 20px;">
    <li>Download <strong>Yaver</strong> from the <a href="https://apps.apple.com/app/yaver/id6760467669" style="color:#1a1a1a;">App Store</a> or <a href="https://play.google.com/store/apps/details?id=io.yaver.mobile" style="color:#1a1a1a;">Google Play</a></li>
    <li>Sign in with any account (Apple, Google, or Microsoft)</li>
    <li>Enter the invite code above</li>
  </ol>

  <p style="font-size:12px;color:#999;margin:32px 0 0;">
    This code expires in 2 days. If you didn't expect this, you can safely ignore it.
  </p>
</body>
</html>`.trim();
}
