import React from "react";
import { Pressable, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { useColors } from "../context/ThemeContext";

/** Old deep links remain navigable but cannot start provider OAuth or copy credentials. */
export default function RunnerSignInDisabled() {
 const router=useRouter();const c=useColors();
 return <View style={{flex:1,padding:24,gap:18,justifyContent:"center",backgroundColor:c.bg}}>
  <Text style={{color:c.textPrimary,fontSize:20}}>Sign in from your remote shell</Text>
  <Text style={{color:c.textMuted}}>Runner-provider sign-in and credential transfer are disabled in Yaver. Open SSH and use the runner's own sign-in command. Your Yaver account approval is separate.</Text>
  <Pressable accessibilityRole="button" onPress={()=>router.replace("/plain")}><Text style={{color:c.accent}}>Open SSH</Text></Pressable>
  <Pressable accessibilityRole="button" onPress={()=>router.canGoBack()?router.back():router.replace("/ssh")}><Text style={{color:c.accent}}>Back</Text></Pressable>
 </View>;
}
