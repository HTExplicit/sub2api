// Explicit one-time publisher setup. Private material is piped directly to the
// existing authenticated GitHub CLI; only public metadata is printed. Existing
// observed keys are never overwritten; incomplete metadata checks fail closed.
// Authorized first-time setup must not race another publisher initializer.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const publisherRepository = "HTExplicit/sub2api"
const publisherSecret = "SUB2API_PLUGIN_SIGNING_KEY"
const publisherMetadata = "backend/pkg/extensionapi/v1/publisher.json"

type publisher struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

func github(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "gh", args...)
	command.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	command.Stdout = &output
	// The command's raw error output is not a safe diagnostic for key setup.
	if err := command.Run(); err != nil {
		return nil, errors.New("GitHub publisher setup command failed; no secret value was logged")
	}
	return output.Bytes(), nil
}

func publisherSecretExists(ctx context.Context, query func(context.Context, []byte, ...string) ([]byte, error)) (bool, error) {
	exists, total := false, -1
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		endpoint := fmt.Sprintf("repos/%s/actions/secrets?per_page=100&page=%d", publisherRepository, page)
		raw, err := query(ctx, nil, "api", endpoint)
		if err != nil {
			return false, err
		}
		var inventory struct {
			TotalCount *int `json:"total_count"`
			Secrets    []struct {
				Name string `json:"name"`
			} `json:"secrets"`
		}
		if json.Unmarshal(raw, &inventory) != nil || inventory.TotalCount == nil ||
			*inventory.TotalCount < 0 || inventory.Secrets == nil || len(inventory.Secrets) > 100 {
			return false, errors.New("invalid GitHub secret metadata response")
		}
		if total < 0 {
			total = *inventory.TotalCount
		} else if total != *inventory.TotalCount {
			return false, errors.New("GitHub secret metadata changed during pagination")
		}
		for _, secret := range inventory.Secrets {
			name := strings.ToUpper(secret.Name)
			if name == "" || name != strings.TrimSpace(name) || seen[name] {
				return false, errors.New("invalid or repeated GitHub secret metadata")
			}
			seen[name] = true
			exists = exists || name == publisherSecret
		}
		if len(seen) == total {
			return exists, nil
		}
		if len(seen) > total || len(inventory.Secrets) < 100 {
			return false, errors.New("GitHub secret metadata inventory is incomplete")
		}
	}
	return false, errors.New("GitHub secret metadata pagination limit exceeded")
}

func run() error {
	initialize := flag.Bool("initialize", false, "initialize the publisher only when neither metadata nor secret exists")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	exists, err := publisherSecretExists(ctx, github)
	if err != nil {
		return err
	}
	metadata, readErr := os.ReadFile(publisherMetadata)
	if readErr == nil {
		var saved publisher
		if json.Unmarshal(metadata, &saved) != nil {
			return errors.New("invalid saved publisher metadata")
		}
		key, err := base64.StdEncoding.DecodeString(saved.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize || saved.KeyID != "codexrip-plugins-v1" {
			return errors.New("invalid saved publisher identity")
		}
		if !exists {
			return errors.New("saved public identity exists but its GitHub signing secret is missing")
		}
		return json.NewEncoder(os.Stdout).Encode(saved)
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if exists {
		return errors.New("signing secret already exists; recover its public metadata instead of replacing the key")
	}
	if !*initialize {
		return errors.New("publisher is not initialized; use -initialize for the authorized one-time setup")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	defer clear(private)
	encoded := []byte(base64.StdEncoding.EncodeToString(private))
	defer clear(encoded)
	if _, err = github(ctx, encoded, "secret", "set", publisherSecret, "--repo", publisherRepository); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(publisher{KeyID: "codexrip-plugins-v1", PublicKey: base64.StdEncoding.EncodeToString(public)})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
