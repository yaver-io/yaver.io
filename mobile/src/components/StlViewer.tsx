// StlViewer — a self-contained 3D viewer for an STL model, rendered in a WebView
// with three.js (loaded from a CDN) + OrbitControls so you can rotate/zoom a
// model on the phone. The STL bytes arrive base64-encoded over the mesh
// (cad_get); we inject them into the page and parse with STLLoader.
//
// This is a plain-web viewer (not a third-party RN app), so a WebView is the
// right tool — no native 3D dependency needed. If three fails to load (offline),
// the page shows a message and the caller can fall back to the PNG preview.
import React, { useMemo } from "react";
import { View } from "react-native";
import { WebView } from "react-native-webview";

export function StlViewer({ base64, height = 320, background = "#0b0f14" }: { base64: string; height?: number; background?: string }) {
  const html = useMemo(() => buildHtml(base64, background), [base64, background]);
  return (
    <View style={{ height, borderRadius: 12, overflow: "hidden", backgroundColor: background }}>
      <WebView
        originWhitelist={["*"]}
        source={{ html }}
        style={{ backgroundColor: background }}
        scrollEnabled={false}
        javaScriptEnabled
        domStorageEnabled
      />
    </View>
  );
}

function buildHtml(base64: string, bg: string): string {
  // three r150+ ESM from jsDelivr; STLLoader + OrbitControls from the examples.
  return `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1">
<style>html,body{margin:0;height:100%;background:${bg};overflow:hidden}#msg{color:#9aa4b2;font:13px -apple-system,system-ui;padding:10px}</style>
</head><body><div id="msg">Loading 3D…</div>
<script type="importmap">{"imports":{"three":"https://cdn.jsdelivr.net/npm/three@0.160.0/build/three.module.js","three/addons/":"https://cdn.jsdelivr.net/npm/three@0.160.0/examples/jsm/"}}</script>
<script type="module">
import * as THREE from 'three';
import { STLLoader } from 'three/addons/loaders/STLLoader.js';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
const msg = document.getElementById('msg');
try {
  const b64 = "${base64}";
  const bin = atob(b64); const buf = new ArrayBuffer(bin.length); const view = new Uint8Array(buf);
  for (let i=0;i<bin.length;i++) view[i]=bin.charCodeAt(i);
  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(45, innerWidth/innerHeight, 0.1, 5000);
  const renderer = new THREE.WebGLRenderer({antialias:true});
  renderer.setSize(innerWidth, innerHeight); renderer.setPixelRatio(devicePixelRatio);
  document.body.appendChild(renderer.domElement);
  scene.add(new THREE.HemisphereLight(0xffffff, 0x444444, 1.1));
  const dir = new THREE.DirectionalLight(0xffffff, 0.8); dir.position.set(1,1,1); scene.add(dir);
  const geo = new STLLoader().parse(buf);
  geo.computeVertexNormals(); geo.center();
  const mat = new THREE.MeshStandardMaterial({color:0x4f9cf9, metalness:0.1, roughness:0.6});
  const mesh = new THREE.Mesh(geo, mat); scene.add(mesh);
  geo.computeBoundingSphere();
  const r = geo.boundingSphere.radius || 50;
  camera.position.set(r*1.8, r*1.4, r*1.8); camera.lookAt(0,0,0);
  const controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true; controls.autoRotate = true; controls.autoRotateSpeed = 1.2;
  scene.add(new THREE.GridHelper(r*4, 16, 0x223044, 0x161d27));
  msg.style.display='none';
  addEventListener('resize', () => { camera.aspect=innerWidth/innerHeight; camera.updateProjectionMatrix(); renderer.setSize(innerWidth,innerHeight); });
  (function loop(){ requestAnimationFrame(loop); controls.update(); renderer.render(scene,camera); })();
} catch(e) { msg.textContent = '3D viewer failed: ' + e.message + ' (showing PNG preview instead)'; }
</script></body></html>`;
}
