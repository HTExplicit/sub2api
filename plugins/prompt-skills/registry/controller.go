package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"golang.org/x/text/unicode/norm"
)

const documentChunkBytes = 256 << 10

var seedCache struct {
	sync.Once
	manifest remoteSkillManifest
	files    map[string][]byte
	err      error
}

func seed() (remoteSkillManifest, map[string][]byte, error) {
	seedCache.Do(func() {
		seedCache.manifest, seedCache.files, seedCache.err = loadRemoteSkillSeedFiles()
		if seedCache.err == nil {
			seedCache.err = validateCurrentRemoteSkillTree(seedCache.files)
		}
	})
	return seedCache.manifest, seedCache.files, seedCache.err
}

func Ready() error { _, _, err := seed(); return err }

type Controller struct{}

func New() *Controller { return &Controller{} }

func (c *Controller) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	var output any = map[string]bool{"valid": true}
	var err error
	switch in.Operation {
	case "skills.prompt.seed":
		output = DefaultPrompt()
	case "skills.profile":
		output = extensionv1.SkillRegistryProfile{SourceID: RemoteSkillUpstreamSourceID, UpstreamRoot: RemoteSkillUpstreamRoot, PublicRoot: RemoteSkillPublicRoot, RequiredPaths: remoteSkillRequiredPaths, RequiredModules: remoteSkillRequiredModules, ScriptExtensions: scriptExtensions, BinaryExtensions: binaryExtensions}
	case "skills.seed.index":
		output, _, err = seed()
	case "skills.manifest.validate":
		var manifest remoteSkillManifest
		if err = json.Unmarshal(in.Payload, &manifest); err == nil {
			err = validateRemoteSkillManifest(manifest)
		}
	case "skills.seed.chunk":
		var request extensionv1.SkillSeedChunkRequest
		if err = json.Unmarshal(in.Payload, &request); err == nil {
			output, err = readSeedChunk(request)
		}
	case "skills.tree.check":
		var request extensionv1.SkillTreeCheck
		if err = json.Unmarshal(in.Payload, &request); err == nil {
			var current *remoteSkillManifest
			if request.Current {
				var manifest remoteSkillManifest
				manifest, _, err = seed()
				current = &manifest
			}
			if err == nil {
				err = validateTreeEntries(request.Entries, current)
			}
		}
	case "skills.file.plan":
		var request extensionv1.SkillFileInspection
		if err = json.Unmarshal(in.Payload, &request); err == nil {
			output = planFile(request)
		}
	default:
		return extensionv1.Result{}, errors.New("unsupported skill registry operation")
	}
	if err != nil {
		return extensionv1.Result{Code: "bundle_invalid", HTTPStatus: 400, Message: err.Error()}, nil
	}
	raw, err := json.Marshal(output)
	return extensionv1.Result{Payload: raw}, err
}

func readSeedChunk(request extensionv1.SkillSeedChunkRequest) (extensionv1.SkillSeedChunk, error) {
	_, files, err := seed()
	if err != nil {
		return extensionv1.SkillSeedChunk{}, err
	}
	body, exists := files[request.Path]
	if !exists || request.Offset < 0 || request.Offset > len(body) || request.Limit <= 0 || request.Limit > documentChunkBytes {
		return extensionv1.SkillSeedChunk{}, errors.New("invalid seed chunk request")
	}
	end := min(len(body), request.Offset+request.Limit)
	return extensionv1.SkillSeedChunk{Data: bytes.Clone(body[request.Offset:end]), TotalBytes: len(body)}, nil
}

var scriptExtensions = []string{".ps1", ".psm1", ".sh", ".bash", ".zsh", ".fish", ".py", ".rb", ".pl", ".lua", ".js", ".mjs", ".cjs", ".ts", ".bat", ".cmd"}
var binaryExtensions = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".jar", ".zip", ".gz", ".7z", ".exe", ".dll", ".so", ".pdf", ".docx"}

func planFile(in extensionv1.SkillFileInspection) extensionv1.SkillFilePlan {
	kind := "text"
	ext := strings.ToLower(path.Ext(in.Path))
	contains := func(values []string) bool {
		for _, value := range values {
			if value == ext {
				return true
			}
		}
		return false
	}
	if bytes.HasPrefix(in.Prefix, []byte("#!")) || contains(scriptExtensions) {
		kind = "script"
	} else if contains(binaryExtensions) || !in.UTF8 {
		kind = "binary"
	}
	plan := extensionv1.SkillFilePlan{Kind: kind}
	if kind == "text" {
		plan.ReplaceFrom, plan.ReplaceTo = RemoteSkillUpstreamRoot, RemoteSkillPublicRoot
	}
	return plan
}

func validateTreeEntries(entries []extensionv1.SkillTreeEntry, current *remoteSkillManifest) error {
	if len(entries) == 0 || len(entries) > remoteSkillMaxFileCount {
		return errors.New("paired tree file count invalid")
	}
	portable := map[string]string{}
	actual := map[string]extensionv1.SkillTreeEntry{}
	var total int64
	for _, entry := range entries {
		normalized, err := normalizeBundleRelativePath(entry.Path)
		if err != nil || normalized != entry.Path || !norm.NFC.IsNormalString(entry.Path) || entry.Bytes <= 0 || entry.Bytes > businessSystemPromptBundleMaxFileBytes || !entry.UTF8 || !validRemoteSkillSHA256(entry.SHA256) {
			return errors.New("paired tree path or body invalid")
		}
		key := portableRemoteSkillPathKey(entry.Path)
		if _, exists := portable[key]; exists {
			return errors.New("portable path collision")
		}
		portable[key], actual[entry.Path] = entry.Path, entry
		total += int64(entry.Bytes)
		if total > remoteSkillMaxTotalBytes {
			return errors.New("paired tree exceeds size limit")
		}
	}
	for _, name := range []string{"RULES.md", "README_AI.md", "SKILL.md"} {
		if _, exists := actual[name]; !exists {
			return errors.New("upstream entry file missing")
		}
	}
	if current == nil {
		return nil
	}
	if len(actual) != len(current.Files) {
		return errors.New("current tree file count mismatch")
	}
	for _, expected := range current.Files {
		entry, exists := actual[expected.Path]
		if !exists || expected.ByteLength != entry.Bytes || expected.SHA256 != entry.SHA256 {
			return errors.New("current tree does not match manifest")
		}
	}
	for _, name := range remoteSkillRequiredPaths {
		if _, exists := actual[name]; !exists {
			return errors.New("required remote skill file missing")
		}
	}
	for _, name := range remoteSkillRequiredModules {
		if _, exists := actual[path.Join("skills", name, "INSTRUCTIONS.md")]; !exists {
			return errors.New("required remote skill module missing")
		}
	}
	return nil
}
