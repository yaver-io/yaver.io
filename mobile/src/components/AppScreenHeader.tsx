import React from "react";
import { StyleProp, StyleSheet, Text, View, ViewStyle } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../context/ThemeContext";
import { AppBackButton } from "./AppBackButton";
import { DeviceAttentionBanner } from "./DeviceAttentionBanner";

// AppScreenHeader is the canonical app bar for every screen the user
// reaches via the More menu (Settings, Files, Builds, Healthmon, etc.)
// — i.e. every (tabs)/* screen that sets headerShown: false in the tab
// layout because it brings its own header.
//
// It mirrors the react-navigation default Header used by the visible
// tabs (Hot Reload, Tasks, Projects, Devices, More) so the user sees
// exactly the same bar shape across the app: same height, same title
// weight, same back-button placement, same border. The
// DeviceAttentionBanner is rendered immediately below so the cross-
// screen claim/reauth CTA is present everywhere — matching what the
// tab layout's HeaderWithBanner does for screens that DO use the
// default navigation header.
export function AppScreenHeader({
  title,
  onBack,
  backLabel = "Back",
  right,
  style,
}: {
  title: string;
  onBack: () => void;
  backLabel?: string;
  right?: React.ReactNode;
  style?: StyleProp<ViewStyle>;
}) {
  const c = useColors();
  const insets = useSafeAreaInsets();

  return (
    <View>
      <View
        style={[
          styles.header,
          {
            backgroundColor: c.bg,
            borderBottomColor: c.border,
            paddingTop: insets.top + 8,
          },
          style,
        ]}
      >
        <AppBackButton onPress={onBack} label={backLabel} />
        <Text style={[styles.title, { color: c.textPrimary }]} numberOfLines={1}>
          {title}
        </Text>
        <View style={styles.rightSlot}>{right}</View>
      </View>
      <DeviceAttentionBanner />
    </View>
  );
}

const styles = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 10,
    borderBottomWidth: 1,
  },
  title: {
    fontSize: 17,
    fontWeight: "700",
  },
  rightSlot: {
    minWidth: 50,
    alignItems: "flex-end",
  },
});
