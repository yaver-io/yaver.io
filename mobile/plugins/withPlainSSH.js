const { withAppBuildGradle } = require("@expo/config-plugins");

module.exports = function withPlainSSH(config) {
  return withAppBuildGradle(config, (mod) => {
    const dependency = 'implementation files("../../native/plain-ssh/build/plainssh.aar")';
    if (!mod.modResults.contents.includes(dependency)) {
      mod.modResults.contents = mod.modResults.contents.replace("dependencies {", `dependencies {\n    ${dependency}`);
    }
    return mod;
  });
};
