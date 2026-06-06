package main

// screenlog_input_producers.go — the per-platform input producers that
// emit the JSON-line protocol consumed by screenlog_input_capture.go.
//
// Each producer prints one InputEvent JSON object per line to stdout:
//   {"t":1717,"type":"click","button":"left","x":840,"y":210}
//   {"t":1718,"type":"key","key":"Return"}
//   {"t":1719,"type":"scroll","dy":-1,"x":840,"y":210}
//
// Top use case: the agent runs in WSL and we want the WINDOWS host's
// input. A WSL (Linux) process can't install a Windows hook, so we run a
// low-level WH_KEYBOARD_LL/WH_MOUSE_LL hook inside `powershell.exe` (Windows
// interop) and stream JSON back over the pipe.

// powershellInputHookScript returns a PowerShell program that installs
// global low-level keyboard + mouse hooks and streams each click / scroll /
// keystroke as a JSON line. Mouse MOVES are intentionally NOT emitted (they
// flood). Runs a message loop until the parent kills it.
func powershellInputHookScript() string {
	return `$src = @"
using System;
using System.Runtime.InteropServices;
using System.Windows.Forms;
public class YaverHook {
  [StructLayout(LayoutKind.Sequential)] public struct POINT { public int x; public int y; }
  [StructLayout(LayoutKind.Sequential)] public struct MSLL { public POINT pt; public uint mouseData; public uint flags; public uint time; public IntPtr extra; }
  [StructLayout(LayoutKind.Sequential)] public struct KBDLL { public uint vkCode; public uint scanCode; public uint flags; public uint time; public IntPtr extra; }
  public delegate IntPtr Proc(int code, IntPtr w, IntPtr l);
  [DllImport("user32")] static extern IntPtr SetWindowsHookEx(int id, Proc cb, IntPtr mod, uint th);
  [DllImport("user32")] static extern IntPtr CallNextHookEx(IntPtr h, int code, IntPtr w, IntPtr l);
  [DllImport("kernel32")] static extern IntPtr GetModuleHandle(string n);
  const int WH_KEYBOARD_LL=13, WH_MOUSE_LL=14;
  const int WM_KEYDOWN=0x100, WM_SYSKEYDOWN=0x104;
  const int WM_LBUTTONDOWN=0x201, WM_RBUTTONDOWN=0x204, WM_MBUTTONDOWN=0x207, WM_MOUSEWHEEL=0x20A;
  static IntPtr kh, mh; static Proc kp, mp;
  static long Now(){ return (long)(DateTime.UtcNow - new DateTime(1970,1,1)).TotalMilliseconds; }
  static void Emit(string s){ try { Console.Out.WriteLine(s); Console.Out.Flush(); } catch {} }
  static IntPtr KCb(int code, IntPtr w, IntPtr l){
    if(code>=0 && ((int)w==WM_KEYDOWN || (int)w==WM_SYSKEYDOWN)){
      KBDLL k=(KBDLL)Marshal.PtrToStructure(l, typeof(KBDLL));
      string name=((Keys)k.vkCode).ToString();
      Emit("{\"t\":"+Now()+",\"type\":\"key\",\"key\":\""+name+"\"}");
    }
    return CallNextHookEx(kh, code, w, l);
  }
  static IntPtr MCb(int code, IntPtr w, IntPtr l){
    if(code>=0){
      MSLL m=(MSLL)Marshal.PtrToStructure(l, typeof(MSLL));
      int msg=(int)w;
      if(msg==WM_LBUTTONDOWN) Emit("{\"t\":"+Now()+",\"type\":\"click\",\"button\":\"left\",\"x\":"+m.pt.x+",\"y\":"+m.pt.y+"}");
      else if(msg==WM_RBUTTONDOWN) Emit("{\"t\":"+Now()+",\"type\":\"click\",\"button\":\"right\",\"x\":"+m.pt.x+",\"y\":"+m.pt.y+"}");
      else if(msg==WM_MBUTTONDOWN) Emit("{\"t\":"+Now()+",\"type\":\"click\",\"button\":\"middle\",\"x\":"+m.pt.x+",\"y\":"+m.pt.y+"}");
      else if(msg==WM_MOUSEWHEEL){ int d=((short)((m.mouseData>>16)&0xffff))/120; Emit("{\"t\":"+Now()+",\"type\":\"scroll\",\"dy\":"+d+",\"x\":"+m.pt.x+",\"y\":"+m.pt.y+"}"); }
    }
    return CallNextHookEx(mh, code, w, l);
  }
  public static void Run(){
    kp=new Proc(KCb); mp=new Proc(MCb);
    IntPtr hm=GetModuleHandle(null);
    kh=SetWindowsHookEx(WH_KEYBOARD_LL, kp, hm, 0);
    mh=SetWindowsHookEx(WH_MOUSE_LL, mp, hm, 0);
    Application.Run();
  }
}
"@
Add-Type -TypeDefinition $src -ReferencedAssemblies System.Windows.Forms
[YaverHook]::Run()`
}

// linuxXinputProducer returns a shell pipeline that turns `xinput
// test-xi2 --root` output into the JSON-line protocol. Best-effort, X11
// only, no coordinates (XI2 raw events don't carry root coords cheaply) —
// real Linux capture (evdev) is a documented follow-up. Keys carry the
// X keycode; clicks carry the button number.
func linuxXinputProducer() string {
	// awk: on "RawKeyPress"/"RawButtonPress" remember the type; on the
	// following "detail:" line, emit a JSON event with an epoch-ms stamp.
	return `xinput test-xi2 --root 2>/dev/null | awk '
		/RawKeyPress/   { ev="key"; next }
		/RawButtonPress/{ ev="click"; next }
		/detail:/ {
			if (ev=="") next
			"date +%s%3N" | getline ms; close("date +%s%3N")
			d=$2
			if (ev=="key") printf "{\"t\":%s,\"type\":\"key\",\"key\":\"X%s\"}\n", ms, d
			else printf "{\"t\":%s,\"type\":\"click\",\"button\":\"%s\"}\n", ms, d
			fflush()
			ev=""
		}'`
}
