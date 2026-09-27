import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export function loadContract() {
  return JSON.parse(fs.readFileSync(path.join(here, "product-contract.json"), "utf8"));
}

export function evaluateReadiness(contract = loadContract()) {
  const blockers = Object.entries(contract.releaseGates)
    .filter(([, passed]) => passed !== true)
    .map(([gate]) => gate);
  return {
    publishable: contract.claimsPlayStationSupport === true && blockers.length === 0,
    blockers,
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const result = evaluateReadiness();
  console.log(JSON.stringify(result, null, 2));
  process.exitCode = result.publishable ? 0 : 2;
}
