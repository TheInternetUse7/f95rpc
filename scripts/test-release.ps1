$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$library = Join-Path $PSScriptRoot 'release-functions.ps1'
if (-not (Test-Path -LiteralPath $library -PathType Leaf)) {
    throw 'Release helpers are missing: tag-derived metadata and safe packaging have not been implemented.'
}
. $library

function Assert-ReleaseTest {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) {
        throw $Message
    }
}

function Assert-ReleaseError {
    param([scriptblock]$Action, [string]$Message)
    $failed = $false
    try {
        & $Action | Out-Null
    }
    catch {
        $failed = $true
    }
    Assert-ReleaseTest $failed $Message
}

$validTags = @(
    @{ Tag = 'v0.0.0'; Windows = '0.0.0.0'; Prerelease = $false },
    @{ Tag = 'v1.2.3'; Windows = '1.2.3.0'; Prerelease = $false },
    @{ Tag = 'v65535.65535.65535'; Windows = '65535.65535.65535.0'; Prerelease = $false },
    @{ Tag = 'v1.2.3-rc.1'; Windows = '1.2.3.0'; Prerelease = $true },
    @{ Tag = 'v1.2.3-0'; Windows = '1.2.3.0'; Prerelease = $true },
    @{ Tag = 'v1.2.3-alpha.2+build.005'; Windows = '1.2.3.0'; Prerelease = $true },
    @{ Tag = 'v1.2.3+build.005'; Windows = '1.2.3.0'; Prerelease = $false }
)
foreach ($case in $validTags) {
    $version = Get-ReleaseVersion -Tag $case.Tag
    Assert-ReleaseTest ($version.Tag -ceq $case.Tag) "Changed the original tag $($case.Tag)"
    Assert-ReleaseTest ($version.WindowsVersion -ceq $case.Windows) "Wrong Windows version for $($case.Tag)"
    Assert-ReleaseTest ($version.IsPrerelease -eq $case.Prerelease) "Wrong prerelease flag for $($case.Tag)"
}

$invalidTags = @(
    '', '1.2.3', 'V1.2.3', 'v1.2', 'v01.2.3', 'v1.02.3', 'v1.2.03',
    'v1.2.3-01', 'v1.2.3-rc.01', 'v1.2.3-rc..1', 'v1.2.3-', 'v1.2.3+',
    'v65536.0.0', 'v1.65536.0', 'v1.0.65536', 'v999999999999999999999999.0.0',
    'v1.2.3;Write-Host injected', 'v1.2.3/../config.yaml', ' v1.2.3', "v1.2.3`n"
)
foreach ($tag in $invalidTags) {
    Assert-ReleaseError { Get-ReleaseVersion -Tag $tag } "Accepted an invalid release tag: $tag"
}

foreach ($executable in @('f95rpc.exe', 'f95rpc-tray.exe')) {
    $resources = Get-ReleaseWindowsResources -Tag 'v1.2.3-rc.1' -ExecutableName $executable -IconPath 'C:\assets\icon.ico'
    $versionInfo = $resources.RT_VERSION['#1']['0000']
    $strings = $versionInfo.info['0409']
    Assert-ReleaseTest ($versionInfo.fixed.file_version -ceq '1.2.3.0') 'Missing numeric file version'
    Assert-ReleaseTest ($versionInfo.fixed.product_version -ceq '1.2.3.0') 'Missing numeric product version'
    Assert-ReleaseTest ($versionInfo.fixed.flags -ceq 'Prerelease') 'Missing Windows prerelease flag'
    Assert-ReleaseTest ($strings.FileVersion -ceq 'v1.2.3-rc.1') 'FileVersion lost the exact tag'
    Assert-ReleaseTest ($strings.ProductVersion -ceq 'v1.2.3-rc.1') 'ProductVersion lost the exact tag'
    Assert-ReleaseTest ($strings.OriginalFilename -ceq $executable) 'Wrong original executable filename'
    Assert-ReleaseTest ($strings.ProductName -ceq 'f95rpc') 'Wrong product name'
    Assert-ReleaseTest ($resources.RT_GROUP_ICON.APP['0000'] -ceq 'C:\assets\icon.ico') 'Missing embedded application icon'
    Assert-ReleaseTest (-not $resources.ContainsKey('RT_MANIFEST')) 'Version metadata unexpectedly changed application privileges'
    $null = $resources | ConvertTo-Json -Depth 10 | ConvertFrom-Json
}
$stableResources = Get-ReleaseWindowsResources -Tag 'v1.2.3' -ExecutableName 'f95rpc.exe' -IconPath 'icon.ico'
Assert-ReleaseTest ($stableResources.RT_VERSION['#1']['0000'].fixed.flags -ceq '') 'Stable releases are marked prerelease'

