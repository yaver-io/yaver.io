module.exports = {
  dependency: {
    platforms: {
      ios: { podspecPath: 'YaverPlainSSH.podspec' },
      android: {
        sourceDir: 'android',
        packageImportPath: 'import io.yaver.plainssh.YaverPlainSSHPackage;',
        packageInstance: 'new YaverPlainSSHPackage()',
      },
    },
  },
};
