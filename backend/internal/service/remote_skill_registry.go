package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	RemoteSkillSourceGitHubOfficial      = "github_official"
	RemoteSkillSourceMoxinggang          = "moxinggang"
	RemoteSkillMoxinggangPath            = "/skills/security-research/current"
	RemoteSkillMoxinggangRoot            = "https://moxinggang.com" + RemoteSkillMoxinggangPath
	RemoteSkillGitHubRoot                = "https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/" + remoteSkillPinnedCommit + "/skills"
	RemoteSkillPublishedVersionsRoot     = "https://codexrip.vip/skills/reverse-skill/versions/"
	RemoteSkillSyncStatusQueued          = "queued"
	RemoteSkillSyncStatusRunning         = "running"
	RemoteSkillSyncStatusSucceeded       = "succeeded"
	RemoteSkillSyncStatusFailed          = "failed"
	RemoteSkillInstallStrategy           = "verified_https_content_addressed"
	RemoteSkillPowerShellBootstrapSHA256 = "2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9"
	RemoteSkillPythonBootstrapSHA256     = "353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9"
	RemoteSkillPowerShellBootstrapURL    = "https://codexrip.vip/skills/bootstrap/" + RemoteSkillPowerShellBootstrapSHA256 + "/bootstrap-reverse-skill.ps1"
	RemoteSkillPythonBootstrapURL        = "https://codexrip.vip/skills/bootstrap/" + RemoteSkillPythonBootstrapSHA256 + "/bootstrap-reverse-skill.py"
	RemoteSkillDescriptorURL             = "https://codexrip.vip/skills/reverse-skill/current.json"
)

var (
	ErrRemoteSkillSeedUnavailable = errors.New("remote skill seed unavailable")
	ErrRemoteSkillVersionNotFound = errors.New("remote skill bundle version not found")
	ErrRemoteSkillSyncNotFound    = errors.New("remote skill sync job not found")
)

