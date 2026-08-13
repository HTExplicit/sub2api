param(
    [Parameter(Mandatory = $true)]
    [string]$OutputRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$upstreamRoot = 'https://moxinggang.com/skills/security-research/current'
$expectedFiles = 458
$expectedUpstreamFiles = 457
$expectedPinnedFiles = 1
$maximumFileBytes = 64MB
$deadline = [DateTimeOffset]::UtcNow.AddMinutes(5)
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$seedRoot = Join-Path $repository 'backend\internal\service\remote_skill_seed'
$manifestPath = Join-Path $seedRoot 'manifest.json'
$output = [IO.Path]::GetFullPath($OutputRoot)

if (-not $output.StartsWith($repository + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Output root must stay inside the repository'
}
if (Test-Path -LiteralPath $output) {
    throw 'Output root already exists'
}

$manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding utf8 | ConvertFrom-Json
if ($manifest.schema_version -ne 1 -or $manifest.bundle_id -ne 'security-research' -or
    $manifest.upstream_source_id -ne 'moxinggang' -or $manifest.upstream_root -ne $upstreamRoot -or
    $manifest.expected_file_count -ne $expectedFiles -or $manifest.upstream_file_count -ne $expectedUpstreamFiles -or
    $manifest.pinned_file_count -ne $expectedPinnedFiles -or @($manifest.files).Count -ne $expectedFiles) {
    throw 'Remote Skill manifest identity mismatch'
}

$seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($entry in $manifest.files) {
    $name = [string]$entry.path
    if ([string]::IsNullOrWhiteSpace($name) -or $name -ne $name.Normalize([Text.NormalizationForm]::FormC) -or
        $name.Contains('\') -or $name.StartsWith('/') -or $name.Contains('//') -or
        @($name.Split('/')) -contains '..' -or -not $seen.Add($name) -or
        [int64]$entry.byte_length -lt 1 -or [int64]$entry.byte_length -gt $maximumFileBytes -or
        [string]$entry.sha256 -notmatch '^[0-9a-f]{64}$') {
        throw "Remote Skill manifest entry rejected: $name"
    }
}

$upstreamEntries = @($manifest.files | Where-Object source_kind -eq 'upstream')
$pinnedEntries = @($manifest.files | Where-Object source_kind -eq 'pinned')
if ($upstreamEntries.Count -ne $expectedUpstreamFiles -or $pinnedEntries.Count -ne $expectedPinnedFiles) {
    throw 'Remote Skill manifest source counts mismatch'
}

$downloaded = @($upstreamEntries | ForEach-Object -Parallel {
    $entry = $_
    $remaining = $using:deadline - [DateTimeOffset]::UtcNow
    if ($remaining -le [TimeSpan]::Zero) {
        throw 'Remote Skill sync exceeded the five minute deadline'
    }
    $handler = [Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $client = [Net.Http.HttpClient]::new($handler)
    $client.Timeout = $remaining
    try {
        $encoded = (([string]$entry.path -split '/') | ForEach-Object { [Uri]::EscapeDataString($_) }) -join '/'
        $uri = "$using:upstreamRoot/$encoded"
        $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Get, $uri)
        $request.Headers.Accept.ParseAdd('text/markdown, text/plain;q=0.9, application/octet-stream;q=0.8')
        $request.Headers.UserAgent.ParseAdd('Sub2API-Remote-Skill-Seed/3')
        $response = $client.Send($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead)
        try {
            if ([int]$response.StatusCode -ne 200) {
                throw "Upstream returned $([int]$response.StatusCode) for $($entry.path)"
            }
            if ($response.RequestMessage.RequestUri.AbsoluteUri -ne $uri) {
                throw "Upstream redirect rejected for $($entry.path)"
            }
            $body = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
            $declaredLength = $response.Content.Headers.ContentLength
            if ($body.Length -ne [int]$entry.byte_length -or $body.Length -gt $using:maximumFileBytes -or
                ($null -ne $declaredLength -and $declaredLength -ne $body.Length)) {
                throw "Upstream length mismatch for $($entry.path)"
            }
            [void][Text.UTF8Encoding]::new($false, $true).GetString($body)
            $sha256 = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($body)).ToLowerInvariant()
            if ($sha256 -ne [string]$entry.sha256) {
                throw "Upstream SHA-256 mismatch for $($entry.path)"
            }
            [pscustomobject]@{ Path = [string]$entry.path; Body = [Convert]::ToBase64String($body) }
        } finally {
            $response.Dispose()
            $request.Dispose()
        }
    } finally {
        $client.Dispose()
        $handler.Dispose()
    }
} -ThrottleLimit 8)

if ($downloaded.Count -ne $expectedUpstreamFiles -or [DateTimeOffset]::UtcNow -gt $deadline) {
    throw "Expected $expectedUpstreamFiles upstream files; received $($downloaded.Count) within the deadline"
}

$files = [Collections.Generic.Dictionary[string, byte[]]]::new([StringComparer]::Ordinal)
foreach ($entry in $downloaded) {
    if ($files.ContainsKey([string]$entry.Path)) { throw "Duplicate upstream response: $($entry.Path)" }
    $files.Add([string]$entry.Path, [Convert]::FromBase64String([string]$entry.Body))
}
foreach ($entry in $pinnedEntries) {
    if ([string]$entry.path -ne 'skills/sec-assessment-tooling/pentest-tools/src-hunter/references/payloader/waf-bypass.md' -or
        [string]$entry.embedded_path -ne 'pinned/waf-bypass.md') {
        throw 'Pinned asset identity mismatch'
    }
    $source = [IO.Path]::GetFullPath((Join-Path $seedRoot ([string]$entry.embedded_path)))
    if (-not $source.StartsWith($seedRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Pinned asset escaped the seed root'
    }
    $body = [IO.File]::ReadAllBytes($source)
    $sha256 = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($body)).ToLowerInvariant()
    if ($body.Length -ne [int]$entry.byte_length -or $sha256 -ne [string]$entry.sha256) {
        throw 'Pinned asset content mismatch'
    }
    [void][Text.UTF8Encoding]::new($false, $true).GetString($body)
    $files.Add([string]$entry.path, $body)
}
if ($files.Count -ne $expectedFiles) { throw "Expected $expectedFiles complete files; received $($files.Count)" }

$parent = [IO.Path]::GetDirectoryName($output)
[IO.Directory]::CreateDirectory($parent) | Out-Null
$staging = Join-Path $parent ('.remote-skill-' + [Guid]::NewGuid().ToString('N'))
try {
    [IO.Directory]::CreateDirectory($staging) | Out-Null
    foreach ($entry in $files.GetEnumerator()) {
        $destination = [IO.Path]::GetFullPath((Join-Path $staging $entry.Key))
        if (-not $destination.StartsWith($staging + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Destination escaped output root: $($entry.Key)"
        }
        [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination)) | Out-Null
        [IO.File]::WriteAllBytes($destination, $entry.Value)
    }
    Move-Item -LiteralPath $staging -Destination $output
    [pscustomobject]@{ Files = $files.Count; UpstreamFiles = $downloaded.Count; PinnedFiles = $pinnedEntries.Count; OutputRoot = $output }
} finally {
    if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging -Recurse -Force }
}
