[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BundleRoot,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = (Resolve-Path -LiteralPath $BundleRoot).Path
$outputFullPath = [IO.Path]::GetFullPath($OutputPath)
$utf8Strict = [Text.UTF8Encoding]::new($false, $true)
$scriptExtensions = @('.ps1', '.sh', '.py', '.js', '.ts', '.bat', '.cmd')
$binaryExtensions = @('.png', '.jpg', '.jpeg', '.gif', '.webp', '.jar', '.zip', '.gz', '.7z', '.exe', '.dll', '.so', '.pdf', '.docx')

$coreFiles = @(
    'moxinggang-overlay/security-research/RULES.md'
    'moxinggang-overlay/security-research/README_AI.md'
    'moxinggang-overlay/security-research/SKILL.md'
)

$keywordOverrides = @{
    'api-security'              = @('api', 'http', 'rest', 'graphql', 'jwt', 'oauth', 'authentication', 'authorization')
    'apk-reverse'               = @('apk', 'android', 'smali', 'frida', 'jadx')
    'attack-chain'              = @('attack chain', 'exploit chain', 'kill chain', 'lateral movement')
    'binary-diff'               = @('binary diff', 'bindiff', 'patch diff')
    'browser-automation'        = @('browser automation', 'playwright', 'selenium', 'cdp')
    'browser-extension-reverse' = @('browser extension', 'chrome extension', 'firefox extension')
    'cloud-k8s'                 = @('cloud', 'kubernetes', 'k8s', 'container', 'docker')
    'code-audit'                = @('code audit', 'source audit', 'sast', 'code review')
    'database-security'         = @('database', 'sql', 'postgresql', 'mysql', 'redis')
    'digital-forensics'         = @('forensics', 'disk image', 'memory dump', 'pcap')
    'dotnet-reverse'            = @('.net', 'dotnet', 'c#', 'dnspy', 'ilspy')
    'edr-bypass-re'             = @('edr', 'endpoint detection', 'unhook', 'telemetry')
    'email-security'            = @('email security', 'smtp', 'spf', 'dkim', 'dmarc')
    'firmware-pentest'          = @('firmware', 'embedded', 'binwalk', 'uart')
    'ghidra-reverse'            = @('ghidra', 'decompiler')
    'go-rust-reverse'           = @('golang binary', 'go binary', 'rust binary')
    'hardware-security'         = @('hardware security', 'jtag', 'spi', 'side channel')
    'ida-reverse'               = @('ida pro', 'idapython', 'disassembly')
    'identity-federation'       = @('saml', 'oidc', 'identity federation', 'single sign-on', 'sso')
    'js-reverse'                = @('javascript reverse', 'js reverse', 'web reverse', 'wasm', 'obfuscation')
    'llm-security'              = @('llm security', 'prompt injection', 'jailbreak', 'agent security')
    'macos-reverse'             = @('macos', 'mach-o', 'objective-c', 'swift binary')
    'malware-analysis'          = @('malware', 'ransomware', 'trojan', 'sandbox', 'yara')
    'mobile-reverse'            = @('mobile reverse', 'ios reverse', 'android reverse')
    'ot-ics'                    = @('ot security', 'ics', 'scada', 'modbus')
    'patch-diff-exploit'        = @('patch diff', 'cve patch', 'vulnerability patch')
    'protocol-reverse'          = @('protocol reverse', 'packet format', 'protobuf', 'websocket')
    'pwn-chain'                 = @('pwn', 'buffer overflow', 'rop', 'heap exploit', 'shellcode')
    'radare2'                   = @('radare2', 'r2', 'rizin')
    'radio-sdr'                 = @('radio', 'sdr', 'rf', 'signal')
    'reverse-engineering'       = @('reverse engineering', 'decompile', 'disassemble', 'binary analysis')
    'supply-chain-security'     = @('supply chain', 'dependency confusion', 'sbom')
    'thick-client'              = @('thick client', 'desktop client', 'windows client')
    'threat-hunting'            = @('threat hunting', 'ioc', 'sigma', 'mitre attack')
    'wifi-wireless'             = @('wifi', 'wireless', '802.11', 'wpa')
    'windows-ad'                = @('active directory', 'windows ad', 'kerberos', 'ldap')
}

$files = [Collections.Generic.List[object]]::new()
Get-ChildItem -LiteralPath $root -Recurse -File | Sort-Object FullName | ForEach-Object {
    if ([IO.Path]::GetFullPath($_.FullName) -eq $outputFullPath) {
        return
    }
    $relative = [IO.Path]::GetRelativePath($root, $_.FullName).Replace('\', '/')
    if ($relative -eq 'bundle-manifest.json') {
        return
    }
    $bytes = [IO.File]::ReadAllBytes($_.FullName)
    $extension = $_.Extension.ToLowerInvariant()
    $kind = if ($scriptExtensions -contains $extension) {
        'script'
    } elseif ($binaryExtensions -contains $extension) {
        'binary'
    } else {
        try {
            $null = $utf8Strict.GetString($bytes)
            'text'
        } catch {
            'binary'
        }
    }
    $digest = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($bytes)).ToLowerInvariant()
    $files.Add([ordered]@{
        path        = $relative
        sha256      = $digest
        byte_length = $bytes.Length
        kind        = $kind
        required    = $true
    })
}

$domains = [Collections.Generic.List[object]]::new()
$skillsRoot = Join-Path $root 'skills'
Get-ChildItem -LiteralPath $skillsRoot -Directory | Sort-Object Name | ForEach-Object {
    $entry = Join-Path $_.FullName 'SKILL.md'
    if (-not (Test-Path -LiteralPath $entry -PathType Leaf)) {
        return
    }
    $id = $_.Name
    [object[]]$keywords = if ($keywordOverrides.ContainsKey($id)) {
        @($keywordOverrides[$id])
    } else {
        @($id, $id.Replace('-', ' '))
    }
    $referencesRoot = Join-Path $_.FullName 'references'
    [object[]]$references = if (Test-Path -LiteralPath $referencesRoot -PathType Container) {
        @(Get-ChildItem -LiteralPath $referencesRoot -Recurse -File -Filter '*.md' |
            Sort-Object FullName |
            Select-Object -First 8 |
            ForEach-Object { [IO.Path]::GetRelativePath($root, $_.FullName).Replace('\', '/') })
    } else {
        @()
    }
    $domains.Add([ordered]@{
        id         = $id
        keywords   = [object[]]$keywords
        entry      = [IO.Path]::GetRelativePath($root, $entry).Replace('\', '/')
        references = [object[]]$references
    })
}

$manifest = [ordered]@{
    schema_version = 1
    bundle_id      = 'moxinggang-reverse-skill'
    version        = '2.8.0+d8bf34540cbc1aa34052e1b142576fc36a1f1437'
    core_files     = $coreFiles
    files          = $files
    domains        = $domains
}

$parent = Split-Path -Parent $outputFullPath
if (-not (Test-Path -LiteralPath $parent)) {
    $null = New-Item -ItemType Directory -Path $parent
}
$json = $manifest | ConvertTo-Json -Depth 8
# The published manifest digest covers CRLF bytes. Keep generation deterministic
# on Windows and non-Windows PowerShell hosts.
$json = $json -replace '\r?\n', "`r`n"
[IO.File]::WriteAllText($outputFullPath, $json, [Text.UTF8Encoding]::new($false))

$manifestBytes = [IO.File]::ReadAllBytes($outputFullPath)
$manifestHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($manifestBytes)).ToLowerInvariant()
[pscustomobject]@{
    BundleId       = $manifest.bundle_id
    ManifestSHA256 = $manifestHash
    Files          = $files.Count
    Domains        = $domains.Count
    OutputPath     = $outputFullPath
}
