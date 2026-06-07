// CameraStream — Bambu-P1S-style live camera view, iOS-robust.
//
// iOS WKWebView does NOT render multipart/x-mixed-replace MJPEG in an <img>
// (a long-standing WebKit limitation), so instead of the /robot/stream MJPEG we
// **poll single JPEG snapshots** (robotd GET /robot/snapshot) and swap a
// preloaded <img> — which works on iOS, Android and the web. The heavy
// capture/encode stays on the edge; this is a thin viewer. ~1–2 fps (the grab
// rate), which is plenty for monitoring a slow Cartesian robot.
import React from "react";
import { View, type ViewStyle } from "react-native";
import { WebView } from "react-native-webview";

export function CameraStream({
  snapshotUrl,
  intervalMs = 700,
  style,
}: {
  snapshotUrl: string;
  intervalMs?: number;
  style?: ViewStyle;
}) {
  const html = `<!doctype html><html><head>
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
    <style>
      html,body{margin:0;height:100%;background:#000;overflow:hidden}
      .wrap{position:fixed;inset:0;display:flex;align-items:center;justify-content:center}
      img{max-width:100%;max-height:100%;object-fit:contain;display:block}
      .msg{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;color:#888;font:14px -apple-system,system-ui;text-align:center;padding:24px}
    </style></head>
    <body>
      <div class="msg" id="msg">connecting to camera…</div>
      <div class="wrap"><img id="cam"/></div>
      <script>
        var base = ${JSON.stringify(snapshotUrl)};
        var img = document.getElementById('cam');
        var msg = document.getElementById('msg');
        var fails = 0;
        function tick(){
          if(!base){ msg.innerText='set the robot host'; return; }
          var n = new Image();
          n.onload = function(){ img.src = n.src; msg.style.display='none'; fails=0; };
          n.onerror = function(){ fails++; if(fails>2){ msg.style.display='flex'; msg.innerText='no camera\\n('+base+')'; } };
          n.src = base + (base.indexOf('?')>=0?'&':'?') + 't=' + Date.now();
        }
        setInterval(tick, ${intervalMs});
        tick();
      </script>
    </body></html>`;
  return (
    <View style={[{ backgroundColor: "#000", overflow: "hidden" }, style]}>
      <WebView
        key={snapshotUrl}
        source={{ html }}
        style={{ flex: 1, backgroundColor: "#000" }}
        originWhitelist={["*"]}
        scrollEnabled={false}
        javaScriptEnabled
        domStorageEnabled={false}
        allowsInlineMediaPlayback
        mediaPlaybackRequiresUserAction={false}
        androidLayerType="hardware"
      />
    </View>
  );
}