type RemoteSkillBundleVersion struct {
	ID             int64      `json:"id"`
	BundleID       string     `json:"bundle_id"`
	SourceID       string     `json:"source_id"`
	RemoteRoot     string     `json:"remote_root"`
	SourceCommit   string     `json:"source_commit"`
	OverlaySHA256  string     `json:"overlay_sha256"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	ArchiveSHA256  string     `json:"archive_sha256"`
	FileCount      int        `json:"file_count"`
	TotalBytes     int64      `json:"total_bytes"`
	AddedFiles     int        `json:"added_files"`
	ModifiedFiles  int        `json:"modified_files"`
	DeletedFiles   int        `json:"deleted_files"`
	ScriptChanges  int        `json:"script_changes"`
	BinaryChanges  int        `json:"binary_changes"`
	CreatedBy      int64      `json:"created_by,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	PublishedBy    int64      `json:"published_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type RemoteSkillBundleVersionDetail struct {
	RemoteSkillBundleVersion
	Verified        bool     `json:"verified"`
	RoutingWarnings []string `json:"routing_warnings,omitempty"`
}

type RemoteSkillRegistrySnapshot struct {
	Revision       int64                     `json:"revision"`
	SourceID       string                    `json:"source_id,omitempty"`
	RemoteRoot     string                    `json:"remote_root,omitempty"`
	Active         *RemoteSkillBundleVersion `json:"active,omitempty"`
	Degraded       bool                      `json:"degraded"`
	DegradedReason string                    `json:"degraded_reason,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type RemoteSkillClientInstaller struct {
	Strategy        string `json:"strategy"`
	BootstrapURL    string `json:"bootstrap_url"`
	BootstrapSHA256 string `json:"bootstrap_sha256"`
	AcquireCommand  string `json:"acquire_command"`
	ExecuteCommand  string `json:"execute_command"`
}

type RemoteSkillClientInstall struct {
	SkillName      string                     `json:"skill_name"`
	SourceID       string                     `json:"source_id"`
	RemoteRoot     string                     `json:"remote_root"`
	SourceCommit   string                     `json:"source_commit,omitempty"`
	ManifestSHA256 string                     `json:"manifest_sha256,omitempty"`
	DescriptorURL  string                     `json:"descriptor_url"`
	PowerShell     RemoteSkillClientInstaller `json:"powershell"`
	Python         RemoteSkillClientInstaller `json:"python"`
}

type RemoteSkillSyncJob struct {
	ID                       int64      `json:"id"`
	SourceID                 string     `json:"source_id"`
	Status                   string     `json:"status"`
	ProgressStage            string     `json:"progress_stage"`
	SourceCommit             string     `json:"source_commit,omitempty"`
	CandidateBundleVersionID int64      `json:"candidate_bundle_version_id,omitempty"`
	ErrorCode                string     `json:"error_code,omitempty"`
	CreatedBy                int64      `json:"created_by,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	StartedAt                *time.Time `json:"started_at,omitempty"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
}

type RemoteSkillCandidate struct {
	Version       RemoteSkillBundleVersion
	Manifest      BusinessSystemPromptBundleManifest
	ManifestBytes []byte
	ArchiveBytes  []byte
	Files         map[string][]byte
}

type RemoteSkillRegistryStore interface {
	EnsureRemoteSkillSeed(context.Context, RemoteSkillBundleVersion) error
	LoadRemoteSkillSnapshot(context.Context) (RemoteSkillRegistrySnapshot, error)
	ListRemoteSkillVersions(context.Context) ([]RemoteSkillBundleVersion, error)
	GetRemoteSkillVersion(context.Context, int64) (RemoteSkillBundleVersion, error)
	CreateRemoteSkillSyncJob(context.Context, string, int64, int64) (RemoteSkillSyncJob, error)
	UpdateRemoteSkillSyncJobStage(context.Context, int64, string) error
	CompleteRemoteSkillSyncJob(context.Context, int64, RemoteSkillBundleVersion) (RemoteSkillSyncJob, error)
	FailRemoteSkillSyncJob(context.Context, int64, string) error
	GetRemoteSkillSyncJob(context.Context, int64) (RemoteSkillSyncJob, error)
	PublishRemoteSkillVersion(context.Context, int64, int64, int64) (RemoteSkillRegistrySnapshot, error)
}

type RemoteSkillRegistryFiles interface {
	LoadSeed(context.Context) (RemoteSkillBundleVersion, error)
	InstallCandidate(context.Context, RemoteSkillCandidate) error
	ValidateVersion(context.Context, RemoteSkillBundleVersion) error
	PreparePublic(context.Context, RemoteSkillBundleVersion) error
	Activate(context.Context, RemoteSkillRegistrySnapshot) error
	LoadManifest(context.Context, RemoteSkillBundleVersion) (BusinessSystemPromptBundleManifest, error)
	LoadBundle(context.Context, RemoteSkillBundleVersion) (*BusinessSystemPromptBundle, error)
}

type RemoteSkillCandidateSource interface {
	Build(context.Context, string, *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error)
}

func NormalizeRemoteSkillSourceID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return RemoteSkillSourceGitHubOfficial, nil
	}
	switch value {
	case RemoteSkillSourceGitHubOfficial, RemoteSkillSourceMoxinggang:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unknown remote skill source", ErrBusinessSystemPromptInvalid)
	}
}

func remoteSkillSourceRoot(sourceID string) string {
	if sourceID == RemoteSkillSourceMoxinggang {
		return RemoteSkillMoxinggangRoot
	}
	return RemoteSkillGitHubRoot
}

func remoteSkillSourceEntryURL(sourceID string) string {
	return remoteSkillSourceRoot(sourceID) + "/SKILL.md"
}

func remoteSkillPublishedRoot(sourceID, manifestSHA256 string) string {
	root := RemoteSkillPublishedVersionsRoot + strings.ToLower(strings.TrimSpace(manifestSHA256))
	if sourceID == RemoteSkillSourceGitHubOfficial {
		return root + "/skills"
	}
	return root
}

