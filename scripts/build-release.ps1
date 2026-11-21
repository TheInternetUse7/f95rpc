#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Tag,
    [string]$OutputDirectory = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) {
    throw 'Release builds must run on Windows so the EXE version fields can be verified.'
}
. (Join-Path $PSScriptRoot 'release-functions.ps1')
$repository = Split-Path $PSScriptRoot -Parent
$version = Get-ReleaseVersion -Tag $Tag
Assert-ReleaseTag -Tag $Tag -Repository $repository
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $repository 'dist'
}

$targets = @(
    @{ Name = 'f95rpc.exe'; Package = 'cmd/f95rpc'; Gui = $false },
    @{ Name = 'f95rpc-tray.exe'; Package = 'cmd/f95rpc-tray'; Gui = $true }
)
foreach ($target in $targets) {
    $directory = Join-Path $repository $target.Package
    if (@(Get-ChildItem -LiteralPath $directory -File -Filter '*_windows_amd64.syso').Count -ne 0) {
        throw "Existing Windows resource objects in $($target.Package). Build in a clean checkout; existing files will not be overwritten."
    }
}

$buildID = [Guid]::NewGuid().ToString('N')
$staging = Join-Path ([IO.Path]::GetTempPath()) ('f95rpc-release-' + $buildID)
$packageDirectory = Join-Path $staging 'package'
$null = New-Item -ItemType Directory -Path $packageDirectory -Force
$resourceObjects = @()
$oldGoos, $oldGoarch, $oldCgo = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
Push-Location $repository
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    Copy-Item -LiteralPath (Join-Path $repository 'assets/icon.ico') -Destination (Join-Path $staging 'icon.ico')

    foreach ($target in $targets) {
        $resources = Get-ReleaseWindowsResources -Tag $Tag -ExecutableName $target.Name -IconPath 'icon.ico'
        $jsonPath = Join-Path $staging ($target.Name + '.json')
        [IO.File]::WriteAllText($jsonPath, ($resources | ConvertTo-Json -Depth 10), [Text.UTF8Encoding]::new($false))
        $prefix = Join-Path (Join-Path $repository $target.Package) ('release_resources_' + $buildID)
        $resourceObjects += $prefix + '_windows_amd64.syso'
        # Pin the build-only tool; it does not become an application dependency.
        & go run github.com/tc-hib/go-winres@v0.3.3 make --in $jsonPath --out $prefix --arch amd64 | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "Resource generation failed for $($target.Name)" }

        $executable = Join-Path $packageDirectory $target.Name
        $ldflags = "-s -w -X f95rpc/internal/version.Version=$Tag"
        if ($target.Gui) { $ldflags += ' -H windowsgui' }
        & go build -trimpath -ldflags $ldflags -o $executable ('./' + $target.Package) | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "Build failed for $($target.Name)" }

        $info = [Diagnostics.FileVersionInfo]::GetVersionInfo($executable)
        $numericVersion = @($info.FileMajorPart, $info.FileMinorPart, $info.FileBuildPart, $info.FilePrivatePart) -join '.'
        if ($info.FileVersion -cne $Tag -or $info.ProductVersion -cne $Tag -or
            $numericVersion -cne $version.WindowsVersion -or $info.OriginalFilename -cne $target.Name -or
            $info.IsPreRelease -ne $version.IsPrerelease) {
            throw "Windows version metadata did not match '$Tag' in $($target.Name)"
        }

        $reader = [IO.BinaryReader]::new([IO.File]::OpenRead($executable))
        try {
            $reader.BaseStream.Position = 0x3C
            $peOffset = $reader.ReadUInt32()
            $reader.BaseStream.Position = $peOffset + 4
            if ($reader.ReadUInt16() -ne 0x8664) { throw "$($target.Name) is not Windows x64" }
            $reader.BaseStream.Position = $peOffset + 24 + 68
            $expectedSubsystem = if ($target.Gui) { 2 } else { 3 }
            if ($reader.ReadUInt16() -ne $expectedSubsystem) { throw "$($target.Name) has the wrong console/GUI subsystem" }
        }
        finally {
            $reader.Dispose()
        }
        Write-Host "Verified $($target.Name): $Tag ($numericVersion)"
    }

    $reportedVersion = & (Join-Path $packageDirectory 'f95rpc.exe') version
    if ($LASTEXITCODE -ne 0 -or $reportedVersion -cne "f95rpc $Tag") {
        throw 'The CLI build version did not match the release tag.'
    }
    foreach ($name in @('config.example.yaml', 'README.md')) {
        Copy-Item -LiteralPath (Join-Path $repository $name) -Destination $packageDirectory
    }
    $release = New-ReleaseArchive -Tag $Tag -PackageDirectory $packageDirectory -OutputDirectory $OutputDirectory
    Write-Host "Created $($release.ArchivePath)"
    return $release
}
finally {
    foreach ($path in $resourceObjects) {
        Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
    }
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldGoos, $oldGoarch, $oldCgo
    Pop-Location
    Remove-Item -LiteralPath $staging -Recurse -Force
}
