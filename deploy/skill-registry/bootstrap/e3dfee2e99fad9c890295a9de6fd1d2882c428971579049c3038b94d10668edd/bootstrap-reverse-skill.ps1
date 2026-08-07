[CmdletBinding()]
param(
    [string]$DescriptorUrl = 'https://codexrip.vip/skills/reverse-skill/current.json',
    [string]$DescriptorFile,
    [string]$AssetRoot,
    [string]$CacheRoot,
    [string]$TaskRoot,
    [string]$TaskId,
    [string]$RouteId
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$AllowedHost = 'codexrip.vip'
$ManifestName = 'bundle-manifest.json'
$MaxDescriptorBytes = 256KB
$MaxManifestBytes = 4MB
$MaxArchiveBytes = 128MB
$MaxExtractedBytes = 256MB
$MaxFileBytes = 64MB
$MaxFiles = 2000
$TaskMaxAge = [TimeSpan]::FromDays(7)

function Throw-BootstrapError([string]$Message) {
    throw [InvalidOperationException]::new($Message)
}

function Test-Sha256([string]$Value) {
    return $Value -cmatch '^[0-9a-f]{64}$'
}

function Get-BytesSha256([byte[]]$Bytes) {
    $hash = [Security.Cryptography.SHA256]::HashData($Bytes)
    return [Convert]::ToHexString($hash).ToLowerInvariant()
}

function Get-FileSha256([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    try {
        $hash = [Security.Cryptography.SHA256]::HashData($stream)
        return [Convert]::ToHexString($hash).ToLowerInvariant()
    }
    finally {
        $stream.Dispose()
    }
}

function Test-BytesEqual([byte[]]$Left, [byte[]]$Right) {
    if ($Left.Length -ne $Right.Length) { return $false }
    for ($index = 0; $index -lt $Left.Length; $index++) {
        if ($Left[$index] -ne $Right[$index]) { return $false }
    }
    return $true
}

function Assert-PublicUrl([string]$Value) {
    $uri = $null
    if (-not [Uri]::TryCreate($Value, [UriKind]::Absolute, [ref]$uri) -or
        $uri.Scheme -cne 'https' -or $uri.Host -cne $AllowedHost -or
        -not $uri.IsDefaultPort -or $uri.UserInfo -or $uri.Fragment) {
        Throw-BootstrapError 'descriptor references an untrusted URL'
    }
    return $uri.AbsoluteUri
}

function Invoke-BoundedDownload([string]$Url, [long]$Maximum) {
    $trusted = Assert-PublicUrl $Url
    $temp = Join-Path ([IO.Path]::GetTempPath()) ('codexrip-download-' + [guid]::NewGuid().ToString('N'))
    try {
        $response = Invoke-WebRequest -Uri $trusted -MaximumRedirection 3 -TimeoutSec 30 -OutFile $temp -PassThru -UseBasicParsing
        $finalUri = $response.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
        $null = Assert-PublicUrl $finalUri
        $item = Get-Item -LiteralPath $temp
        if ($item.Length -gt $Maximum) {
            Throw-BootstrapError 'download exceeds size limit'
        }
        return [IO.File]::ReadAllBytes($temp)
    }
    finally {
        Remove-Item -LiteralPath $temp -Force -ErrorAction SilentlyContinue
    }
}

function Read-LocalContractAsset([string]$Root, [string]$Name, [long]$Maximum) {
    $resolvedRoot = [IO.Path]::GetFullPath($Root)
    $target = [IO.Path]::GetFullPath((Join-Path $resolvedRoot $Name))
    if ([IO.Path]::GetDirectoryName($target) -cne $resolvedRoot) {
        Throw-BootstrapError 'local contract asset path is invalid'
    }
    $item = Get-Item -LiteralPath $target
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $item.Length -le 0 -or $item.Length -gt $Maximum) {
        Throw-BootstrapError 'local contract asset exceeds size limit'
    }
    return [IO.File]::ReadAllBytes($target)
}

function Get-DefaultCacheRoot {
    if ($IsWindows) {
        $base = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { Join-Path $HOME 'AppData\Local' }
        return Join-Path $base 'CodexRip\skills'
    }
    if ($IsMacOS) {
        return Join-Path $HOME 'Library/Caches/CodexRip/skills'
    }
    $base = if ($env:XDG_CACHE_HOME) { $env:XDG_CACHE_HOME } else { Join-Path $HOME '.cache' }
    return Join-Path $base 'codexrip/skills'
}

function Get-PortablePathKey([string]$Value) {
    $reserved = @('CON', 'PRN', 'AUX', 'NUL', 'COM1', 'COM2', 'COM3', 'COM4', 'COM5', 'COM6', 'COM7', 'COM8', 'COM9', 'LPT1', 'LPT2', 'LPT3', 'LPT4', 'LPT5', 'LPT6', 'LPT7', 'LPT8', 'LPT9')
    $parts = @()
    foreach ($part in $Value.Split('/')) {
        $normalized = $part.TrimEnd(' ', '.').ToLowerInvariant()
        $stem = $normalized.Split('.')[0].ToUpperInvariant()
        if (-not $normalized -or $reserved -contains $stem) {
            Throw-BootstrapError "non-portable bundle path: $Value"
        }
        $parts += $normalized
    }
    return $parts -join '/'
}

function Assert-RelativePath([object]$InputValue) {
    if ($InputValue -isnot [string]) {
        Throw-BootstrapError 'invalid bundle path'
    }
    $value = [string]$InputValue
    if (-not $value -or $value.Contains([char]0) -or $value.Contains('\') -or $value.StartsWith('/') -or
        $value.Split('/') -contains '..' -or $value.Split('/') -contains '.' -or $value.Split('/') -contains '' -or
        $value.Split('/')[0].Contains(':')) {
        Throw-BootstrapError "unsafe bundle path: $value"
    }
    $null = Get-PortablePathKey $value
    return $value
}

function ConvertFrom-StrictJson([byte[]]$Bytes, [string]$Label) {
    try {
        $text = [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
        return $text | ConvertFrom-Json -Depth 100
    }
    catch {
        Throw-BootstrapError "$Label is not valid UTF-8 JSON"
    }
}

function Read-Descriptor([byte[]]$Bytes) {
    if ($Bytes.Length -gt $MaxDescriptorBytes) {
        Throw-BootstrapError 'descriptor exceeds size limit'
    }
    $descriptor = ConvertFrom-StrictJson $Bytes 'descriptor'
    foreach ($name in @('schema_version', 'bundle_id', 'revision', 'source_commit', 'manifest_sha256', 'archive_sha256', 'manifest_url', 'archive_url')) {
        if (-not ($descriptor.PSObject.Properties.Name -contains $name)) {
            Throw-BootstrapError 'descriptor is incomplete'
        }
    }
    if ([int]$descriptor.schema_version -ne 1 -or [long]$descriptor.revision -lt 1 -or
        [string]$descriptor.bundle_id -cnotmatch '^[A-Za-z0-9._-]{1,128}$' -or
        [string]$descriptor.source_commit -cnotmatch '^[0-9a-f]{40}$') {
        Throw-BootstrapError 'descriptor identity is invalid'
    }
    $descriptor.manifest_sha256 = ([string]$descriptor.manifest_sha256).ToLowerInvariant()
    $descriptor.archive_sha256 = ([string]$descriptor.archive_sha256).ToLowerInvariant()
    if (-not (Test-Sha256 $descriptor.manifest_sha256) -or -not (Test-Sha256 $descriptor.archive_sha256)) {
        Throw-BootstrapError 'descriptor digest is invalid'
    }
    $descriptor.manifest_url = Assert-PublicUrl ([string]$descriptor.manifest_url)
    $descriptor.archive_url = Assert-PublicUrl ([string]$descriptor.archive_url)
    return $descriptor
}

function Read-Manifest([byte[]]$Bytes, [object]$Descriptor) {
    if ($Bytes.Length -gt $MaxManifestBytes -or (Get-BytesSha256 $Bytes) -cne $Descriptor.manifest_sha256) {
        Throw-BootstrapError 'manifest digest or size is invalid'
    }
    $manifest = ConvertFrom-StrictJson $Bytes 'manifest'
    if ([int]$manifest.schema_version -ne 1 -or [string]$manifest.bundle_id -cne [string]$Descriptor.bundle_id) {
        Throw-BootstrapError 'manifest identity is invalid'
    }
    $files = @($manifest.files)
    if ($files.Count -lt 1 -or $files.Count -gt $MaxFiles) {
        Throw-BootstrapError 'manifest file count is invalid'
    }
    $byPath = @{}
    $portable = @{}
    [long]$total = 0
    foreach ($entry in $files) {
        $path = Assert-RelativePath $entry.path
        $key = Get-PortablePathKey $path
        $digest = ([string]$entry.sha256).ToLowerInvariant()
        $length = [long]$entry.byte_length
        $kind = if ($entry.kind) { [string]$entry.kind } else { 'text' }
        if ($path -ceq $ManifestName -or $byPath.ContainsKey($path) -or $portable.ContainsKey($key) -or
            -not (Test-Sha256 $digest) -or $length -lt 0 -or $length -gt $MaxFileBytes -or
            @('text', 'binary', 'script') -cnotcontains $kind) {
            Throw-BootstrapError "manifest metadata is invalid for $path"
        }
        $entry.sha256 = $digest
        $entry.kind = $kind
        $byPath[$path] = $entry
        $portable[$key] = $true
        $total += $length
        if ($total -gt $MaxExtractedBytes) {
            Throw-BootstrapError 'manifest extracted size exceeds limit'
        }
    }
    foreach ($pathValue in @($manifest.core_files)) {
        $path = Assert-RelativePath $pathValue
        if (-not $byPath.ContainsKey($path)) {
            Throw-BootstrapError 'manifest core files are invalid'
        }
    }
    $routeIds = @{}
    foreach ($route in @($manifest.domains)) {
        $id = [string]$route.id
        if (-not $id -or $routeIds.ContainsKey($id)) {
            Throw-BootstrapError 'manifest route is invalid'
        }
        $routeIds[$id] = $true
        foreach ($pathValue in @($route.entry) + @($route.references)) {
            $path = Assert-RelativePath $pathValue
            if (-not $byPath.ContainsKey($path)) {
                Throw-BootstrapError "manifest route references an unknown file: $path"
            }
        }
    }
    return @{ Manifest = $manifest; ByPath = $byPath }
}

function Get-NativePath([string]$Root, [string]$RelativePath) {
    $path = $Root
    foreach ($part in $RelativePath.Split('/')) {
        $path = Join-Path $path $part
    }
    return $path
}

function Assert-FileBytes([byte[]]$Bytes, [object]$Entry, [string]$Name) {
    if ($Bytes.Length -ne [long]$Entry.byte_length -or (Get-BytesSha256 $Bytes) -cne [string]$Entry.sha256) {
        Throw-BootstrapError "file verification failed: $Name"
    }
}

function Install-Bundle([byte[]]$ArchiveBytes, [byte[]]$ManifestBytes, [object]$Descriptor, [hashtable]$ManifestState, [string]$Destination) {
    Add-Type -AssemblyName System.IO.Compression
    $parent = Split-Path -Parent $Destination
    $null = New-Item -ItemType Directory -Path $parent -Force
    $staging = Join-Path $parent ('.install-' + [guid]::NewGuid().ToString('N'))
    $null = New-Item -ItemType Directory -Path $staging
    try {
        $memory = [IO.MemoryStream]::new($ArchiveBytes, $false)
        $archive = [IO.Compression.ZipArchive]::new($memory, [IO.Compression.ZipArchiveMode]::Read, $false)
        try {
            $expected = @{$ManifestName = $true}
            foreach ($name in $ManifestState.ByPath.Keys) { $expected[$name] = $true }
            $actual = @{}
            $portable = @{}
            foreach ($entry in $archive.Entries) {
                $name = Assert-RelativePath $entry.FullName
                $key = Get-PortablePathKey $name
                $unixType = (($entry.ExternalAttributes -shr 16) -band 0xF000)
                if (-not $entry.Name -or $unixType -eq 0xA000 -or ($unixType -ne 0 -and $unixType -ne 0x8000) -or
                    $actual.ContainsKey($name) -or $portable.ContainsKey($key) -or -not $expected.ContainsKey($name)) {
                    Throw-BootstrapError 'archive contains an unsafe or conflicting entry'
                }
                $expectedLength = if ($name -ceq $ManifestName) { $ManifestBytes.Length } else { [long]$ManifestState.ByPath[$name].byte_length }
                if ($entry.Length -ne $expectedLength -or $entry.Length -gt $MaxFileBytes) {
                    Throw-BootstrapError "archive metadata mismatch for $name"
                }
                $actual[$name] = $true
                $portable[$key] = $true
            }
            if ($actual.Count -ne $expected.Count) {
                Throw-BootstrapError 'archive entry set does not match manifest'
            }
            foreach ($entry in $archive.Entries) {
                $stream = $entry.Open()
                $buffer = [IO.MemoryStream]::new()
                try { $stream.CopyTo($buffer); $bytes = $buffer.ToArray() }
                finally { $buffer.Dispose(); $stream.Dispose() }
                if ($entry.FullName -ceq $ManifestName) {
                    if (-not (Test-BytesEqual $bytes $ManifestBytes)) {
                        Throw-BootstrapError 'archive manifest bytes mismatch'
                    }
                }
                else {
                    Assert-FileBytes $bytes $ManifestState.ByPath[$entry.FullName] $entry.FullName
                }
                $target = Get-NativePath $staging $entry.FullName
                $null = New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force
                [IO.File]::WriteAllBytes($target, $bytes)
            }
            [IO.File]::WriteAllBytes((Join-Path $staging '.bundle.zip'), $ArchiveBytes)
        }
        finally {
            $archive.Dispose()
            $memory.Dispose()
        }
        try { Move-Item -LiteralPath $staging -Destination $Destination -ErrorAction Stop }
        catch {
            if (-not (Test-Path -LiteralPath $Destination -PathType Container)) { throw }
        }
    }
    finally {
        Remove-Item -LiteralPath $staging -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Assert-CachedBundle([string]$Root, [byte[]]$ManifestBytes, [object]$Descriptor, [hashtable]$ManifestState) {
    $rootItem = Get-Item -LiteralPath $Root
    if (-not $rootItem.PSIsContainer -or ($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        Throw-BootstrapError 'cached bundle root is invalid'
    }
    $manifestPath = Join-Path $Root $ManifestName
    if (-not (Test-BytesEqual ([IO.File]::ReadAllBytes($manifestPath)) $ManifestBytes)) {
        Throw-BootstrapError 'cached manifest mismatch'
    }
    $archivePath = Join-Path $Root '.bundle.zip'
    if (-not (Test-Path -LiteralPath $archivePath -PathType Leaf) -or (Get-FileSha256 $archivePath) -cne $Descriptor.archive_sha256) {
        Throw-BootstrapError 'cached archive mismatch'
    }
    foreach ($name in $ManifestState.ByPath.Keys) {
        $path = Get-NativePath $Root $name
        $item = Get-Item -LiteralPath $path
        if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
            $item.Length -ne [long]$ManifestState.ByPath[$name].byte_length -or
            (Get-FileSha256 $path) -cne [string]$ManifestState.ByPath[$name].sha256) {
            Throw-BootstrapError "cached file verification failed: $name"
        }
    }
}

function Remove-ExpiredTasks([string]$Root) {
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) { return }
    $cutoff = [DateTime]::UtcNow.Subtract($TaskMaxAge)
    foreach ($item in Get-ChildItem -LiteralPath $Root -Directory -Force) {
        if (-not ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -and $item.LastWriteTimeUtc -lt $cutoff) {
            Remove-Item -LiteralPath $item.FullName -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

function New-TaskMaterialization([string]$BundleRoot, [string]$TasksRoot, [string]$RequestedTaskId, [string]$RequestedRouteId, [hashtable]$ManifestState) {
    Remove-ExpiredTasks $TasksRoot
    $null = New-Item -ItemType Directory -Path $TasksRoot -Force
    $id = if ($RequestedTaskId) { $RequestedTaskId } else { [DateTimeOffset]::UtcNow.ToUnixTimeSeconds().ToString() + '-' + [guid]::NewGuid().ToString('N').Substring(0, 12) }
    if ($id -cnotmatch '^[A-Za-z0-9._-]{1,128}$') { Throw-BootstrapError 'task id is invalid' }
    $task = Join-Path $TasksRoot $id
    $null = New-Item -ItemType Directory -Path $task -ErrorAction Stop
    $selected = [Collections.Generic.List[string]]::new()
    foreach ($name in @($ManifestState.Manifest.core_files)) { if (-not $selected.Contains([string]$name)) { $selected.Add([string]$name) } }
    if ($RequestedRouteId) {
        $route = @($ManifestState.Manifest.domains) | Where-Object { [string]$_.id -ceq $RequestedRouteId } | Select-Object -First 1
        if (-not $route) { Throw-BootstrapError 'requested route does not exist' }
        foreach ($name in @($route.entry) + @($route.references)) { if (-not $selected.Contains([string]$name)) { $selected.Add([string]$name) } }
    }
    $copied = [Collections.Generic.List[string]]::new()
    $scripts = [Collections.Generic.List[string]]::new()
    foreach ($name in $selected) {
        $entry = $ManifestState.ByPath[$name]
        if ([string]$entry.kind -ceq 'script' -and -not $RequestedRouteId) {
            Throw-BootstrapError 'scripts require an explicit route'
        }
        $source = Get-NativePath $BundleRoot $name
        $bytes = [IO.File]::ReadAllBytes($source)
        Assert-FileBytes $bytes $entry $name
        $target = Get-NativePath (Join-Path $task 'bundle') $name
        $null = New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force
        [IO.File]::WriteAllBytes($target, $bytes)
        $copied.Add([IO.Path]::GetFullPath($target))
        if ([string]$entry.kind -ceq 'script') { $scripts.Add([IO.Path]::GetFullPath($target)) }
    }
    return @{ Task = [IO.Path]::GetFullPath($task); Copied = @($copied); Scripts = @($scripts) }
}

try {
    if ([bool]$DescriptorFile -ne [bool]$AssetRoot) {
        Throw-BootstrapError '-DescriptorFile and -AssetRoot must be used together'
    }
    $descriptorBytes = if ($DescriptorFile) {
        $item = Get-Item -LiteralPath $DescriptorFile
        if ($item.Length -gt $MaxDescriptorBytes) { Throw-BootstrapError 'descriptor exceeds size limit' }
        [IO.File]::ReadAllBytes($item.FullName)
    }
    else { Invoke-BoundedDownload $DescriptorUrl $MaxDescriptorBytes }
    $descriptor = Read-Descriptor $descriptorBytes
    $manifestBytes = if ($AssetRoot) {
        Read-LocalContractAsset $AssetRoot $ManifestName $MaxManifestBytes
    }
    else { Invoke-BoundedDownload $descriptor.manifest_url $MaxManifestBytes }
    $manifestState = Read-Manifest $manifestBytes $descriptor
    $resolvedCacheRoot = [IO.Path]::GetFullPath($(if ($CacheRoot) { $CacheRoot } else { Get-DefaultCacheRoot }))
    $bundleRoot = Join-Path (Join-Path (Join-Path $resolvedCacheRoot 'bundles') $descriptor.bundle_id) $descriptor.manifest_sha256
    $cacheReused = Test-Path -LiteralPath $bundleRoot -PathType Container
    if (-not $cacheReused) {
        $archiveName = [IO.Path]::GetFileName(([Uri]$descriptor.archive_url).AbsolutePath)
        $archiveBytes = if ($AssetRoot) {
            Read-LocalContractAsset $AssetRoot $archiveName $MaxArchiveBytes
        }
        else { Invoke-BoundedDownload $descriptor.archive_url $MaxArchiveBytes }
        if ((Get-BytesSha256 $archiveBytes) -cne $descriptor.archive_sha256) { Throw-BootstrapError 'archive digest is invalid' }
        Install-Bundle $archiveBytes $manifestBytes $descriptor $manifestState $bundleRoot
    }
    Assert-CachedBundle $bundleRoot $manifestBytes $descriptor $manifestState
    $resolvedTaskRoot = [IO.Path]::GetFullPath($(if ($TaskRoot) { $TaskRoot } else { Join-Path $resolvedCacheRoot 'tasks' }))
    $materialized = New-TaskMaterialization $bundleRoot $resolvedTaskRoot $TaskId $RouteId $manifestState
    [ordered]@{
        status = 'ready'
        platform = $(if ($IsWindows) { 'windows' } elseif ($IsMacOS) { 'macos' } else { 'linux' })
        bundle_id = $descriptor.bundle_id
        bundle_revision = [long]$descriptor.revision
        manifest_sha256 = $descriptor.manifest_sha256
        source_commit = $descriptor.source_commit
        cache_reused = [bool]$cacheReused
        cache_path = [IO.Path]::GetFullPath($bundleRoot)
        manifest_path = [IO.Path]::GetFullPath((Join-Path $bundleRoot $ManifestName))
        task_path = $materialized.Task
        materialized_files = $materialized.Copied
        materialized_scripts = $materialized.Scripts
        scripts_executed = $false
    } | ConvertTo-Json -Compress -Depth 8
    exit 0
}
catch {
    $errorJson = [ordered]@{ status = 'skill_unavailable'; error = $_.Exception.Message } | ConvertTo-Json -Compress
    [Console]::Error.WriteLine($errorJson)
    exit 1
}