func normalizeRemoteSkillVersionSource(version *RemoteSkillBundleVersion) {
	if version == nil {
		return
	}
	if strings.TrimSpace(version.SourceID) == "" {
		version.SourceID = RemoteSkillSourceGitHubOfficial
	}
	if strings.TrimSpace(version.RemoteRoot) == "" {
		version.RemoteRoot = remoteSkillSourceRoot(version.SourceID)
	}
}

type RemoteSkillRegistryRevisionBus interface {
	Publish(context.Context, int64, string) error
	Subscribe(context.Context, func(int64, string)) error
}

type RemoteSkillRegistryService struct {
	store  RemoteSkillRegistryStore
	bus    RemoteSkillRegistryRevisionBus
	files  RemoteSkillRegistryFiles
	source RemoteSkillCandidateSource

	snapshot atomic.Pointer[RemoteSkillRegistrySnapshot]
	bundle   atomic.Pointer[BusinessSystemPromptBundle]
	stateMu  sync.Mutex
	applyMu  sync.Mutex
	runMu    sync.Mutex
	runCtx   context.Context
	cancel   context.CancelFunc
	started  bool
	wg       sync.WaitGroup
}

func NewRemoteSkillRegistryService(
	store RemoteSkillRegistryStore,
	bus RemoteSkillRegistryRevisionBus,
	files RemoteSkillRegistryFiles,
	source RemoteSkillCandidateSource,
) *RemoteSkillRegistryService {
	return &RemoteSkillRegistryService{store: store, bus: bus, files: files, source: source}
}

func (s *RemoteSkillRegistryService) Initialize(ctx context.Context) error {
	if s == nil || s.store == nil || s.files == nil {
		return errors.New("remote skill registry unavailable")
	}
	seed, err := s.files.LoadSeed(ctx)
	if err == nil {
		if seed.SourceID == "" {
			seed.SourceID = RemoteSkillSourceGitHubOfficial
		}
		if seed.RemoteRoot == "" {
			seed.RemoteRoot = remoteSkillSourceRoot(seed.SourceID)
		}
		if err := s.store.EnsureRemoteSkillSeed(ctx, seed); err != nil {
			return fmt.Errorf("ensure remote skill seed: %w", err)
		}
	} else if !errors.Is(err, ErrRemoteSkillSeedUnavailable) {
		return fmt.Errorf("load remote skill seed: %w", err)
	}
	return s.Reload(ctx)
}

