function Get-ReleaseVersion {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Tag)

    $identifier = '(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
    $pattern = '\Av(?<major>0|[1-9][0-9]*)\.(?<minor>0|[1-9][0-9]*)\.(?<patch>0|[1-9][0-9]*)' +
        '(?:-(?<prerelease>' + $identifier + '(?:\.' + $identifier + ')*))?' +
        '(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?\z'
    $match = [regex]::Match($Tag, $pattern)
    if (-not $match.Success) {
        throw "Invalid release tag '$Tag'. Use vMAJOR.MINOR.PATCH with optional SemVer prerelease/build metadata."
    }
    $numbers = foreach ($name in @('major', 'minor', 'patch')) {
        [UInt16]$number = 0
        if (-not [UInt16]::TryParse($match.Groups[$name].Value, [ref]$number)) {
            throw "Release tag '$Tag' exceeds Windows version limits: each numeric component must be 0..65535."
        }
        $number
    }
    return [pscustomobject]@{
        Tag = $Tag
        WindowsVersion = ($numbers -join '.') + '.0'
        IsPrerelease = $match.Groups['prerelease'].Success
    }
}

function Assert-ReleaseTag {
    param(
        [Parameter(Mandatory)][string]$Tag,
        [Parameter(Mandatory)][string]$Repository
    )

    $null = Get-ReleaseVersion -Tag $Tag
    $tagCommit = & git -C $Repository rev-parse --verify "refs/tags/$Tag^{commit}" 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "Release tag '$Tag' does not exist in this repository. Create the tag before building a release."
    }
    $headCommit = & git -C $Repository rev-parse --verify HEAD
    if ($LASTEXITCODE -ne 0 -or $tagCommit -cne $headCommit) {
        throw "Release tag '$Tag' does not point at the checked-out commit. Check out that exact tag before building."
    }
}

function Get-ReleaseWindowsResources {
    param(
        [Parameter(Mandatory)][string]$Tag,
        [Parameter(Mandatory)][ValidateSet('f95rpc.exe', 'f95rpc-tray.exe')][string]$ExecutableName,
        [Parameter(Mandatory)][string]$IconPath
    )

    $version = Get-ReleaseVersion -Tag $Tag
    $description = if ($ExecutableName -eq 'f95rpc.exe') { 'f95rpc command-line tool' } else { 'f95rpc system tray companion' }
    $flags = if ($version.IsPrerelease) { 'Prerelease' } else { '' }
    return @{
        RT_GROUP_ICON = @{ APP = @{ '0000' = $IconPath } }
        RT_VERSION = @{
            '#1' = @{
                '0000' = @{
                    fixed = @{
                        file_version = $version.WindowsVersion
                        product_version = $version.WindowsVersion
                        flags = $flags
                    }
                    info = @{
                        '0409' = @{
                            FileDescription = $description
                            FileVersion = $version.Tag
                            InternalName = [IO.Path]::GetFileNameWithoutExtension($ExecutableName)
                            OriginalFilename = $ExecutableName
                            ProductName = 'f95rpc'
                            ProductVersion = $version.Tag
                        }
                    }
                }
            }
        }
    }
}

function New-ReleaseArchive {
    param(
        [Parameter(Mandatory)][string]$Tag,
        [Parameter(Mandatory)][string]$PackageDirectory,
        [Parameter(Mandatory)][string]$OutputDirectory
    )

    $version = Get-ReleaseVersion -Tag $Tag
    $files = foreach ($name in @('f95rpc.exe', 'f95rpc-tray.exe', 'config.example.yaml', 'README.md')) {
        $path = Join-Path $PackageDirectory $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Missing release file: $name"
        }
        $path
    }
    $OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
    $null = New-Item -ItemType Directory -Path $OutputDirectory -Force
    # GitHub may normalize '+' in asset filenames. Keep the full tag inside the EXEs.
    $archiveTag = $version.Tag.Replace('+', '_')
    $archivePath = Join-Path $OutputDirectory "f95rpc-$archiveTag-windows-amd64.zip"
    Compress-Archive -LiteralPath $files -DestinationPath $archivePath -CompressionLevel Optimal -Force
    $hash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $checksumPath = Join-Path $OutputDirectory 'SHA256SUMS.txt'
    [IO.File]::WriteAllText($checksumPath, "$hash  $(Split-Path $archivePath -Leaf)`n", [Text.UTF8Encoding]::new($false))
    return [pscustomobject]@{
        Tag = $version.Tag
        IsPrerelease = $version.IsPrerelease
        ArchivePath = $archivePath
        ChecksumPath = $checksumPath
    }
}
