import React from "react";
import { Image, type ImageStyle, StyleSheet, View, type ViewStyle } from "react-native";

const APP_ICON = require("../../assets/icon.png");

/**
 * The one canonical Yaver mark used before authentication and during startup.
 * Keeping the asset reference here prevents individual entry points from
 * recreating (or tinting) the Y into the legacy blue chevron.
 */
export function YaverAppIcon({
  size = 92,
  style,
  imageStyle,
}: {
  size?: number;
  style?: ViewStyle;
  imageStyle?: ImageStyle;
}) {
  return (
    <View
      accessibilityRole="image"
      accessibilityLabel="Yaver Y"
      style={[styles.tile, { width: size, height: size, borderRadius: Math.round(size * 0.23) }, style]}
    >
      <Image source={APP_ICON} resizeMode="cover" style={[styles.image, imageStyle]} />
    </View>
  );
}

const styles = StyleSheet.create({
  tile: {
    overflow: "hidden",
    backgroundColor: "#fff",
  },
  image: {
    width: "100%",
    height: "100%",
  },
});