func (s *RemoteSkillRegistryService) Start(ctx context.Context) error {
	if err := s.Initialize(ctx); err != nil {
		return err
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.started {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.runCtx = runCtx
	s.cancel = cancel
	s.started = true
	if s.bus != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for runCtx.Err() == nil {
				_ = s.bus.Subscribe(runCtx, func(revision int64, _ string) {
					current := s.CurrentSnapshot()
					if revision == 0 || revision > current.Revision {
						_ = s.Reload(runCtx)
					}
				})
				if runCtx.Err() != nil {
					return
				}
				s.markDegraded("revision_subscription_failed")
				timer := time.NewTimer(time.Second)
				select {
				case <-runCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	return nil
}

func (s *RemoteSkillRegistryService) Stop() {
	if s == nil {
		return
	}
	s.runMu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.runCtx = nil
	s.cancel = nil
	s.started = false
	s.runMu.Unlock()
	s.wg.Wait()
}

func (s *RemoteSkillRegistryService) CurrentSnapshot() RemoteSkillRegistrySnapshot {
	if s == nil {
		return RemoteSkillRegistrySnapshot{Degraded: true, DegradedReason: "service_unavailable"}
	}
	current := s.snapshot.Load()
	if current == nil {
		return RemoteSkillRegistrySnapshot{Degraded: true, DegradedReason: "not_loaded"}
	}
	return cloneRemoteSkillRegistrySnapshot(*current)
}

func (s *RemoteSkillRegistryService) ClientInstallMetadata() RemoteSkillClientInstall {
	metadata := RemoteSkillClientInstall{
		SkillName:     "codexrip-reverse-skill",
		SourceID:      RemoteSkillSourceGitHubOfficial,
		RemoteRoot:    RemoteSkillGitHubRoot,
		DescriptorURL: RemoteSkillDescriptorURL,
		PowerShell:    remoteSkillPowerShellInstaller(),
		Python:        remoteSkillPythonInstaller(),
	}
	if snapshot := s.CurrentSnapshot(); snapshot.Active != nil {
		metadata.SourceID = snapshot.Active.SourceID
		metadata.RemoteRoot = snapshot.Active.RemoteRoot
		metadata.SourceCommit = snapshot.Active.SourceCommit
		metadata.ManifestSHA256 = snapshot.Active.ManifestSHA256
	}
	return metadata
}

func remoteSkillPowerShellInstaller() RemoteSkillClientInstaller {
	acquire := fmt.Sprintf(`$url='%s'
$hash='%s'
$temp=[IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$root=[IO.Path]::GetFullPath((Join-Path $temp ('codexrip-reverse-skill-bootstrap-'+$hash)))
if($root -eq $temp -or -not $root.StartsWith($temp,[StringComparison]::OrdinalIgnoreCase)){throw 'bootstrap directory rejected'}
$null=New-Item -ItemType Directory -Path $root -Force
$rootItem=Get-Item -LiteralPath $root -Force
if(-not $rootItem.PSIsContainer -or ($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint)){throw 'bootstrap directory rejected'}
$path=Join-Path $root 'bootstrap-reverse-skill.ps1'
$download=Join-Path $root ('.download-'+[guid]::NewGuid().ToString('N'))
try{
  $response=Invoke-WebRequest -Uri $url -MaximumRedirection 0 -TimeoutSec 30 -OutFile $download -PassThru -UseBasicParsing
  $final=$response.BaseResponse.RequestMessage.RequestUri
  if($final.Scheme -cne 'https' -or $final.Host -cne 'codexrip.vip' -or -not $final.IsDefaultPort -or $final.UserInfo -or $final.Query -or $final.Fragment){throw 'bootstrap final URL rejected'}
  $item=Get-Item -LiteralPath $download -Force
  if($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $item.Length -le 0 -or $item.Length -gt 1MB){throw 'bootstrap download rejected'}
  if((Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant() -cne $hash){throw 'bootstrap hash mismatch'}
  Move-Item -LiteralPath $download -Destination $path -Force
}finally{
  Remove-Item -LiteralPath $download -Force -ErrorAction SilentlyContinue
}
$path`, RemoteSkillPowerShellBootstrapURL, RemoteSkillPowerShellBootstrapSHA256)
	execute := fmt.Sprintf(`$hash='%s'
$descriptor='%s'
$temp=[IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$root=[IO.Path]::GetFullPath((Join-Path $temp ('codexrip-reverse-skill-bootstrap-'+$hash)))
$path=[IO.Path]::GetFullPath((Join-Path $root 'bootstrap-reverse-skill.ps1'))
if($root -eq $temp -or -not $root.StartsWith($temp,[StringComparison]::OrdinalIgnoreCase) -or -not (Test-Path -LiteralPath $path -PathType Leaf)){throw 'bootstrap path rejected'}
$item=Get-Item -LiteralPath $path -Force
if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'bootstrap path rejected'}
if((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $hash){throw 'bootstrap hash mismatch'}
$output=@(& $path -DescriptorUrl $descriptor)
if($LASTEXITCODE -ne 0){throw 'bootstrap execution failed'}
$result=$output[-1] | ConvertFrom-Json
if($result.status -cne 'ready' -or $result.scripts_executed -ne $false){throw 'bootstrap result rejected'}
$result | ConvertTo-Json -Compress -Depth 8`, RemoteSkillPowerShellBootstrapSHA256, RemoteSkillDescriptorURL)
	return remoteSkillClientInstaller(RemoteSkillPowerShellBootstrapURL, RemoteSkillPowerShellBootstrapSHA256, acquire, execute)
}

func remoteSkillPythonInstaller() RemoteSkillClientInstaller {
	acquire := fmt.Sprintf(`url='%s'
hash='%s'
temp="${TMPDIR:-/tmp}"
uid="$(id -u)"
root="$temp/codexrip-reverse-skill-bootstrap-$uid-$hash"
path="$root/bootstrap-reverse-skill.py"
python3 - "$url" "$hash" "$temp" "$root" "$path" <<'PY'
import hashlib, os, pathlib, stat, sys, tempfile, urllib.error, urllib.parse, urllib.request
url, expected, temp_root, root, target = sys.argv[1:]
parsed = urllib.parse.urlparse(url)
if parsed.scheme != "https" or parsed.hostname != "codexrip.vip" or parsed.port not in (None, 443) or parsed.username or parsed.password or parsed.query or parsed.fragment:
    raise SystemExit("bootstrap URL rejected")
temp_root = os.path.realpath(temp_root)
root = os.path.abspath(root)
target = os.path.abspath(target)
if os.path.commonpath((temp_root, root)) != temp_root or os.path.dirname(target) != root:
    raise SystemExit("bootstrap directory rejected")
try:
    os.mkdir(root, 0o700)
except FileExistsError:
    pass
root_info = os.stat(root, follow_symlinks=False)
if not stat.S_ISDIR(root_info.st_mode) or root_info.st_uid != os.geteuid() or stat.S_IMODE(root_info.st_mode) != 0o700:
    raise SystemExit("bootstrap directory rejected")
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
request = urllib.request.Request(url, headers={"User-Agent": "CodexRip-Bootstrap-Acquirer/1"})
try:
    with urllib.request.build_opener(NoRedirect).open(request, timeout=30) as response:
        final = urllib.parse.urlparse(response.geturl())
        if final.scheme != "https" or final.hostname != "codexrip.vip" or final.port not in (None, 443):
            raise SystemExit("bootstrap final URL rejected")
        declared = response.headers.get("Content-Length")
        if declared and int(declared) > 1048576:
            raise SystemExit("bootstrap download rejected")
        raw = response.read(1048577)
except urllib.error.HTTPError as exc:
    raise SystemExit("bootstrap download rejected") from exc
if not raw or len(raw) > 1048576 or hashlib.sha256(raw).hexdigest() != expected:
    raise SystemExit("bootstrap hash mismatch")
fd, staging = tempfile.mkstemp(prefix=".download-", dir=root)
try:
    with os.fdopen(fd, "wb") as handle:
        os.fchmod(handle.fileno(), 0o600)
        handle.write(raw)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(staging, target)
    target_info = os.stat(target, follow_symlinks=False)
    if not stat.S_ISREG(target_info.st_mode) or target_info.st_uid != os.geteuid() or stat.S_IMODE(target_info.st_mode) != 0o600:
        os.unlink(target)
        raise SystemExit("bootstrap target rejected")
finally:
    if os.path.exists(staging):
        os.unlink(staging)
print(pathlib.Path(target).resolve())
PY`, RemoteSkillPythonBootstrapURL, RemoteSkillPythonBootstrapSHA256)
	execute := fmt.Sprintf(`hash='%s'
descriptor='%s'
temp="${TMPDIR:-/tmp}"
uid="$(id -u)"
root="$temp/codexrip-reverse-skill-bootstrap-$uid-$hash"
path="$root/bootstrap-reverse-skill.py"
result="$(python3 - "$path" "$hash" "$descriptor" "$temp" "$root" <<'PY'
import hashlib, os, stat, sys
target, expected, descriptor, temp_root, root = sys.argv[1:]
temp_root = os.path.realpath(temp_root)
root = os.path.abspath(root)
target = os.path.abspath(target)
if os.path.commonpath((temp_root, root)) != temp_root or os.path.dirname(target) != root:
    raise SystemExit("bootstrap directory rejected")
root_info = os.stat(root, follow_symlinks=False)
if not stat.S_ISDIR(root_info.st_mode) or root_info.st_uid != os.geteuid() or stat.S_IMODE(root_info.st_mode) != 0o700:
    raise SystemExit("bootstrap directory rejected")
fd = os.open(target, os.O_RDONLY | os.O_NOFOLLOW)
try:
    target_info = os.fstat(fd)
    if (
        not stat.S_ISREG(target_info.st_mode)
        or target_info.st_uid != os.geteuid()
        or stat.S_IMODE(target_info.st_mode) != 0o600
        or target_info.st_nlink != 1
        or target_info.st_size <= 0
        or target_info.st_size > 1048576
    ):
        raise SystemExit("bootstrap target rejected")
    with os.fdopen(os.dup(fd), "rb") as handle:
        raw = handle.read(1048577)
    if len(raw) != target_info.st_size or hashlib.sha256(raw).hexdigest() != expected:
        raise SystemExit("bootstrap hash mismatch")
    sys.argv = [target, "--descriptor-url", descriptor]
    namespace = {
        "__name__": "__main__",
        "__file__": target,
        "__package__": None,
        "__builtins__": __builtins__,
    }
    exec(compile(raw, target, "exec"), namespace, namespace)
finally:
    os.close(fd)
PY
)" || exit 1
python3 -c 'import json,sys; value=json.loads(sys.argv[1]); assert value.get("status")=="ready" and value.get("scripts_executed") is False; print(json.dumps(value,separators=(",",":"),ensure_ascii=False))' "$result"`, RemoteSkillPythonBootstrapSHA256, RemoteSkillDescriptorURL)
	return remoteSkillClientInstaller(RemoteSkillPythonBootstrapURL, RemoteSkillPythonBootstrapSHA256, acquire, execute)
}

func remoteSkillClientInstaller(bootstrapURL, hash, acquire, execute string) RemoteSkillClientInstaller {
	return RemoteSkillClientInstaller{
		Strategy:        RemoteSkillInstallStrategy,
		BootstrapURL:    bootstrapURL,
		BootstrapSHA256: hash,
		AcquireCommand:  acquire,
		ExecuteCommand:  execute,
	}
}

// ActiveBundle returns the last verified immutable registry bundle. A
// degraded snapshot may still be served when it is the last-known-good copy;
// callers surface the degraded flag without switching to unverified bytes.
func (s *RemoteSkillRegistryService) ActiveBundle(ctx context.Context) (RemoteSkillRegistrySnapshot, *BusinessSystemPromptBundle, error) {
	snapshot := s.CurrentSnapshot()
	if snapshot.Active == nil || s == nil || s.files == nil {
		return snapshot, nil, fmt.Errorf("%w: no active skill bundle", ErrBusinessSystemPromptBundleUnavailable)
	}
	if current := s.bundle.Load(); current != nil && current.ManifestSHA256 == snapshot.Active.ManifestSHA256 {
		return snapshot, current, nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.bundle.Load(); current != nil && current.ManifestSHA256 == snapshot.Active.ManifestSHA256 {
		return snapshot, current, nil
	}
	bundle, err := s.files.LoadBundle(ctx, *snapshot.Active)
	if err != nil {
		return snapshot, nil, err
	}
	if bundle == nil || bundle.ManifestSHA256 != snapshot.Active.ManifestSHA256 {
		return snapshot, nil, fmt.Errorf("%w: active bundle identity mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	s.bundle.Store(bundle)
	return snapshot, bundle, nil
}

func (s *RemoteSkillRegistryService) Reload(ctx context.Context) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	loaded, err := s.store.LoadRemoteSkillSnapshot(ctx)
	if err != nil {
		return s.retainLastKnownGood(err)
	}
	if loaded.Active == nil {
		loaded.Degraded = true
		loaded.DegradedReason = "no_active_bundle"
		s.installSnapshot(loaded)
		return nil
	}
	normalizeRemoteSkillVersionSource(loaded.Active)
	loaded.SourceID = loaded.Active.SourceID
	loaded.RemoteRoot = loaded.Active.RemoteRoot
	if err := s.files.ValidateVersion(ctx, *loaded.Active); err != nil {
		return s.retainLastKnownGood(fmt.Errorf("validate active remote skill: %w", err))
	}
	if err := s.files.PreparePublic(ctx, *loaded.Active); err != nil {
		return s.retainLastKnownGood(fmt.Errorf("prepare public remote skill: %w", err))
	}
	if err := s.files.Activate(ctx, loaded); err != nil {
		return s.retainLastKnownGood(fmt.Errorf("activate public remote skill descriptor: %w", err))
	}
	loaded.Degraded = false
	loaded.DegradedReason = ""
	s.installSnapshot(loaded)
	return nil
}

func (s *RemoteSkillRegistryService) StartSync(ctx context.Context, sourceID string, actorID, expectedRevision int64) (RemoteSkillSyncJob, error) {
	if s == nil || s.store == nil || s.source == nil {
		return RemoteSkillSyncJob{}, errors.New("remote skill sync unavailable")
	}
	sourceID, err := NormalizeRemoteSkillSourceID(sourceID)
	if err != nil {
		return RemoteSkillSyncJob{}, err
	}
	job, err := s.store.CreateRemoteSkillSyncJob(ctx, sourceID, actorID, expectedRevision)
	if err != nil {
		return RemoteSkillSyncJob{}, err
	}
	s.runMu.Lock()
	if !s.started || s.runCtx == nil {
		s.runMu.Unlock()
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "service_stopped")
		return RemoteSkillSyncJob{}, errors.New("remote skill sync service is not running")
	}
	runCtx := s.runCtx
	s.wg.Add(1)
	s.runMu.Unlock()
	go func() {
		defer s.wg.Done()
		s.runSyncJob(runCtx, job)
	}()
	return job, nil
}

func (s *RemoteSkillRegistryService) runSyncJob(ctx context.Context, job RemoteSkillSyncJob) {
	if err := s.store.UpdateRemoteSkillSyncJobStage(ctx, job.ID, "fetching_source"); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
		return
	}
	var activeManifest *BusinessSystemPromptBundleManifest
	current := s.CurrentSnapshot()
	if current.Active != nil {
		if manifest, err := s.files.LoadManifest(ctx, *current.Active); err == nil {
			activeManifest = &manifest
		}
	}
	candidate, err := s.source.Build(ctx, job.SourceID, activeManifest)
	if err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, remoteSkillSyncErrorCode(err))
		return
	}
	if candidate.Version.SourceID != job.SourceID || candidate.Version.RemoteRoot != remoteSkillSourceRoot(job.SourceID) {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "bundle_invalid")
		return
	}
	if err := s.store.UpdateRemoteSkillSyncJobStage(ctx, job.ID, "verifying_candidate"); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
		return
	}
	if err := s.files.InstallCandidate(ctx, candidate); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, remoteSkillSyncErrorCode(err))
		return
	}
	candidate.Version.CreatedBy = job.CreatedBy
	if _, err := s.store.CompleteRemoteSkillSyncJob(ctx, job.ID, candidate.Version); err != nil {
		_ = s.store.FailRemoteSkillSyncJob(ctx, job.ID, "storage_error")
	}
}

