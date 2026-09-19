# Regenerate the checked-in icon resources after changing assets/logo.png.
# Requires Windows PowerShell/System.Drawing and MinGW-w64 windres on PATH.
$ErrorActionPreference = "Stop"
$projectRoot = Split-Path $PSScriptRoot -Parent
$assetsPath = Join-Path $projectRoot "assets"
$iconPath = Join-Path $assetsPath "typenext.ico"
$resourcePath = Join-Path $projectRoot "cmd/typenext/icon_windows_amd64.syso"
$resourceCompiler = Get-Command windres -ErrorAction SilentlyContinue
if (-not $resourceCompiler) {
    throw "Install MinGW-w64 windres and add it to PATH to regenerate the icon resources. Normal builds use the checked-in resources."
}

Add-Type -AssemblyName System.Drawing
# Apply the mask after resizing so the logo pixels remain unchanged. A one-pixel
# coverage band keeps the rounded edge smooth even in the small tray sizes.
if (-not ('TypeNextIconCorners' -as [type])) {
    Add-Type -ReferencedAssemblies System.Drawing -TypeDefinition @'
using System;
using System.Drawing;
public static class TypeNextIconCorners {
    public static void Apply(Bitmap bitmap) {
        double radius = Math.Min(bitmap.Width, bitmap.Height) * 0.20;
        for (int y = 0; y < bitmap.Height; y++) {
            for (int x = 0; x < bitmap.Width; x++) {
                double dx = Math.Max(radius - (x + 0.5), x + 0.5 - (bitmap.Width - radius));
                double dy = Math.Max(radius - (y + 0.5), y + 0.5 - (bitmap.Height - radius));
                if (dx <= 0 || dy <= 0) continue;
                double coverage = Math.Max(0, Math.Min(1, radius + 0.5 - Math.Sqrt(dx * dx + dy * dy)));
                if (coverage >= 1) continue;
                Color pixel = bitmap.GetPixel(x, y);
                int alpha = (int)Math.Round(pixel.A * coverage);
                bitmap.SetPixel(x, y, Color.FromArgb(alpha, pixel.R, pixel.G, pixel.B));
            }
        }
    }
}
'@
}
$source = [System.Drawing.Image]::FromFile((Join-Path $assetsPath "logo.png"))
$sizes = @(16, 24, 32, 48, 64, 128, 256)
$frames = New-Object 'System.Collections.Generic.List[byte[]]'
try {
    foreach ($size in $sizes) {
        $bitmap = New-Object System.Drawing.Bitmap($size, $size)
        $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
        $stream = New-Object System.IO.MemoryStream
        try {
            $graphics.Clear([System.Drawing.Color]::Transparent)
            $graphics.CompositingMode = [System.Drawing.Drawing2D.CompositingMode]::SourceCopy
            $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
            $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
            $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
            # Trim the source's generous outer padding so the mark fills about
            # 89% of the icon width and stays legible at taskbar/tray sizes.
            $artworkZoom = 1.25
            $scale = [Math]::Min($size / $source.Width, $size / $source.Height) * $artworkZoom
            $width = [int][Math]::Round($source.Width * $scale)
            $height = [int][Math]::Round($source.Height * $scale)
            $destination = New-Object System.Drawing.Rectangle(([int](($size - $width) / 2)), ([int](($size - $height) / 2)), $width, $height)
            $graphics.DrawImage($source, $destination)
            $graphics.Dispose()
            $graphics = $null
            [TypeNextIconCorners]::Apply($bitmap)
            $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
            $frames.Add($stream.ToArray())
        } finally {
            $stream.Dispose()
            if ($null -ne $graphics) { $graphics.Dispose() }
            $bitmap.Dispose()
        }
    }
} finally {
    $source.Dispose()
}

$iconStream = [System.IO.File]::Create($iconPath)
$writer = New-Object System.IO.BinaryWriter($iconStream)
try {
    $writer.Write([uint16]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]$sizes.Count)
    $offset = 6 + 16 * $sizes.Count
    for ($index = 0; $index -lt $sizes.Count; $index++) {
        # ICO stores 256-pixel dimensions as zero; each frame contains PNG data.
        $dimension = [byte]($sizes[$index] % 256)
        $writer.Write($dimension)
        $writer.Write($dimension)
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]32)
        $writer.Write([uint32]$frames[$index].Length)
        $writer.Write([uint32]$offset)
        $offset += $frames[$index].Length
    }
    foreach ($frame in $frames) {
        $writer.Write([byte[]]$frame)
    }
} finally {
    $writer.Dispose()
    $iconStream.Dispose()
}

$resourceSourcePath = Join-Path $assetsPath "typenext.rc"
& $resourceCompiler.Source --input $resourceSourcePath --include-dir $assetsPath --output $resourcePath --output-format coff --target pe-x86-64
if ($LASTEXITCODE -ne 0) { throw "Icon resource compilation failed." }
Write-Host "Updated assets/typenext.ico and cmd/typenext/icon_windows_amd64.syso."