$tempRoot = Join-Path ([IO.Path]::GetTempPath()) ('f95rpc-release-tests-' + [Guid]::NewGuid().ToString('N'))
$packageDirectory = Join-Path $tempRoot 'package'
$outputDirectory = Join-Path $tempRoot 'output'
$null = New-Item -ItemType Directory -Path $packageDirectory -Force
try {
    $expectedFiles = @('f95rpc.exe', 'f95rpc-tray.exe', 'config.example.yaml', 'README.md')
    foreach ($name in $expectedFiles) {
        [IO.File]::WriteAllText((Join-Path $packageDirectory $name), "fixture: $name")
    }
    foreach ($name in @('config.yaml', 'f95rpc.db', 'f95rpc.db-wal', 'f95rpc.db-shm', 'api-key.txt', 'old-release.zip')) {
        [IO.File]::WriteAllText((Join-Path $packageDirectory $name), 'private data: must not be packaged')
    }
    $release = New-ReleaseArchive -Tag 'v1.2.3-rc.1' -PackageDirectory $packageDirectory -OutputDirectory $outputDirectory
    Assert-ReleaseTest ((Split-Path $release.ArchivePath -Leaf) -ceq 'f95rpc-v1.2.3-rc.1-windows-amd64.zip') 'Wrong archive filename'
    $archive = [IO.Compression.ZipFile]::OpenRead($release.ArchivePath)
    try {
        $entries = @($archive.Entries | ForEach-Object { $_.FullName } | Sort-Object)
        Assert-ReleaseTest (($entries -join ',') -ceq (($expectedFiles | Sort-Object) -join ',')) "Unsafe archive contents: $entries"
    }
    finally {
        $archive.Dispose()
    }
    $hash = (Get-FileHash -LiteralPath $release.ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $checksum = [IO.File]::ReadAllText($release.ChecksumPath)
    $expectedChecksum = "$hash  $(Split-Path $release.ArchivePath -Leaf)`n"
    Assert-ReleaseTest ($checksum -ceq $expectedChecksum) 'Checksum file does not match the ZIP'
    Assert-ReleaseTest ([IO.File]::ReadAllBytes($release.ChecksumPath)[0] -ne 0xEF) 'Checksum contains a UTF-8 BOM'

    $metadataRelease = New-ReleaseArchive -Tag 'v1.2.3+build.005' -PackageDirectory $packageDirectory -OutputDirectory $outputDirectory
    Assert-ReleaseTest ((Split-Path $metadataRelease.ArchivePath -Leaf) -ceq 'f95rpc-v1.2.3_build.005-windows-amd64.zip') 'Archive names must be safe for GitHub asset downloads, including build metadata tags'

    $tagRepository = Join-Path $tempRoot 'tags'
    & git init --quiet $tagRepository
    if ($LASTEXITCODE -ne 0) { throw 'Could not initialize the isolated tag test repository' }
    & git -C $tagRepository -c user.name=ReleaseTests -c user.email=release-tests@example.invalid commit --allow-empty --quiet -m 'tag fixture'
    if ($LASTEXITCODE -ne 0) { throw 'Could not commit the tag fixture' }
    & git -C $tagRepository tag v1.2.3
    if ($LASTEXITCODE -ne 0) { throw 'Could not tag the fixture' }
    Assert-ReleaseTag -Tag 'v1.2.3' -Repository $tagRepository
    Assert-ReleaseError { Assert-ReleaseTag -Tag 'v9.9.9' -Repository $tagRepository } 'Accepted a tag that does not exist'
    & git -C $tagRepository -c user.name=ReleaseTests -c user.email=release-tests@example.invalid commit --allow-empty --quiet -m 'after tag'
    if ($LASTEXITCODE -ne 0) { throw 'Could not advance the tag fixture' }
    Assert-ReleaseError { Assert-ReleaseTag -Tag 'v1.2.3' -Repository $tagRepository } 'Built a release from a different commit than its tag'

    Remove-Item -LiteralPath (Join-Path $packageDirectory 'README.md')
    Assert-ReleaseError {
        New-ReleaseArchive -Tag 'v1.2.3-rc.1' -PackageDirectory $packageDirectory -OutputDirectory $outputDirectory
    } 'Packaged a release without its README'
    Assert-ReleaseTest ((Get-FileHash -LiteralPath $release.ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant() -ceq $hash) 'Failed packaging overwrote the previous archive'
}
finally {
    Remove-Item -LiteralPath $tempRoot -Recurse -Force
}

Write-Host 'Release tests passed: exact Git tags, Windows metadata, archive allowlist, and checksums.'