func (s *RemoteSkillRegistryService) PublishVersion(ctx context.Context, versionID, expectedRevision, actorID int64) (RemoteSkillRegistrySnapshot, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	version, err := s.store.GetRemoteSkillVersion(ctx, versionID)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, err
	}
	if err := s.files.ValidateVersion(ctx, version); err != nil {
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	if err := s.files.PreparePublic(ctx, version); err != nil {
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: public skill preparation failed", ErrBusinessSystemPromptUnavailable)
	}
	snapshot, err := s.store.PublishRemoteSkillVersion(ctx, versionID, expectedRevision, actorID)
	if err != nil {
		return RemoteSkillRegistrySnapshot{}, err
	}
	if err := s.files.Activate(ctx, snapshot); err != nil {
		_ = s.retainLastKnownGood(err)
		return RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: public descriptor activation failed", ErrBusinessSystemPromptUnavailable)
	}
	s.installSnapshot(snapshot)
	if s.bus != nil && snapshot.Active != nil {
		if err := s.bus.Publish(ctx, snapshot.Revision, snapshot.Active.ManifestSHA256); err != nil {
			snapshot.Degraded = true
			snapshot.DegradedReason = "revision_broadcast_failed"
			s.installSnapshot(snapshot)
		}
	}
	return snapshot, nil
}

