// Post-build: replace whatever shebang bun bundled into dist/cli.js
// with `#!/usr/bin/env node`, and mark it executable. Bun copies the
// source's `#!/usr/bin/env bun` into the bundled output, which would
// make plain-Node users get a syntax error. We ship a Node-friendly
// binary because the npm audience is Node-first.

import * as fs from "node:fs";

for (const name of ["dist/cli.js"]) {
  let src = fs.readFileSync(name, "utf8");
  if (src.startsWith("#!")) {
    const nl = src.indexOf("\n");
    src = src.slice(nl + 1);
  }
  fs.writeFileSync(name, "#!/usr/bin/env node\n" + src);
  fs.chmodSync(name, 0o755);
}

console.log("shebangs fixed");
