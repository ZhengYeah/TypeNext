param(
    [ValidateSet('Weixin', 'WeChat')][string]$ProcessName = 'Weixin',
    [ValidateRange(0, 55)][int]$WatchSeconds = 0,
    [switch]$ProbeProvider
)
$ErrorActionPreference = 'Stop'
# Only accessibility metadata is printed: never Name, Value, Text or chat titles.
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
if (-not ('TypeNextFocusMetadata' -as [type])) {
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class TypeNextFocusMetadata {
 public delegate bool EnumProc(IntPtr hwnd,IntPtr arg);
 [StructLayout(LayoutKind.Sequential)] public struct Rect {public int l,t,r,b;}
 [StructLayout(LayoutKind.Sequential)] public struct Info {public uint size,flags; public IntPtr active,focus,capture,menu,move,caret; public Rect box;}
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb,IntPtr arg);
 [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr hwnd,EnumProc cb,IntPtr arg);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll")] public static extern bool GetGUIThreadInfo(uint thread,ref Info info);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd,StringBuilder b,int n);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern IntPtr SendMessageTimeout(IntPtr hwnd,uint message,IntPtr wp,IntPtr lp,uint flags,uint timeout,out IntPtr result);
 [DllImport("oleacc.dll")] static extern int AccessibleObjectFromWindow(IntPtr hwnd,uint id,ref Guid iid,[MarshalAs(UnmanagedType.IDispatch)] out object result);
 public static string Class(IntPtr hwnd) {var b=new StringBuilder(256);GetClassName(hwnd,b,b.Capacity);return b.ToString();}
 public static void Probe(IntPtr hwnd) {
  IntPtr result;SendMessageTimeout(hwnd,0x003d,IntPtr.Zero,new IntPtr(-25),2,1000,out result);
  Console.WriteLine("UIA provider response available={0}",result!=IntPtr.Zero);
  object acc=null;var iid=new Guid("618736e0-3c3d-11cf-810c-00aa00389b71");
  try {
   int hr=AccessibleObjectFromWindow(hwnd,0xfffffffc,ref iid,out acc);
   Console.WriteLine("MSAA client available={0}; hr=0x{1:X8}",acc!=null,hr);
   if(acc!=null) {
    foreach(string property in new[]{"accRole","accState","accChildCount"}) {
     object[] args=property=="accChildCount"?null:new object[]{0};
     try {Console.WriteLine("MSAA {0}={1}",property,acc.GetType().InvokeMember(property,System.Reflection.BindingFlags.GetProperty,null,acc,args));}
     catch(Exception) {Console.WriteLine("MSAA {0}=unavailable",property);}
    }
   }
  } finally {if(acc!=null && Marshal.IsComObject(acc))Marshal.ReleaseComObject(acc);}
 }
 public static void ProbeChildren(IntPtr hwnd) {
  int count=0;
  EnumChildWindows(hwnd,(child,arg)=> {
   if(!IsWindowVisible(child))return true;
   if(++count>8)return false;
   Console.WriteLine("PROBE CHILD hwnd={0:X} class={1}",child.ToInt64(),Class(child));
   Probe(child);return true;
  },IntPtr.Zero);
 }
 public static void Windows(uint pid) {
  EnumWindows((w,a)=> {uint found;uint thread=GetWindowThreadProcessId(w,out found);if(found!=pid || !IsWindowVisible(w))return true;
   var info=new Info();info.size=(uint)Marshal.SizeOf(info);GetGUIThreadInfo(thread,ref info);
   Console.WriteLine("WINDOW hwnd={0:X} class={1} focus={2:X}/{3} caret={4:X}/{5}",w.ToInt64(),Class(w),info.focus.ToInt64(),Class(info.focus),info.caret.ToInt64(),Class(info.caret));
   EnumChildWindows(w,(c,x)=> {if(IsWindowVisible(c))Console.WriteLine(" CHILD hwnd={0:X} class={1}",c.ToInt64(),Class(c));return true;},IntPtr.Zero);return true;
  },IntPtr.Zero);
 }
}
'@
}

$targetProcesses = @(Get-Process -Name $ProcessName -ErrorAction Stop)
foreach ($targetProcess in $targetProcesses) { [TypeNextFocusMetadata]::Windows([uint32]$targetProcess.Id) }
$deadline = [DateTime]::UtcNow.AddSeconds($WatchSeconds)
$probed = $false
$printedShell = $false
do {
    $focused = [System.Windows.Automation.AutomationElement]::FocusedElement
    if ($null -ne $focused -and $targetProcesses.Id -contains $focused.Current.ProcessId) {
        $current = $focused.Current
        if ($ProbeProvider -and -not $probed -and $current.NativeWindowHandle -ne 0) {
            [void][System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$current.NativeWindowHandle)
            [TypeNextFocusMetadata]::Probe([IntPtr]$current.NativeWindowHandle)
            [TypeNextFocusMetadata]::ProbeChildren([IntPtr]$current.NativeWindowHandle)
            $probed = $true
            Start-Sleep -Milliseconds 300
            continue
        }
        if ($current.ControlType.Id -eq 50032 -and $printedShell) {
            if ([DateTime]::UtcNow -ge $deadline) { break }
            Start-Sleep -Milliseconds 200
            continue
        }
        [pscustomobject]@{
            ProcessId = $current.ProcessId
            ControlType = $current.ControlType.ProgrammaticName
            ControlTypeId = $current.ControlType.Id
            ClassName = $current.ClassName
            FrameworkId = $current.FrameworkId
            NativeWindowHandle = $current.NativeWindowHandle
            HasKeyboardFocus = $current.HasKeyboardFocus
            IsKeyboardFocusable = $current.IsKeyboardFocusable
            IsEnabled = $current.IsEnabled
            IsPassword = $current.IsPassword
            Patterns = ($focused.GetSupportedPatterns() | ForEach-Object { $_.ProgrammaticName }) -join ','
        } | Format-List
        $textPattern = $null
        if ($focused.TryGetCurrentPattern([System.Windows.Automation.TextPattern]::Pattern, [ref]$textPattern)) {
            $selection = $textPattern.GetSelection()
            Write-Output ('SelectionRanges={0}' -f $selection.Length)
            if ($selection.Length -eq 1) {
                $comparison = $selection[0].CompareEndpoints([System.Windows.Automation.TextPatternRangeEndpoint]::Start, $selection[0], [System.Windows.Automation.TextPatternRangeEndpoint]::End)
                Write-Output ('SelectionCollapsed={0}; ReadOnlyAttribute={1}' -f ($comparison -eq 0), $selection[0].GetAttributeValue([System.Windows.Automation.TextPattern]::IsReadOnlyAttribute))
            }
        }
        $valuePattern = $null
        if ($focused.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern, [ref]$valuePattern)) {
            Write-Output ('ValuePatternReadOnly={0}' -f $valuePattern.Current.IsReadOnly)
        }
        if ($current.ControlType.Id -ne 50032) { exit 0 }
        $printedShell = $true
    }
    if ([DateTime]::UtcNow -ge $deadline) { break }
    Start-Sleep -Milliseconds 200
} while ($true)
if ($printedShell) {
    Write-Output 'The target app exposed only its outer window; no focused text editor became accessible.'
} else {
    Write-Output 'The target app did not receive accessibility focus during the metadata check.'
}
