const path = require('path');

function loadSDKManifest() {
  const manifestPath = path.resolve(__dirname, '..', 'sdk-manifest.json');
  delete require.cache[manifestPath];
  return require(manifestPath);
}

module.exports = { loadSDKManifest };
