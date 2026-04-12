module.exports = function (api) {
  api.cache(true);
  return {
    presets: [
      ["babel-preset-expo", {
        jsxImportSource: "nativewind",
        // Bento doesn't animate; reanimated's plugin pulls in react-native-worklets
        // which requires RN >= 0.78, while we're on 0.76.3. Disabling is safe
        // because reanimated's native module is only imported from components
        // we don't use (drawer/gesture-handler extras).
        reanimated: false,
      }],
      "nativewind/babel",
    ],
  };
};