func (s *RemoteSkillRegistryService) ListVersions(ctx context.Context) ([]RemoteSkillBundleVersion, error) {
	return s.store.ListRemoteSkillVersions(ctx)
}

func (s *RemoteSkillRegistryService) GetVersion(ctx context.Context, id int64) (RemoteSkillBundleVersion, error) {
	return s.store.GetRemoteSkillVersion(ctx, id)
}

func (s *RemoteSkillRegistryService) InspectVersion(ctx context.Context, id int64) (RemoteSkillBundleVersionDetail, error) {
	version, err := s.store.GetRemoteSkillVersion(ctx, id)
	if err != nil {
		return RemoteSkillBundleVersionDetail{}, err
	}
	if err := s.files.ValidateVersion(ctx, version); err != nil {
		return RemoteSkillBundleVersionDetail{}, fmt.Errorf("%w: candidate validation failed", ErrBusinessSystemPromptUnavailable)
	}
	manifest, err := s.files.LoadManifest(ctx, version)
	if err != nil {
		return RemoteSkillBundleVersionDetail{}, fmt.Errorf("%w: candidate manifest unavailable", ErrBusinessSystemPromptUnavailable)
	}
	warnings := make([]string, 0)
	for _, route := range manifest.Domains {
		if len(remoteSkillRouteKeywords[route.ID]) == 0 {
			warnings = append(warnings, "missing_bilingual_mapping:"+route.ID)
		}
	}
	return RemoteSkillBundleVersionDetail{RemoteSkillBundleVersion: version, Verified: true, RoutingWarnings: warnings}, nil
}

