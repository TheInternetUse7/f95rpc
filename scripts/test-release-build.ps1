#requires -Version 7.0
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$builder = Join-Path $PSScriptRoot 'build-release.ps1'
if (-not (Test-Path -LiteralPath $builder -PathType Leaf)) {
    throw 'Release builder is missing: cannot embed tag versions into both EXEs or verify a release ZIP.'
}
. (Join-Path $PSScriptRoot 'release-functions.ps1')

function Assert-BuildTest {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

$sourceRoot = Split-Path $PSScriptRoot -Parent
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('f95rpc-tagged-build-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $fixture
try {
    foreach ($directory in @('cmd', 'internal', 'assets', 'scripts')) {
        Copy-Item -LiteralPath (Join-Path $sourceRoot $directory) -Destination $fixture -Recurse
    }
    foreach ($file in @('go.mod', 'go.sum', 'README.md', 'config.example.yaml', '.gitignore', '.gitattributes')) {
        Copy-Item -LiteralPath (Join-Path $sourceRoot $file) -Destination $fixture
    }
    & git -C $fixture init --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Could not initialize the isolated build repository' }
    & git -C $fixture add --all
    if ($LASTEXITCODE -ne 0) { throw 'Could not stage the build fixture' }
    & git -C $fixture -c user.name=ReleaseTests -c user.email=release-tests@example.invalid commit --quiet -m 'release build fixture'
    if ($LASTEXITCODE -ne 0) { throw 'Could not commit the build fixture' }

    foreach ($tag in @('v1.2.3-rc.1', 'v1.2.3')) {
        & git -C $fixture -c user.name=ReleaseTests -c user.email=release-tests@example.invalid tag -a $tag -m $tag
        if ($LASTEXITCODE -ne 0) { throw 'Could not tag the build fixture' }
        $release = & (Join-Path $fixture 'scripts/build-release.ps1') -Tag $tag
        Assert-BuildTest (Test-Path -LiteralPath $release.ArchivePath -PathType Leaf) 'Build did not produce its ZIP'
        Assert-BuildTest ($release.IsPrerelease -eq $tag.Contains('-')) 'Build returned the wrong prerelease flag'

        $extracted = Join-Path $fixture ('dist/extract-' + $tag)
        Expand-Archive -LiteralPath $release.ArchivePath -DestinationPath $extracted
        foreach ($name in @('f95rpc.exe', 'f95rpc-tray.exe')) {
            $path = Join-Path $extracted $name
            $info = [Diagnostics.FileVersionInfo]::GetVersionInfo($path)
            Assert-BuildTest ($info.FileVersion -ceq $tag) "$name FileVersion did not preserve the tag"
            Assert-BuildTest ($info.ProductVersion -ceq $tag) "$name ProductVersion did not preserve the tag"
            Assert-BuildTest ($info.OriginalFilename -ceq $name) "$name has the wrong original filename"
            Assert-BuildTest ($info.IsPreRelease -eq $tag.Contains('-')) "$name has the wrong Windows prerelease flag"
            Assert-BuildTest ($info.FileMajorPart -eq 1 -and $info.FileMinorPart -eq 2 -and $info.FileBuildPart -eq 3 -and $info.FilePrivatePart -eq 0) "$name has the wrong numeric Windows version"

            $reader = [IO.BinaryReader]::new([IO.File]::OpenRead($path))
            try {
                $reader.BaseStream.Position = 0x3C
                $peOffset = $reader.ReadUInt32()
                $reader.BaseStream.Position = $peOffset + 4
                Assert-BuildTest ($reader.ReadUInt16() -eq 0x8664) "$name is not Windows x64"
                $reader.BaseStream.Position = $peOffset + 24 + 68
                $subsystem = $reader.ReadUInt16()
                $expectedSubsystem = if ($name -eq 'f95rpc.exe') { 3 } else { 2 }
                Assert-BuildTest ($subsystem -eq $expectedSubsystem) "$name uses the wrong console/GUI subsystem"
            }
            finally {
                $reader.Dispose()
            }
        }
        $reportedVersion = & (Join-Path $extracted 'f95rpc.exe') version
        Assert-BuildTest ($LASTEXITCODE -eq 0 -and $reportedVersion -ceq "f95rpc $tag") 'Release CLI reported the wrong version'
        $expectedFiles = @('f95rpc.exe', 'f95rpc-tray.exe', 'config.example.yaml', 'README.md') | Sort-Object
        $files = @(Get-ChildItem -LiteralPath $extracted -File | ForEach-Object { $_.Name } | Sort-Object)
        Assert-BuildTest (($files -join ',') -ceq ($expectedFiles -join ',')) 'Release ZIP contained unexpected files'
        Assert-BuildTest (@(Get-ChildItem -LiteralPath (Join-Path $fixture 'cmd') -Recurse -Filter '*.syso').Count -eq 0) 'Build left generated resources in the source tree'
    }
    $status = & git -C $fixture status --porcelain
    Assert-BuildTest ([string]::IsNullOrWhiteSpace(($status -join "`n"))) 'Build changed tracked files or left unignored generated files'
    Write-Host 'Tagged build tests passed: both EXEs, Windows version fields, x64 console/GUI subsystems, and clean ZIPs.'
}
finally {
    Remove-Item -LiteralPath $fixture -Recurse -Force
}
