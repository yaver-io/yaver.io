"use strict";

/**
 * Provision binaries only. Authentication and provider/model configuration
 * remain entirely owned by each native CLI and are never changed by Yaver.
 * A complete development/CI node has all supported runners available, so the
 * plan installs only the missing binaries without touching existing installs.
 */
function codingRunnerBootstrapPlan(entries, commandExists) {
  const installed = entries.filter((entry) => commandExists(entry.command));
  return {
    installed,
    toInstall: entries.filter((entry) => !installed.includes(entry)),
  };
}

module.exports = { codingRunnerBootstrapPlan };
