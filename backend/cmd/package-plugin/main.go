// package-plugin creates an immutable signed package from a first-party module.
// Publisher private keys are read only by this build command, never embedded.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type packageOptions struct {
	domain, sourceRevision                                               string
	defaultEnabled                                                       bool
	source, binary, platform, hostVersion, output, keyFile, keyID, sdkUI string
	bundleLock, migration                                                string
}

func main() {
	var options packageOptions
	flag.StringVar(&options.source, "source", "", "plugin module directory")
	flag.StringVar(&options.domain, "plugin-domain", "", "select one domain from the bundle source")
	flag.StringVar(&options.sourceRevision, "source-revision", "", "immutable source commit included in the signed manifest")
	flag.StringVar(&options.binary, "binary", "", "compiled plugin program")
	flag.StringVar(&options.platform, "platform", "linux-amd64", "runtime platform")
	flag.StringVar(&options.hostVersion, "tested-host-version", "", "exact host version whose contract tests passed")
	flag.StringVar(&options.output, "output", "", "new .s2plugin archive path")
	flag.StringVar(&options.keyFile, "signing-key-file", "", "base64 Ed25519 private key file (or use SUB2API_PLUGIN_SIGNING_KEY)")
	flag.StringVar(&options.keyID, "key-id", "codexrip-plugins-v1", "publisher key identifier")
	flag.StringVar(&options.sdkUI, "ui-sdk", "pkg/extensionapi/ui", "public UI SDK directory")
	flag.StringVar(&options.bundleLock, "bundle-lock", "", "write an immutable first-party bundle lock beside the package")
	flag.StringVar(&options.migration, "migration", "", "one-time migration profile recorded in the bundle lock")
	flag.BoolVar(&options.defaultEnabled, "default-enabled", false, "initial activation when no saved plugin state exists")
	generate := flag.String("generate-key", "", "create a new private key file; prints only its public key")
	bundleSourcePath := flag.String("bundle-source", "", "first-party source inventory to package")
	binaryDirectory := flag.String("binary-dir", "", "prebuilt module binaries for the bundle")
	buildBinaries := flag.Bool("build-binaries", false, "build selected binaries without loading any signing key")
	verifyBundle := flag.String("verify-bundle", "", "verify packages against the fixed publisher without executing them")
	development := flag.Bool("development", false, "allow a synthetic publisher only for development bundle verification")
	flag.Parse()
	var err error
	if *buildBinaries {
		err = buildBundleBinaries(*bundleSourcePath, *binaryDirectory, options)
	} else if *verifyBundle != "" {
		err = verifyPluginBundle(*verifyBundle, options.hostVersion, options.platform, *development)
	} else if *generate != "" {
		err = generateKey(*generate)
	} else if *bundleSourcePath != "" {
		err = packageBundle(*bundleSourcePath, *binaryDirectory, options)
	} else {
		err = packagePlugin(options)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generateKey(path string) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("could not generate publisher key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(base64.StdEncoding.EncodeToString(private) + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"public_key": base64.StdEncoding.EncodeToString(public), "private_key_file": path})
}

func packagePlugin(options packageOptions) error {
	if options.source == "" || options.binary == "" || options.output == "" || options.keyID == "" {
		return errors.New("source, binary, output and key-id are required")
	}
	raw, err := os.ReadFile(filepath.Join(options.source, "manifest.source.json"))
	if err != nil {
		return err
	}
	var manifest service.PluginManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if options.sourceRevision != "" {
		decoded, err := hex.DecodeString(options.sourceRevision)
		if err != nil || len(decoded) != 20 {
			return errors.New("source-revision must be a complete commit SHA")
		}
		manifest.SourceRevision = options.sourceRevision
	}
	assets := make(map[string][]byte)
	if err := filepath.WalkDir(filepath.Join(options.source, "ui"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("plugin assets cannot be symbolic links")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("plugin assets must be regular files")
		}
		relative, err := filepath.Rel(options.source, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assets[filepath.ToSlash(relative)] = content
		return nil
	}); err != nil {
		return err
	}
	bridge, err := os.ReadFile(filepath.Join(options.sdkUI, "bridge.js"))
	if err != nil {
		return err
	}
	assets["ui/assets/bridge.js"] = bridge
	binary, err := os.ReadFile(options.binary)
	if err != nil {
		return err
	}
	binaryName := "plugin"
	if strings.HasPrefix(options.platform, "windows-") {
		binaryName += ".exe"
	}
	binaryPath := "runtimes/" + options.platform + "/" + binaryName
	assets[binaryPath] = binary
	manifest.Runtimes = map[string]service.PluginRuntime{options.platform: {Path: binaryPath}}
	manifest.Files = make(map[string]string, len(assets))
	for name, content := range assets {
		digest := sha256.Sum256(content)
		manifest.Files[name] = hex.EncodeToString(digest[:])
	}
	if options.hostVersion != "" {
		manifest.Requires.TestedSub2APIVersions = []string{options.hostVersion}
		manifest.Requires.RecommendedSub2APIVersion = options.hostVersion
	}
	if err := manifest.ValidateForRuntime(options.platform); err != nil {
		return err
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encodedKey := strings.TrimSpace(os.Getenv("SUB2API_PLUGIN_SIGNING_KEY"))
	if options.keyFile != "" {
		if encodedKey != "" {
			return errors.New("choose a signing-key file or environment value, not both")
		}
		value, err := os.ReadFile(options.keyFile)
		if err != nil {
			return err
		}
		encodedKey = strings.TrimSpace(string(value))
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("a valid Ed25519 private signing key is required")
	}
	private := ed25519.PrivateKey(key)
	signature := service.PluginSignature{Algorithm: "ed25519", KeyID: options.keyID, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifestRaw))}
	signatureRaw, err := json.Marshal(signature)
	if err != nil {
		return err
	}
	assets["manifest.json"] = manifestRaw
	assets["signature.json"] = signatureRaw
	if err := os.MkdirAll(filepath.Dir(options.output), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(options.output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0644)
		if name == binaryPath {
			header.SetMode(0755)
		}
		writer, writeErr := archive.CreateHeader(header)
		if writeErr == nil {
			_, writeErr = writer.Write(assets[name])
		}
		if writeErr != nil {
			_ = archive.Close()
			_ = file.Close()
			return writeErr
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	content, err := os.ReadFile(options.output)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(content)
	packageDigest := hex.EncodeToString(digest[:])
	publicKey := base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
	if options.bundleLock != "" {
		if strings.TrimSpace(options.hostVersion) == "" {
			return errors.New("tested-host-version is required when writing a bundle lock")
		}
		if filepath.Dir(options.bundleLock) != filepath.Dir(options.output) {
			return errors.New("bundle lock and plugin package must share a directory")
		}
		bundle := service.PluginBundle{
			SchemaVersion:      1,
			HostVersion:        options.hostVersion,
			PublisherKeyID:     options.keyID,
			PublisherPublicKey: publicKey,
			Plugins: []service.BundledPlugin{{
				ID: manifest.ID, Version: manifest.Version, File: filepath.Base(options.output),
				SHA256: packageDigest, Migration: options.migration, DefaultEnabled: options.defaultEnabled,
			}},
		}
		if previous, readErr := os.ReadFile(options.bundleLock); readErr == nil {
			var existing service.PluginBundle
			if json.Unmarshal(previous, &existing) != nil || existing.SchemaVersion != bundle.SchemaVersion || existing.HostVersion != bundle.HostVersion || existing.PublisherKeyID != bundle.PublisherKeyID || existing.PublisherPublicKey != bundle.PublisherPublicKey {
				return errors.New("existing bundle identity does not match this build")
			}
			for _, entry := range existing.Plugins {
				if entry.ID == manifest.ID {
					return errors.New("plugin already exists in bundle lock")
				}
			}
			bundle.Plugins = append(existing.Plugins, bundle.Plugins...)
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		lock, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return err
		}
		lock = append(lock, '\n')
		if err := os.WriteFile(options.bundleLock, lock, 0600); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"plugin_id": manifest.ID, "version": manifest.Version, "sha256": packageDigest, "public_key": publicKey, "path": options.output})
}
