import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Windows installer exit codes — Yaver",
  description: "Return codes used by the Yaver Windows EXE installer distributed through Microsoft Store.",
};

const codes = [
  {
    code: "0",
    meaning: "Installation completed successfully.",
    action: "No action is required.",
  },
  {
    code: "1",
    meaning: "The installation was cancelled by the user.",
    action: "Run the installer again when ready and approve any requested Windows prompt.",
  },
  {
    code: "2",
    meaning: "The installer aborted because it could not complete the requested installation.",
    action: "Retry from a standard signed Yaver installer. If it fails again, contact Yaver support with the installer version and Windows version; do not include passwords, tokens, source code, or private file paths.",
  },
] as const;

export default function WindowsInstallerExitCodesPage() {
  return (
    <main className="mx-auto max-w-3xl px-6 py-16 text-surface-200">
      <h1 className="text-3xl font-semibold text-surface-50">Yaver Windows installer exit codes</h1>
      <p className="mt-4 text-surface-400">
        These codes apply to the signed Yaver NSIS EXE installer distributed through Microsoft Store
        and the versioned Yaver download service. Silent installation uses <code>/S</code>.
      </p>

      <div className="mt-8 overflow-hidden rounded-xl border border-surface-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-surface-900 text-surface-300">
            <tr>
              <th className="px-4 py-3">Code</th>
              <th className="px-4 py-3">Meaning</th>
              <th className="px-4 py-3">Recommended action</th>
            </tr>
          </thead>
          <tbody>
            {codes.map((item) => (
              <tr key={item.code} className="border-t border-surface-800 align-top">
                <td className="px-4 py-3 font-mono text-surface-100">{item.code}</td>
                <td className="px-4 py-3">{item.meaning}</td>
                <td className="px-4 py-3 text-surface-400">{item.action}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <p className="mt-8 text-sm text-surface-400">
        Yaver does not currently emit distinct installer-level codes for disk-full, reboot-required,
        network, already-installed, concurrent-installation, or policy-rejection scenarios. Microsoft
        Store or Windows may report those conditions independently.
      </p>
      <p className="mt-4 text-sm text-surface-400">
        Support and legal contact information is available in the <a className="underline" href="/terms">Yaver Terms of Service</a>.
      </p>
    </main>
  );
}
