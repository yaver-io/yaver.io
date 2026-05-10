import React from "react";
import {
  Modal,
  ModalProps,
  Pressable,
  StyleProp,
  StyleSheet,
  View,
  ViewStyle,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../context/ThemeContext";
import { useResponsiveLayout } from "../../hooks/useResponsiveLayout";
import { layoutTokens } from "../../theme/tokens";

type DialogSize = keyof typeof layoutTokens.dialog;

// AdaptiveDialog — single source of truth for modal sizing across
// the app. On phone, renders as a bottom-sheet style overlay
// (rounded top, full width). On tablet, centers a card with the
// width preset from layoutTokens.dialog.
//
// Replaces ad-hoc maxWidth: 380/460/540 patterns scattered across
// RunnerAuthModal, TaskTargetWizard, OpenCodeConfigModal, etc.
export function AdaptiveDialog({
  visible,
  onClose,
  size = "form",
  children,
  dismissOnBackdrop = true,
  presentation = "auto",
  bodyStyle,
  ...modalProps
}: {
  visible: boolean;
  onClose: () => void;
  size?: DialogSize;
  children: React.ReactNode;
  dismissOnBackdrop?: boolean;
  presentation?: "auto" | "sheet" | "centered";
  bodyStyle?: StyleProp<ViewStyle>;
} & Omit<ModalProps, "visible" | "onRequestClose" | "children">) {
  const layout = useResponsiveLayout();
  const c = useColors();
  const insets = useSafeAreaInsets();

  const wantSheet = presentation === "sheet" || (presentation === "auto" && layout.layoutClass === "phone");
  const width = layoutTokens.dialog[size];

  const containerStyle = wantSheet
    ? [
        styles.sheetContainer,
        {
          backgroundColor: c.surface,
          paddingBottom: Math.max(insets.bottom, 12),
          maxHeight: "92%" as const,
        },
      ]
    : [
        styles.centeredCard,
        {
          backgroundColor: c.surface,
          width,
          maxWidth: "92%" as const,
          maxHeight: "88%" as const,
          borderColor: c.border,
        },
      ];

  return (
    <Modal
      visible={visible}
      onRequestClose={onClose}
      transparent
      animationType={wantSheet ? "slide" : "fade"}
      {...modalProps}
    >
      <Pressable
        style={[styles.backdrop, { justifyContent: wantSheet ? "flex-end" : "center" }]}
        onPress={dismissOnBackdrop ? onClose : undefined}
      >
        <Pressable
          // Stop the inner press from bubbling to the backdrop
          // dismiss handler. Using Pressable not View so we don't
          // need a TouchableWithoutFeedback wrapper.
          onPress={(e) => e.stopPropagation()}
          style={[containerStyle, bodyStyle]}
        >
          {children}
        </Pressable>
      </Pressable>
    </Modal>
  );
}

const styles = StyleSheet.create({
  backdrop: {
    flex: 1,
    backgroundColor: "rgba(0,0,0,0.5)",
    alignItems: "center",
  },
  sheetContainer: {
    width: "100%",
    borderTopLeftRadius: 24,
    borderTopRightRadius: 24,
    paddingTop: 14,
  },
  centeredCard: {
    borderRadius: 16,
    borderWidth: 1,
    overflow: "hidden",
  },
});
