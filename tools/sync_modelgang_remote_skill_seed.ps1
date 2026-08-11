param(
    [string]$OutputRoot = (Join-Path $PSScriptRoot '..\backend\internal\service\remote_skill_seed\tree')
)

$ErrorActionPreference = 'Stop'
$upstreamRoot = 'https://moxinggang.com/skills/security-research/current'
$expectedCount = 73
$maximumFileBytes = 8MB
$output = [IO.Path]::GetFullPath($OutputRoot)
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if (-not $output.StartsWith($repository + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Output root must stay inside the repository'
}
if (Test-Path -LiteralPath $output) {
    throw 'Output root already exists'
}

$handler = [Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$client = [Net.Http.HttpClient]::new($handler)
$client.Timeout = [TimeSpan]::FromSeconds(45)
$client.DefaultRequestHeaders.UserAgent.ParseAdd('Sub2API-Remote-Skill-Seed/2')
$queue = [Collections.Generic.Queue[string]]::new()
@(
    'assets/templates/cloud-handoff-prompt.md',
    'assets/templates/ctf-writeup.md',
    'assets/templates/mission-state.md',
    'assets/templates/research-result.json',
    'assets/templates/writeups-index.md',
    'ctf-orchestrator/ctf-sandbox-orchestrator/INSTRUCTIONS.md',
    'ctf-orchestrator/ctf-sandbox-orchestrator/references/router-matrix.md',
    'NOTICE.md',
    'README_AI.md',
    'references/ai-security.md',
    'references/ctf/ai-ml/index.md',
    'references/ctf/ai-ml/llm-attacks.md',
    'references/ctf/crypto/index.md',
    'references/ctf/forensics/index.md',
    'references/ctf/malware/index.md',
    'references/ctf/misc/index.md',
    'references/ctf/osint/index.md',
    'references/ctf/pwn/index.md',
    'references/ctf/reverse/index.md',
    'references/ctf/web/index.md',
    'references/ctf/writeup/index.md',
    'references/environment-and-resources.md',
    'references/evidence-workflow.md',
    'references/experience-index.md',
    'references/field-journal-template.md',
    'references/malware.md',
    'references/pentest.md',
    'references/precedent-auth.md',
    'references/progressive-reasoning.md',
    'references/reporting.md',
    'references/reverse.md',
    'references/routing.md',
    'references/scanner.md',
    'references/scope-and-evidence.md',
    'references/source-provenance.md',
    'references/tools.md',
    'RULES.md',
    'schemas/research-result.schema.json',
    'scripts/env_probe.py',
    'scripts/reusable/artifact_inventory.py',
    'scripts/reusable/har_summary.py',
    'scripts/reusable/new_experience_entry.py',
    'scripts/reusable/new-experience-entry.ps1',
    'scripts/reusable/pack_cloud_handoff.py',
    'scripts/reusable/pe_entropy_triage.py',
    'scripts/reusable/route_task.py',
    'scripts/reusable/scaffold_project.py',
    'scripts/validate_result.py',
    'scripts/validate_skill.py',
    'SKILL.md',
    'skills/sec-ai-security/INSTRUCTIONS.md',
    'skills/sec-ai-security/references/llm-deep/_llm-security-workflow.md',
    'skills/sec-ai-security/references/llm-deep/agent-obedience-engineering.md',
    'skills/sec-ai-security/references/llm-deep/agent-security-testing.md',
    'skills/sec-ai-security/references/llm-deep/owasp-llm-top10.md',
    'skills/sec-ai-security/references/llm-deep/prompt-injection-methodology.md',
    'skills/sec-assessment-tooling/INSTRUCTIONS.md',
    'skills/sec-assessment-tooling/pentest-tools/INSTRUCTIONS.md',
    'skills/sec-attack-chain/INSTRUCTIONS.md',
    'skills/sec-crypto/INSTRUCTIONS.md',
    'skills/sec-edr-bypass/INSTRUCTIONS.md',
    'skills/sec-firmware/INSTRUCTIONS.md',
    'skills/sec-forensics-dfir/INSTRUCTIONS.md',
    'skills/sec-malware/INSTRUCTIONS.md',
    'skills/sec-misc/INSTRUCTIONS.md',
    'skills/sec-osint/INSTRUCTIONS.md',
    'skills/sec-patch-diff/INSTRUCTIONS.md',
    'skills/sec-pwn-chain/INSTRUCTIONS.md',
    'skills/sec-pwn-native/INSTRUCTIONS.md',
    'skills/sec-reporting/INSTRUCTIONS.md',
    'skills/sec-reverse/INSTRUCTIONS.md',
    'skills/sec-supply-chain/INSTRUCTIONS.md',
    'skills/sec-web-api/INSTRUCTIONS.md'
) | ForEach-Object { $queue.Enqueue($_) }
$files = [Collections.Generic.Dictionary[string, byte[]]]::new([StringComparer]::Ordinal)

try {
    while ($queue.Count -gt 0) {
        $name = $queue.Dequeue()
        if ($files.ContainsKey($name)) { continue }
        if ($name -notmatch '^[A-Za-z0-9][A-Za-z0-9._/-]*$' -or $name.Contains('..') -or $name.Contains('\') -or $name.StartsWith('/')) {
            throw "Rejected upstream path: $name"
        }
        $uri = [Uri]("$upstreamRoot/$name")
        $response = $client.GetAsync($uri).GetAwaiter().GetResult()
        try {
            if ([int]$response.StatusCode -ne 200) {
                throw "Upstream returned $([int]$response.StatusCode) for $name"
            }
            if ($response.Headers.Location) {
                throw "Upstream redirect rejected for $name"
            }
            $body = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
            if ($body.Length -eq 0 -or $body.Length -gt $maximumFileBytes) {
                throw "Upstream file size rejected for $name"
            }
            $files.Add($name, $body)
        } finally {
            $response.Dispose()
        }
    }

    if ($files.Count -ne $expectedCount) {
        throw "Expected $expectedCount files, discovered $($files.Count)"
    }
    foreach ($entry in $files.GetEnumerator()) {
        $destination = [IO.Path]::GetFullPath((Join-Path $output $entry.Key))
        if (-not $destination.StartsWith($output + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Destination escaped output root: $($entry.Key)"
        }
        [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination)) | Out-Null
        [IO.File]::WriteAllBytes($destination, $entry.Value)
    }
    [pscustomobject]@{ Files = $files.Count; OutputRoot = $output }
} finally {
    $client.Dispose()
    $handler.Dispose()
}
