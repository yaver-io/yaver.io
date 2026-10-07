import React, { useEffect, useRef, useState } from "react";
import { AppState, KeyboardAvoidingView, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as Crypto from "expo-crypto";
import * as Speech from "expo-speech";
import { Buffer } from "buffer";
import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import { saveSSHVoiceTarget } from "../src/lib/plainSSHVoice";
import RemoteSSHEnrollment from "../src/components/RemoteSSHEnrollment";
import YaverAccountCard from "../src/components/YaverAccountCard";
import XtermView, { type XtermHandle } from "../src/components/PaneTerminalView";
import { startRealtimeTranscribe } from "../src/lib/speech";
import { appendTerminalSpeechText, terminalSpeechExcerpt } from "../src/lib/terminalSpeech";
import { loadSSHHosts, loadSSHCredentials, saveSSHHost, removeSSHHost, sshCall, sshBytes, sshInput, plainSSHUnavailable, type SSHHost, type SSHPane } from "../src/lib/plainSSH";

/** Independent of AuthContext/DeviceContext: the SSH handshake authorizes this
 * lane. A cloud sign-out cannot interrupt it and tailnet IPs grant no trust. */
export default function PlainScreen() {
  const c = useColors();
  const router = useRouter();
  const [hosts, setHosts] = useState<SSHHost[]>([]);
  const [selected, setSelected] = useState<SSHHost | null>(null);
  const [panes, setPanes] = useState<SSHPane[] | null>(null);
  const [pane, setPane] = useState<SSHPane | null>(null);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [accountCard, setAccountCard] = useState(false);
  const [host, setHost] = useState("");
  const [port, setPort] = useState("22");
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [authMode, setAuthMode] = useState<"key" | "password" | "tailnet">("key");
  const [candidate, setCandidate] = useState<SSHHost | null>(null);
  const [draft, setDraft] = useState("");
  const [submittedInput, setSubmittedInput] = useState("");
  const [sending, setSending] = useState(false);
  const [dictating, setDictating] = useState(false);
  const connection = useRef<string | null>(null);
  const generation = useRef(0);
  const terminal = useRef<XtermHandle | null>(null);
  const ready = useRef(false);
  const pendingOutput = useRef<Uint8Array[]>([]);
  const speechText = useRef("");
  const recording = useRef<{stop: () => Promise<string>} | null>(null);
  const writeQueue = useRef(Promise.resolve());
  const alive = useRef(true);

  const report = (cause: unknown) => { if (alive.current) setError(cause instanceof Error ? cause.message : "SSH operation failed. Retry the connection."); };
  const detach = () => {
    generation.current++;
    const id = connection.current; connection.current = null;
    if (id) void sshCall({ op: "close", id }).catch(() => {});
    const rec = recording.current; recording.current = null;
    if (rec) void rec.stop().catch(() => {});
    void Speech.stop();
    ready.current = false; pendingOutput.current = []; speechText.current = "";
    setDictating(false); setPane(null); setPanes(null); setBusy(false); setStatus("Detached · remote panes keep running");
  };
  useEffect(() => {
    alive.current = true;
    void loadSSHHosts().then((items) => { if (alive.current) setHosts(items); }).catch(report);
    const sub = AppState.addEventListener("change", (state) => {
      if (state === "background") {
        generation.current++;
        const id=connection.current; connection.current=null;
        if(id) void sshCall({op:"close",id}).catch(()=>{});
        const rec=recording.current; recording.current=null;
        if(rec) void rec.stop().catch(()=>{});
        void Speech.stop(); setDictating(false); setBusy(false);
        setStatus("Disconnected in background · reconnect to resume the same pane");
      }
    });
    return () => {
      alive.current = false; generation.current++;
      const id = connection.current; connection.current = null;
      if (id) void sshCall({ op: "close", id }).catch(() => {});
      if (recording.current) void recording.current.stop().catch(() => {});
      void Speech.stop(); sub.remove();
    };
  }, []);

  const connect = async (target: SSHHost, resume?: SSHPane) => {
    detach(); if (!resume) {setDraft("");setSubmittedInput("");} setSelected(target); setError(""); setBusy(true); setStatus("Connecting over SSH…");
    const attempt = generation.current;
    let id: string | undefined;
    try {
      const credentials = await loadSSHCredentials(target.id);
      const result = await sshCall<{id: string}>({ ...target, ...credentials, op: "connect" }); id = result.id;
      if (!alive.current || attempt !== generation.current) { await sshCall({op: "close", id}); return; }
      connection.current = id;
      setStatus("Reading tmux panes…");
      const items = await sshCall<SSHPane[]>({op: "panes", id});
      if (attempt !== generation.current || !alive.current) return;
      setPanes(items); setStatus("Connected over SSH");
      if (resume) {
        const exact = items.find((item) => item.id===resume.id && item.sessionId===resume.sessionId && item.identity===resume.identity);
        if (!exact) throw new Error("The previous pane has exited. Choose a current pane; no input was replayed.");
        void openPane(exact);
      }
    } catch (cause) {
      if (id) void sshCall({op: "close", id}).catch(() => {});
      if (attempt === generation.current) { connection.current = null; report(cause); setStatus("Disconnected"); }
    } finally { if (attempt === generation.current && alive.current) setBusy(false); }
  };

  const openPane = async (target: SSHPane) => {
    const id = connection.current; if (!id) return;
    const attempt = generation.current;
    setBusy(true); setError(""); setStatus("Attaching to pane…");
    try {
      await sshCall({op: "open", id, pane: target.id, session: target.sessionId, identity: target.identity});
      if (!alive.current || generation.current !== attempt) return;
      setPane(target); setStatus(`${target.session} · window ${target.window} · ${target.id}`); setBusy(false);
      while (alive.current && generation.current === attempt) {
        const frame = await sshCall<{data: string}>({op: "read", id});
        if (!alive.current || generation.current !== attempt) return;
        if (!frame.data) continue;
        const bytes = sshBytes(frame.data);
        speechText.current = appendTerminalSpeechText(speechText.current, Buffer.from(bytes).toString("utf8"));
        if (ready.current) terminal.current?.write(bytes);
        else {
          pendingOutput.current.push(bytes);
          if (pendingOutput.current.reduce((total, part) => total + part.length, 0) > 1024 * 1024) throw new Error("Terminal did not become ready. Reconnect to the pane.");
        }
      }
    } catch (cause) {
      if (attempt === generation.current) { report(cause); setStatus("Disconnected · reconnect to resume"); }
      void sshCall({op: "close", id}).catch(() => {});
      if (connection.current === id) connection.current = null;
    } finally { if (alive.current && attempt === generation.current) setBusy(false); }
  };

  const send = (data: string | Uint8Array, op = "write") => {
    const id = connection.current;
    if (!id || !pane) { const cause = new Error("Reconnect to the pane before sending input."); report(cause); return Promise.reject(cause); }
    // Hardware keys and composed messages share one ordered stream. Never
    // replay an uncertain write after reconnect: that could execute twice.
    const operation = writeQueue.current.then(async () => {
      if (connection.current !== id) throw new Error("Connection changed; input was not sent.");
      await sshCall({op, id, data: sshInput(data)});
    });
    writeQueue.current = operation.catch(report);
    return operation;
  };
  const probe = async () => {
    setBusy(true); setError(""); setCandidate(null); setStatus("Checking SSH host key…");
    try {
      const target = { id: Crypto.randomUUID(), host: host.trim(), port: Number(port), user: user.trim() };
      if (!Number.isInteger(target.port) || target.port < 1 || target.port > 65535) throw new Error("Enter an SSH port between 1 and 65535.");
      const result = await sshCall<{fingerprint: string}>({...target, op: "probe"});
      if (alive.current) setCandidate({...target, fingerprint: result.fingerprint});
    } catch (cause) { report(cause); } finally { if (alive.current) { setBusy(false); setStatus(""); } }
  };
  const trust = async () => {
    if (!candidate) return;
    setBusy(true); setError("");
    try {
      const target = candidate;
      await saveSSHHost(target, authMode === "key" ? {privateKey, passphrase} : authMode === "password" ? {password} : {});
      setHosts(await loadSSHHosts()); setCandidate(null); setPassword(""); setPrivateKey(""); setPassphrase("");
      await connect(target);
    } catch (cause) { report(cause); } finally { if (alive.current) setBusy(false); }
  };
  const dictate = async () => {
    if (dictating) {
      const rec = recording.current; recording.current = null; setDictating(false);
      if (rec) { try { const text = await rec.stop(); if (alive.current) setDraft((previous) => [previous, text.trim()].filter(Boolean).join(" ")); } catch (cause) { report(cause); } }
      return;
    }
    setDictating(true); const attempt = generation.current;
    try {
      const rec = await startRealtimeTranscribe(() => {});
      if (!alive.current || attempt !== generation.current) { await rec.stop(); return; }
      recording.current = rec;
    } catch (cause) { setDictating(false); report(cause); }
  };
  const button = (title: string, action: () => void, disabled = busy) => (
    <Pressable accessibilityRole="button" accessibilityLabel={title} disabled={disabled} onPress={action} style={[styles.button, {borderColor: c.border, opacity: disabled ? 0.45 : 1}]}>
      <Text style={{color: c.textPrimary, fontWeight: "600"}}>{title}</Text>
    </Pressable>
  );
  const field = (label: string, value: string, change: (value: string) => void, secret = false, multiline = false) => (
    <TextInput accessibilityLabel={label} placeholder={label} placeholderTextColor={c.textMuted} value={value}
      onChangeText={(text) => {setCandidate(null); change(text);}} secureTextEntry={secret && !multiline} multiline={multiline}
      autoCapitalize="none" autoCorrect={false} spellCheck={false} style={[styles.field, {color:c.textPrimary, borderColor:c.border}]} />
  );
  return (
    <SafeAreaView style={{flex:1, backgroundColor:c.bg}}>
      <KeyboardAvoidingView style={{flex:1}} behavior={Platform.OS === "ios" ? "padding" : undefined}>
        <View style={styles.header}>
          <AppBackButton onPress={() => {detach();if(router.canGoBack()) router.back();else router.replace("/");}} />
          <Text style={{color:c.textPrimary, fontSize:18, fontWeight:"700"}}>Plain SSH</Text>
          {button("Yaver", () => setAccountCard((visible) => !visible), false)}
          {(pane || panes) && button("Detach", detach, false)}
        </View>
        {accountCard && <><YaverAccountCard onClose={() => setAccountCard(false)} />{connection.current && <RemoteSSHEnrollment connectionId={connection.current} />}</>}
        {!!status && <Text accessibilityLiveRegion="polite" style={[styles.note, {color:c.textMuted}]}>{status}</Text>}
        {!!error && <Text accessibilityRole="alert" style={[styles.note, {color:c.error}]}>{error}</Text>}
        {plainSSHUnavailable ? <View style={styles.content}><Text style={{color:c.textPrimary}}>{plainSSHUnavailable}</Text>{button("Open Yaver chat", () => router.push("/"), false)}</View>
          : pane ? <>
            <XtermView submittedInput={submittedInput} ref={terminal} style={{flex:1}} columns={pane.width} rows={pane.height} onError={setError} onData={(bytes) => {void send(bytes).catch(() => {});}} onReady={() => {
              ready.current = true; for (const bytes of pendingOutput.current) terminal.current?.write(bytes); pendingOutput.current = [];
            }} />
            <ScrollView horizontal style={{flexGrow:0}} contentContainerStyle={styles.keys} keyboardShouldPersistTaps="handled">
              {button("Esc", () => {void send("\x1b").catch(() => {});}, false)}{button("Tab", () => {void send("\t").catch(() => {});}, false)}{button("Ctrl-C", () => {void send("\x03").catch(() => {});}, false)}
              {button("↑", () => {void send("\x1b[A").catch(() => {});}, false)}{button("↓", () => {void send("\x1b[B").catch(() => {});}, false)}{button("Enter", () => {void send("\r").catch(() => {});}, false)}
              {button(dictating ? "Stop dictation" : "Dictate", () => {void dictate();}, false)}
              {button("Read output", () => {const text = terminalSpeechExcerpt(speechText.current); if (text) Speech.speak(text);}, false)}
              {selected && button("Use this pane in CarPlay", () => {void saveSSHVoiceTarget({hostId:selected.id,pane}).then(()=>setStatus("CarPlay will use this pane · every voice submission asks for confirmation")).catch(report);}, false)}
              {button("Stop reading", () => {void Speech.stop();}, false)}
              {!connection.current && selected && button("Reconnect", () => {void connect(selected, pane);}, false)}
            </ScrollView>
            <View style={styles.composer}>
              <TextInput accessibilityLabel="Message to selected pane" multiline value={draft} onChangeText={setDraft}
                placeholder="Type or dictate, then send to this pane" placeholderTextColor={c.textMuted} autoCapitalize="none"
                style={[styles.field, {flex:1, maxHeight:120, color:c.textPrimary, borderColor:c.border}]} />
              {button("Send", () => {
                const text = draft; if (!text || !connection.current || !terminal.current) return;
                setSending(true);
                void send(text, "submit").then(() => {setSubmittedInput(text);setDraft((current) => current===text ? "" : current);}).catch(() => {}).finally(()=>setSending(false));
              }, sending || !draft || !connection.current)}
            </View>
          </> : <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
            {panes ? <>
              <Text style={{color:c.textPrimary}}>Choose the pane you left running</Text>
              {panes.map((item) => <View key={`${item.sessionId}:${item.id}`}>{button(`${item.session} · ${item.window} · ${item.id} · ${item.command}`, () => {void openPane(item);})}</View>)}
              {!panes.length && <Text style={{color:c.textMuted}}>No tmux panes are running for this SSH user. On the host, run tmux new -s work and start your runner, then refresh.</Text>}
              {selected && button("Refresh panes", () => {void connect(selected);})}
            </> : <>
              <Text style={{color:c.textMuted}}>Continue an existing tmux pane over SSH. No Yaver account required.</Text>
              {hosts.map((item) => <View key={item.id} style={styles.saved}>
                <View style={{flex:1}}>{button(`${item.user}@${item.host}`, () => {void connect(item);})}</View>
                {button(`Forget ${item.host}`, () => {void removeSSHHost(item.id).then(loadSSHHosts).then(setHosts).catch(report);})}
              </View>)}
              {selected && !!error && button("Retry connection", () => {void connect(selected);})}
              {field("Hostname or Tailscale address", host, setHost)}{field("SSH port", port, setPort)}{field("SSH username", user, setUser)}
              <View style={styles.keys}>{button("SSH key", () => {setAuthMode("key");setCandidate(null);})}{button("Password", () => {setAuthMode("password");setCandidate(null);})}{button("Tailscale SSH", () => {setAuthMode("tailnet");setCandidate(null);})}</View>
              {authMode === "key" ? <>{field("Private key (stored only on this device)", privateKey, setPrivateKey, true, true)}{field("Key passphrase", passphrase, setPassphrase, true)}</> : authMode === "password" ? field("SSH password", password, setPassword, true) : <Text style={{color:c.textMuted}}>Use this only when the host runs Tailscale SSH and its policy authorizes this phone and SSH user. Ordinary SSH over Tailscale still needs a key or password.</Text>}
              {candidate ? <View style={{gap:12}}>
                <Text selectable style={{color:c.textPrimary}}>Verify this host key on your machine:{"\n"}{candidate.fingerprint}</Text>
                <Text style={{color:c.textMuted}}>Compare with ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub on the host (or the host key algorithm your server uses).</Text>
                {button("Trust host and connect", () => {void trust();})}
              </View> : button("Check host key", () => {void probe();}, busy || !host.trim() || !user.trim() || (authMode === "key" && !privateKey.trim()) || (authMode === "password" && !password))}
            </>}
          </ScrollView>}
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
const styles = StyleSheet.create({
  header:{flexDirection:"row",justifyContent:"space-between",alignItems:"center",paddingHorizontal:16,paddingVertical:8},
  content:{padding:16,gap:12,flexGrow:1,width:"100%",maxWidth:680,alignSelf:"center"}, note:{paddingHorizontal:16,paddingVertical:4,fontSize:12},
  button:{paddingHorizontal:12,paddingVertical:11,borderWidth:1,borderRadius:10,alignItems:"center"},
  field:{borderWidth:1,borderRadius:10,padding:12,minHeight:44},
  saved:{flexDirection:"row",gap:8}, keys:{flexDirection:"row",gap:6,padding:6},
  composer:{flexDirection:"row",alignItems:"flex-end",gap:8,padding:8},
});