func (s *RemoteSkillRegistryService) GetSyncJob(ctx context.Context, id int64) (RemoteSkillSyncJob, error) {
	return s.store.GetRemoteSkillSyncJob(ctx, id)
}

func (s *RemoteSkillRegistryService) installSnapshot(snapshot RemoteSkillRegistrySnapshot) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	current := s.snapshot.Load()
	if current != nil && current.Revision > snapshot.Revision {
		return
	}
	cloned := cloneRemoteSkillRegistrySnapshot(snapshot)
	if cloned.Active != nil {
		cloned.SourceID = cloned.Active.SourceID
		cloned.RemoteRoot = cloned.Active.RemoteRoot
	}
	s.snapshot.Store(&cloned)
}

func (s *RemoteSkillRegistryService) retainLastKnownGood(err error) error {
	s.markDegraded("reload_failed")
	return err
}

func (s *RemoteSkillRegistryService) markDegraded(reason string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if current := s.snapshot.Load(); current != nil {
		degraded := cloneRemoteSkillRegistrySnapshot(*current)
		degraded.Degraded = true
		degraded.DegradedReason = reason
		s.snapshot.Store(&degraded)
	}
}

func cloneRemoteSkillRegistrySnapshot(snapshot RemoteSkillRegistrySnapshot) RemoteSkillRegistrySnapshot {
	if snapshot.Active != nil {
		active := *snapshot.Active
		snapshot.Active = &active
	}
	return snapshot
}

func remoteSkillSyncErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "sync_timeout"
	case errors.Is(err, ErrBusinessSystemPromptBundleInvalid):
		return "bundle_invalid"
	case errors.Is(err, ErrBusinessSystemPromptBundleUnavailable):
		return "source_unavailable"
	default:
		return "sync_failed"
	}
}
