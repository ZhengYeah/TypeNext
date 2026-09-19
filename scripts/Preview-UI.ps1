param([string]$Executable = 'TypeNext.next.exe')
$ErrorActionPreference = 'Stop'
$workspace = Split-Path $PSScriptRoot -Parent
$runningMutex = $null
try { $runningMutex = [Threading.Mutex]::OpenExisting('Local\TypeNext-0.1') }
catch [Threading.WaitHandleCannotBeOpenedException] { }
if ($null -ne $runningMutex) {
    $runningMutex.Dispose()
    throw 'Quit the running TypeNext before opening the isolated UI preview.'
}
$previewRoot = Join-Path $workspace 'ui-preview.tmp'
$previewConfigDir = Join-Path $previewRoot 'TypeNext'
$outputDir = Join-Path $workspace 'docs\screenshots'
[void](New-Item -ItemType Directory -Path $previewConfigDir, $outputDir -Force)
# Isolated, synthetic settings. No model calls or user credentials are used.
$previewConfig = Get-Content -LiteralPath (Join-Path $workspace 'config.example.json') -Raw -Encoding UTF8 | ConvertFrom-Json
$previewConfig.provider = 'openai-compatible'
$previewConfig.endpoint = 'https://api.example.com/v1'
$previewConfig.model = 'example-model'
$previewConfig.automatic_suggestions = $true
$previewConfig.accept_with_tab = $true
$previewConfig.allow_remote_api = $true
$previewConfig.allow_automatic_remote_requests = $false
$previewConfig | Add-Member -NotePropertyName approved_remote_endpoint -NotePropertyValue 'https://api.example.com/v1/chat/completions' -Force
$previewConfig.api_key_environment_variable = ''
$previewConfig.suggest_hotkey = 'None'
$previewConfig.accept_hotkey = 'None'
$previewConfig.pause_hotkey = 'None'
[IO.File]::WriteAllText((Join-Path $previewConfigDir 'config.json'), ($previewConfig | ConvertTo-Json -Depth 6), (New-Object Text.UTF8Encoding($false)))
Add-Type -AssemblyName System.Drawing
Add-Type -ReferencedAssemblies System.Drawing -TypeDefinition @'
using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.Runtime.InteropServices;
using System.Text;
public static class TypeNextPreview {
 public delegate bool Callback(IntPtr hwnd, IntPtr arg);
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
 [DllImport("user32.dll")] static extern bool EnumWindows(Callback cb,IntPtr arg);
 [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr hwnd,Callback cb,IntPtr arg);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetWindowText(IntPtr hwnd,StringBuilder text,int max);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd,StringBuilder text,int max);
 [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hwnd,out Rect rect);
 [DllImport("user32.dll")] static extern bool GetClientRect(IntPtr hwnd,out Rect rect);
 [DllImport("user32.dll")] static extern bool PrintWindow(IntPtr hwnd,IntPtr dc,uint flags);
 [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(IntPtr hwnd,int attribute,out Rect rect,int size);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd,uint message,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] static extern IntPtr SendMessage(IntPtr hwnd,uint message,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] static extern IntPtr GetDC(IntPtr hwnd);
 [DllImport("user32.dll")] static extern int ReleaseDC(IntPtr hwnd,IntPtr dc);
 [DllImport("gdi32.dll")] static extern IntPtr SelectObject(IntPtr dc,IntPtr obj);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int DrawText(IntPtr dc,string text,int count,ref Rect rect,uint flags);
 public static IntPtr Find(int pid,string ending) {
  IntPtr found=IntPtr.Zero;
  EnumWindows((hwnd,arg)=> { uint owner;GetWindowThreadProcessId(hwnd,out owner);if(owner!=pid)return true;
   var title=new StringBuilder(256);GetWindowText(hwnd,title,title.Capacity);
   if(title.ToString().EndsWith(ending,StringComparison.OrdinalIgnoreCase))found=hwnd;
   return true;
  },IntPtr.Zero);return found;
 }
 public static void Capture(IntPtr hwnd,string path) {
  if(hwnd==IntPtr.Zero)throw new InvalidOperationException("Preview window not found.");
  Rect bounds;GetWindowRect(hwnd,out bounds);
  using(var bitmap=new Bitmap(bounds.Right-bounds.Left,bounds.Bottom-bounds.Top))
  using(var graphics=Graphics.FromImage(bitmap)) {
   IntPtr dc=graphics.GetHdc();try { if(!PrintWindow(hwnd,dc,2))throw new InvalidOperationException("Window capture failed."); }
   finally { graphics.ReleaseHdc(dc); }
   Rect frame;
   if(DwmGetWindowAttribute(hwnd,9,out frame,16)==0 && frame.Left>=bounds.Left && frame.Top>=bounds.Top && frame.Right<=bounds.Right && frame.Bottom<=bounds.Bottom) {
    using(var visible=bitmap.Clone(new Rectangle(frame.Left-bounds.Left,frame.Top-bounds.Top,frame.Right-frame.Left,frame.Bottom-frame.Top),bitmap.PixelFormat))visible.Save(path,ImageFormat.Png);
   } else bitmap.Save(path,ImageFormat.Png);
  }
  int clipped=0;
  EnumChildWindows(hwnd,(child,arg)=> {
   var cls=new StringBuilder(256);GetClassName(child,cls,cls.Capacity);if(cls.ToString()!="STATIC")return true;
   var text=new StringBuilder(2000);GetWindowText(child,text,text.Capacity);if(text.Length==0)return true;
   Rect box;GetClientRect(child,out box);Rect measured=box;
   IntPtr dc=GetDC(child),font=SendMessage(child,0x31,IntPtr.Zero,IntPtr.Zero),old=SelectObject(dc,font);
   try {DrawText(dc,text.ToString(),text.Length,ref measured,0x400|0x10|0x800);}
   finally {SelectObject(dc,old);ReleaseDC(child,dc);}
   if(measured.Bottom-measured.Top>box.Bottom-box.Top) {clipped++;Console.WriteLine("Clipped static: {0}",text);}
   return true;
  },IntPtr.Zero);
  Console.WriteLine("Saved {0}; clipped static labels={1}",path,clipped);
 }
}
'@
$startInfo = New-Object Diagnostics.ProcessStartInfo
$startInfo.FileName = Join-Path $workspace $Executable
$startInfo.WorkingDirectory = $workspace
$startInfo.UseShellExecute = $false
$startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
$startInfo.EnvironmentVariables['APPDATA'] = $previewRoot
$preview = [Diagnostics.Process]::Start($startInfo)
$main = [IntPtr]::Zero
try {
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        Start-Sleep -Milliseconds 200
        if ($preview.HasExited) { throw ('Preview exited during startup with code {0}.' -f $preview.ExitCode) }
        $main = [TypeNextPreview]::Find($preview.Id, 'writing completion')
    } while ($main -eq [IntPtr]::Zero -and [DateTime]::UtcNow -lt $deadline)
    Start-Sleep -Milliseconds 500
    [TypeNextPreview]::Capture($main, (Join-Path $outputDir 'settings.png'))
    [void][TypeNextPreview]::PostMessage($main, 0x111, [IntPtr]111, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 600
    $api = [TypeNextPreview]::Find($preview.Id, 'API settings')
    [TypeNextPreview]::Capture($api, (Join-Path $outputDir 'api-settings.png'))
    [void][TypeNextPreview]::PostMessage($api, 0x111, [IntPtr]114, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 200
    [void][TypeNextPreview]::PostMessage($main, 0x111, [IntPtr]107, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 600
    $shortcuts = [TypeNextPreview]::Find($preview.Id, 'Shortcuts')
    [TypeNextPreview]::Capture($shortcuts, (Join-Path $outputDir 'shortcuts.png'))
    [void][TypeNextPreview]::PostMessage($shortcuts, 0x111, [IntPtr]110, [IntPtr]::Zero)
} finally {
    if ($main -ne [IntPtr]::Zero) { [void][TypeNextPreview]::PostMessage($main, 0x111, [IntPtr]104, [IntPtr]::Zero) }
    if (-not $preview.WaitForExit(5000)) { throw 'Preview did not exit; close the isolated preview manually.' }
    $preview.Dispose()
}
