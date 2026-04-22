const fs = require('fs');
const semver = require('semver');

// Pure JS packages — never need native code
const PURE_JS_PACKAGES = new Set([
  'axios', 'lodash', 'moment', 'date-fns', 'uuid', 'dayjs',
  'zustand', 'jotai', 'redux', '@reduxjs/toolkit', 'mobx', 'mobx-react',
  'react-query', '@tanstack/react-query',
  'formik', 'yup', 'zod', 'react-hook-form',
  'i18next', 'react-i18next',
  'nativewind', 'twrnc', 'styled-components', '@emotion/native',
  'swr', 'immer',
  '@react-navigation/native', '@react-navigation/stack',
  '@react-navigation/bottom-tabs', '@react-navigation/drawer',
  '@react-navigation/native-stack',
  '@react-navigation/material-top-tabs',
  'react-native-web', 'react-dom',
  '@expo/metro-runtime', 'expo-router', 'expo-splash-screen', 'expo-status-bar',
  'three', '@react-three/fiber', '@react-three/drei',
]);

// Packages that LOOK native (react-native-*) but are pure JS
const FALSE_POSITIVE_NATIVE = new Set([
  'react-native-paper', 'react-native-elements',
  'react-native-size-matters', 'react-native-responsive-screen',
  'react-native-toast-message', 'react-native-responsive-fontsize',
  'react-native-iphone-x-helper', 'react-native-status-bar-height',
  'react-native-markdown-display',
]);

function analyzeProject(packageJson, sdkManifest) {
  const errors = [];
  const warnings = [];
  const availableModules = [];
  const missingModules = [];

  const allDeps = { ...packageJson.dependencies, ...packageJson.peerDependencies };

  // 1. React Native version
  const projectRN = cleanVersion(allDeps['react-native'] || '');
  const sdkRN = sdkManifest.reactNative;

  if (projectRN && sdkRN) {
    const projParsed = semver.coerce(projectRN);
    const sdkParsed = semver.coerce(sdkRN);

    if (projParsed && sdkParsed) {
      if (semver.major(projParsed) !== semver.major(sdkParsed)) {
        errors.push({
          type: 'rn_major_mismatch',
          message: `React Native major version mismatch: project ${projectRN}, yaver ${sdkRN}. Incompatible.`,
        });
      } else if (semver.minor(projParsed) !== semver.minor(sdkParsed)) {
        warnings.push({
          type: 'rn_minor_mismatch',
          message: `React Native ${projectRN} vs yaver ${sdkRN}. Minor version differs — may work.`,
        });
      }
    }
  }

  // 2. Architecture check
  const newArchEnabled = packageJson.reactNative?.newArchEnabled === true;
  if (newArchEnabled && !sdkManifest.arch.newArch) {
    errors.push({
      type: 'arch_mismatch',
      message: 'Project uses New Architecture but yaver uses classic bridge. Incompatible.',
    });
  }

  // 3. Native module check
  for (const [name, version] of Object.entries(allDeps)) {
    if (name === 'react' || name === 'react-native') continue;
    if (PURE_JS_PACKAGES.has(name)) continue;
    if (FALSE_POSITIVE_NATIVE.has(name)) continue;
    if (packageJson.devDependencies?.[name] && !packageJson.dependencies?.[name]) continue;
    if (!looksLikeNativeModule(name, sdkManifest)) continue;

    const sdkVersion = sdkManifest.nativeModules[name];

    if (!sdkVersion) {
      missingModules.push({ name, version: cleanVersion(version), reason: 'not in yaver SDK' });
      errors.push({
        type: 'missing_module', module: name, version,
        message: `"${name}" requires native code but is NOT in the yaver SDK.`,
      });
    } else {
      const cleanLocal = cleanVersion(version);
      availableModules.push({ name, projectVersion: cleanLocal, sdkVersion });

      const localParsed = semver.coerce(cleanLocal);
      const sdkParsed = semver.coerce(sdkVersion);
      if (localParsed && sdkParsed && hasPotentiallyBreakingVersionDrift(localParsed, sdkParsed)) {
        warnings.push({
          type: 'version_mismatch', module: name,
          message: `"${name}": project ${version}, yaver ${sdkVersion}. Version differs at a potentially breaking boundary.`,
        });
      }
    }
  }

  return { reactNativeVersion: projectRN, errors, warnings, availableModules, missingModules };
}

function looksLikeNativeModule(name, sdkManifest) {
  if (sdkManifest?.nativeModules?.[name]) return true;
  return name.startsWith('react-native-') ||
    name.startsWith('@react-native-') ||
    name.startsWith('@react-native/') ||
    name.startsWith('rn') ||
    name.startsWith('expo-') ||
    name.startsWith('@shopify/react-native-');
}

function cleanVersion(v) {
  return (v || '').replace(/[\^~>=<\s]/g, '');
}

function hasPotentiallyBreakingVersionDrift(projectVersion, sdkVersion) {
  if (semver.major(projectVersion) !== semver.major(sdkVersion)) return true;
  if (semver.major(projectVersion) === 0 && semver.minor(projectVersion) !== semver.minor(sdkVersion)) {
    return true;
  }
  return false;
}

module.exports = { analyzeProject, PURE_JS_PACKAGES, FALSE_POSITIVE_NATIVE };
