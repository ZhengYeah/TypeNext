param([ValidateRange(0, 60)][int]$WatchSeconds = 0)
$ErrorActionPreference = 'Stop'

# Read-only diagnostics: window classes and selection metadata. This never reads
# Text/Name/FullName, activates a window, edits a slide, or calls a model.
if (-not ('TypeNextPPTMetadata' -as [type])) {
Add-Type -TypeDefinition @'
using System;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Text;
public static class TypeNextPPTMetadata {
 public delegate bool EnumProc(IntPtr hwnd, IntPtr arg);
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int l,t,r,b; }
 [StructLayout(LayoutKind.Sequential)] public struct Info { public uint size,flags; public IntPtr active,focus,capture,menu,move,caret; public Rect box; }
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc cb,IntPtr arg);
 [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr hwnd,EnumProc cb,IntPtr arg);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll")] static extern bool GetGUIThreadInfo(uint thread,ref Info info);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd,StringBuilder b,int n);
 [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] static extern IntPtr GetParent(IntPtr hwnd);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr hwnd);
 [DllImport("oleacc.dll")] static extern int AccessibleObjectFromWindow(IntPtr hwnd,uint id,ref Guid iid,[MarshalAs(UnmanagedType.IDispatch)] out object result);
 static string Class(IntPtr hwnd) { var b=new StringBuilder(256); GetClassName(hwnd,b,b.Capacity); return b.ToString(); }
 static object Prop(object obj,string name) { return obj.GetType().InvokeMember(name,BindingFlags.GetProperty,null,obj,null); }
 static void Show(object obj,string name) {
  try { Console.WriteLine("  PROPERTY {0}={1}",name,Prop(obj,name)); }
  catch(Exception e) {while(e.InnerException!=null)e=e.InnerException;Console.WriteLine("  PROPERTY {0} error=0x{1:X8}",name,e.HResult);}
 }
 static void Release(object obj) { if(obj!=null && Marshal.IsComObject(obj)) Marshal.ReleaseComObject(obj); }
 static void NativeModel(IntPtr w) {
  object doc=null,pane=null,selection=null,range=null,presentation=null,application=null;
  try {
   var iid=new Guid("00020400-0000-0000-C000-000000000046");
   int hr=AccessibleObjectFromWindow(w,0xfffffff0,ref iid,out doc);
   Console.WriteLine("  NATIVEOM hr=0x{0:X8} available={1}",hr,doc!=null);
   if(hr<0 || doc==null)return;
   Show(doc,"Active"); Show(doc,"ViewType");
   pane=Prop(doc,"ActivePane"); Console.WriteLine("  PANE view={0}",Prop(pane,"ViewType"));
   presentation=Prop(doc,"Presentation"); Show(presentation,"ReadOnly");
   application=Prop(presentation,"Application"); Show(application,"IsSandboxed");
   selection=Prop(doc,"Selection"); var type=Prop(selection,"Type"); Console.WriteLine("  SELECTION type={0}",type);
   if(Convert.ToInt32(type)==3) {
    range=Prop(selection,"TextRange"); Console.WriteLine("  CARET start={0} length={1}",Prop(range,"Start"),Prop(range,"Length"));
   }
  } catch(Exception e) {while(e.InnerException!=null)e=e.InnerException;Console.WriteLine("  METADATA error=0x{0:X8}",e.HResult);}
  finally {Release(range);Release(selection);Release(application);Release(presentation);Release(pane);Release(doc);}
 }
 public static void Inspect(uint target,bool details) {
  int count=0;
  EnumWindows((w,a)=> {
   uint pid;uint thread=GetWindowThreadProcessId(w,out pid);
   if(pid!=target || !IsWindowVisible(w))return true;
   count++; var g=new Info();g.size=(uint)Marshal.SizeOf(g);bool got=GetGUIThreadInfo(thread,ref g);
   Console.WriteLine("WINDOW hwnd={0:X} class={1} foreground={2} gui={3} focus={4:X}/{5} caret={6:X}/{7}",w.ToInt64(),Class(w),GetForegroundWindow()==w,got,g.focus.ToInt64(),Class(g.focus),g.caret.ToInt64(),Class(g.caret));
   EnumChildWindows(w,(c,x)=> {
    string name=Class(c);
    bool native=name.Equals("paneClassDC",StringComparison.OrdinalIgnoreCase)||name.Equals("mdiClass",StringComparison.OrdinalIgnoreCase);
    if(details || native) Console.WriteLine(" CHILD hwnd={0:X} parent={1:X} visible={2} class={3}",c.ToInt64(),GetParent(c).ToInt64(),IsWindowVisible(c),name);
    if(native && IsWindowVisible(c))NativeModel(c);
    return true;
   },IntPtr.Zero);
   return true;
  },IntPtr.Zero);
  Console.WriteLine("Visible PowerPoint windows={0}",count);
 }
}
'@
}

$pptProcesses = @(Get-Process POWERPNT -ErrorAction Stop)
$deadline = [DateTime]::UtcNow.AddSeconds($WatchSeconds)
$first = $true
do {
    foreach ($pptProcess in $pptProcesses) {
        [TypeNextPPTMetadata]::Inspect([uint32]$pptProcess.Id, $first)
    }
    $first = $false
    if ([DateTime]::UtcNow -ge $deadline) { break }
    Start-Sleep -Seconds 2
} while ($true)
