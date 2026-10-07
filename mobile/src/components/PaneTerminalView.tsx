import React, { forwardRef, useRef, useState } from "react";
import { Pressable, ScrollView, Text, TextInput, View } from "react-native";
import XtermView, { type XtermHandle, type XtermViewProps } from "./XtermView";
import { useColors } from "../context/ThemeContext";

export type { XtermHandle } from "./XtermView";

/** Presentation only: the terminal and its transport stay mounted in BOTH
 * modes. Changing this toggle cannot reconnect, resize or launch a runner. */
const PaneTerminalView = forwardRef<XtermHandle, XtermViewProps & { submittedInput?: string; inputEnabled?: boolean }>(function PaneTerminalView(
  { style, submittedInput, inputEnabled = true, onScreen, ...terminalProps }, ref,
) {
  const c = useColors();
  const [mode, setMode] = useState<"raw" | "chat">("raw");
  const [screen, setScreen] = useState("");
  const [draft, setDraft] = useState("");
  const [lastInput, setLastInput] = useState("");
  const inner = useRef<XtermHandle | null>(null);
  const input = submittedInput ?? lastInput;
  return <View style={[{flex:1}, style]}>
    <View style={{flexDirection:"row", justifyContent:"flex-end", paddingHorizontal:8, paddingVertical:4}}>
      <View accessibilityRole="tablist" style={{flexDirection:"row", borderRadius:8, borderWidth:1, borderColor:c.border, overflow:"hidden"}}>
        {(["raw", "chat"] as const).map((value) => <Pressable key={value} accessibilityRole="tab" accessibilityState={{selected:mode===value}} aria-selected={mode===value}
          onPress={() => setMode(value)} style={{paddingHorizontal:12,paddingVertical:7,backgroundColor:mode===value?c.bgCard:"transparent"}}>
          <Text style={{fontSize:12,color:mode===value?c.textPrimary:c.textMuted}}>{value==="raw"?"Raw":"Pane chat"}</Text>
        </Pressable>)}
      </View>
    </View>
    <View style={{flex:1,position:"relative"}}>
      <View pointerEvents={mode==="raw"?"auto":"none"} accessibilityElementsHidden={mode!=="raw"} importantForAccessibility={mode==="raw"?"auto":"no-hide-descendants"}
        style={{position:"absolute",top:0,left:0,right:0,bottom:0,opacity:mode==="raw"?1:0}}>
        <XtermView {...terminalProps} ref={(instance) => {inner.current=instance;if(typeof ref==="function")ref(instance);else if(ref)ref.current=instance;}} onScreen={(text) => {setScreen(text);onScreen?.(text);}} />
      </View>
      {mode==="chat" && <ScrollView style={{flex:1}} contentContainerStyle={{padding:12,gap:12}} keyboardShouldPersistTaps="handled">
        {!!input && <View style={{alignSelf:"flex-end",maxWidth:"90%",padding:12,borderRadius:12,backgroundColor:c.bgCard}}>
          <Text selectable style={{color:c.textPrimary}}>{input}</Text>
        </View>}
        <View style={{padding:12,borderRadius:12,borderWidth:1,borderColor:c.border}}>
          <Text style={{fontSize:11,color:c.textMuted,marginBottom:8}}>Live pane</Text>
          <Text testID="live-pane-output" selectable style={{color:c.textPrimary,fontFamily:"monospace",fontSize:13,lineHeight:19}}>{screen || "Waiting for pane output…"}</Text>
        </View>
      </ScrollView>}
    </View>
    {mode==="chat" && submittedInput === undefined && terminalProps.onData && <View style={{flexDirection:"row",gap:8,padding:8,alignItems:"flex-end"}}>
      <TextInput accessibilityLabel="Message to selected pane" multiline editable={inputEnabled} value={draft} onChangeText={setDraft}
        placeholder="Type a message to this pane" placeholderTextColor={c.textMuted}
        style={{flex:1,minHeight:42,maxHeight:120,padding:10,borderWidth:1,borderColor:c.border,borderRadius:10,color:c.textPrimary}} />
      <Pressable accessibilityRole="button" disabled={!inputEnabled || !draft} onPress={()=>{if(inner.current){inner.current.submit(draft);setLastInput(draft);setDraft("");}}} style={{padding:12}}>
        <Text style={{color:inputEnabled&&draft?c.accent:c.textMuted}}>Send</Text>
      </Pressable>
    </View>}
  </View>;
});
export default PaneTerminalView;
