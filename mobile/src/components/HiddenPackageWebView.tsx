// HiddenPackageWebView.tsx — an off-screen WebView that loads an allowlisted
// public page using THIS phone's connection (its residential IP), injects an
// extraction script that pulls the package's selectors out of the rendered DOM,
// and resolves with the fields. This is the on-phone collector for JS-rendered
// pages the agent's plain fetch can't read. It detects CAPTCHA/login and stops
// (never bypasses). See docs/yaver-task-packages.md (mobile target).

import React, { forwardRef, useImperativeHandle, useRef, useState } from "react";
import { View } from "react-native";
import { WebView } from "react-native-webview";

export type ExtractRequest = { url: string; selectors: Record<string, string> };
export type ExtractResult = {
  status: "ok" | "blocked_challenge" | "error";
  fields: Record<string, string>;
  error?: string;
};
export type HiddenExtractorHandle = { extract: (req: ExtractRequest) => Promise<ExtractResult> };

const EXTRACT_TIMEOUT_MS = 20000;

export const HiddenPackageWebView = forwardRef<HiddenExtractorHandle>((_props, ref) => {
  const [uri, setUri] = useState<string | null>(null);
  const [selectors, setSelectors] = useState<Record<string, string>>({});
  const pending = useRef<((r: ExtractResult) => void) | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const finish = (r: ExtractResult) => {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    const resolve = pending.current;
    pending.current = null;
    setUri(null);
    if (resolve) resolve(r);
  };

  useImperativeHandle(ref, () => ({
    extract: (req: ExtractRequest) =>
      new Promise<ExtractResult>((resolve) => {
        pending.current = resolve;
        setSelectors(req.selectors || {});
        setUri(req.url);
        timer.current = setTimeout(
          () => finish({ status: "error", fields: {}, error: "timeout" }),
          EXTRACT_TIMEOUT_MS,
        );
      }),
  }));

  const injected = () => {
    const sel = JSON.stringify(selectors);
    return `(function(){try{
      var t=((document.body&&document.body.innerText)||"").toLowerCase();
      if(/captcha|are you a robot|just a moment|cf-challenge|sign in to continue/.test(t)){
        window.ReactNativeWebView.postMessage(JSON.stringify({status:"blocked_challenge",fields:{}}));return;}
      var s=${sel};var out={};
      for(var k in s){try{var el=document.querySelector(s[k]);if(el){out[k]=((el.innerText||el.textContent||"")+"").trim();}}catch(e){}}
      window.ReactNativeWebView.postMessage(JSON.stringify({status:"ok",fields:out}));
    }catch(e){window.ReactNativeWebView.postMessage(JSON.stringify({status:"error",fields:{},error:String(e)}));}})();true;`;
  };

  if (!uri) return null;
  return (
    <View style={{ width: 0, height: 0, opacity: 0, position: "absolute" }} pointerEvents="none">
      <WebView
        source={{ uri }}
        injectedJavaScript={injected()}
        onMessage={(e) => {
          try {
            finish(JSON.parse(e.nativeEvent.data) as ExtractResult);
          } catch {
            finish({ status: "error", fields: {}, error: "bad message" });
          }
        }}
        javaScriptEnabled
        domStorageEnabled
        // a real residential UA so the page renders normally
        userAgent="Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Mobile Safari/537.36"
      />
    </View>
  );
});

HiddenPackageWebView.displayName = "HiddenPackageWebView";
