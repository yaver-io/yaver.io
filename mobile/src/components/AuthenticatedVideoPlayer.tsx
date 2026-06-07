import React, { useEffect, useMemo, useState } from "react";
import { ActivityIndicator, StyleProp, StyleSheet, Text, View, ViewStyle } from "react-native";
import { useVideoPlayer, VideoView, type VideoSource } from "expo-video";

type Props = {
  uri: string | null | undefined;
  headers?: Record<string, string>;
  style?: StyleProp<ViewStyle>;
  autoPlay?: boolean;
  loop?: boolean;
  muted?: boolean;
  onEnd?: () => void;
};

export function AuthenticatedVideoPlayer({
  uri,
  headers,
  style,
  autoPlay = true,
  loop = false,
  muted = false,
  onEnd,
}: Props) {
  const [status, setStatus] = useState<string>(uri ? "loading" : "idle");
  const [error, setError] = useState<string | null>(null);

  const source = useMemo<VideoSource>(() => {
    if (!uri) return null;
    return {
      uri,
      headers: headers ? { ...headers } : undefined,
      contentType: "progressive",
      useCaching: false,
    };
  }, [uri, headers]);

  const player = useVideoPlayer(source, (p) => {
    p.loop = loop;
    p.muted = muted;
    p.staysActiveInBackground = false;
    p.showNowPlayingNotification = false;
    if (autoPlay && uri) {
      p.play();
    }
  });

  useEffect(() => {
    setStatus(uri ? "loading" : "idle");
    setError(null);
  }, [uri]);

  useEffect(() => {
    const statusSub = (player as any).addListener?.("statusChange", (event: any) => {
      setStatus(String(event?.status ?? ""));
      if (event?.error) {
        setError(event.error.message || event.error.localizedDescription || "Video failed to load.");
      }
    });
    const endSub = (player as any).addListener?.("playToEnd", () => {
      onEnd?.();
    });
    return () => {
      statusSub?.remove?.();
      endSub?.remove?.();
    };
  }, [player, onEnd]);

  if (!uri) {
    return (
      <View style={[styles.wrap, style]}>
        <Text style={styles.muted}>No playable video stream.</Text>
      </View>
    );
  }

  return (
    <View style={[styles.wrap, style]}>
      <VideoView
        player={player}
        style={StyleSheet.absoluteFill}
        contentFit="contain"
        nativeControls
        allowsFullscreen
        allowsPictureInPicture
      />
      {status === "loading" ? (
        <View style={styles.overlay} pointerEvents="none">
          <ActivityIndicator color="#ffffff" />
        </View>
      ) : null}
      {status === "error" || error ? (
        <View style={styles.overlay}>
          <Text style={styles.error}>{error ?? "Video failed to load."}</Text>
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    minHeight: 180,
    backgroundColor: "#000000",
    overflow: "hidden",
  },
  overlay: {
    ...StyleSheet.absoluteFillObject,
    alignItems: "center",
    justifyContent: "center",
    padding: 16,
    backgroundColor: "rgba(0,0,0,0.35)",
  },
  error: {
    color: "#fecaca",
    fontSize: 13,
    fontWeight: "600",
    textAlign: "center",
  },
  muted: {
    color: "#8a8a8a",
    fontSize: 13,
    textAlign: "center",
  },
});
