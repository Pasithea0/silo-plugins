// Command verify checks the catalog the way a Silo host reads it.
//
// It rejects an entry the host would skip — a manifest the SDK does not
// validate, a capability list the host refuses, a binary whose checksum does
// not match, or a binary that reports a different plugin ID or version than
// the catalog claims. Run it after build_catalog.py and before publishing.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

type catalog struct {
	Plugins []struct {
		Manifest json.RawMessage   `json:"manifest"`
		RepoURL  string            `json:"repo_url"`
		Binaries map[string]binary `json:"binaries"`
		Tag      string            `json:"tag"`
	} `json:"plugins"`
}

type binary struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum"`
}

var problems int

func main() {
	path := "manifest.json"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fail("read %s: %v", path, err)
	}
	var index catalog
	if err := json.Unmarshal(data, &index); err != nil {
		fail("decode %s: %v", path, err)
	}
	if len(index.Plugins) == 0 {
		fail("%s lists no plugins", path)
	}

	for _, entry := range index.Plugins {
		if err := checkEntry(entry.Manifest, entry.Binaries, entry.Tag); err != nil {
			problems++
			fmt.Printf("FAIL %v\n", err)
			continue
		}
	}
	if problems > 0 {
		fail("%d problem(s)", problems)
	}
	fmt.Printf("%s is valid: %d plugin(s)\n", path, len(index.Plugins))
}

func checkEntry(raw json.RawMessage, binaries map[string]binary, tag string) error {
	manifest, err := publicmanifest.Load(raw)
	if err != nil {
		return fmt.Errorf("manifest does not load: %w", err)
	}
	if err := publicmanifest.Validate(manifest); err != nil {
		return fmt.Errorf("%s: manifest does not validate: %w", manifest.GetPluginId(), err)
	}
	id := manifest.GetPluginId()
	if id == "" || manifest.GetVersion() == "" {
		return fmt.Errorf("manifest has no plugin id or version")
	}
	if manifest.GetSiloApiVersion() != "v1" {
		return fmt.Errorf("%s: silo_api_version %q is not v1", id, manifest.GetSiloApiVersion())
	}
	if len(manifest.GetCapabilities()) == 0 {
		return fmt.Errorf("%s: no capabilities", id)
	}
	capabilities := map[string]bool{}
	for _, capability := range manifest.GetCapabilities() {
		key := capability.GetType() + ":" + capability.GetId()
		if capabilities[key] {
			return fmt.Errorf("%s: duplicate capability %s", id, key)
		}
		capabilities[key] = true
	}
	if tag != "" && strings.TrimPrefix(tag, "v") != manifest.GetVersion() {
		fmt.Printf("  warn %s: tag %s names version %s\n", id, tag, manifest.GetVersion())
	}

	supported := map[string]bool{}
	for _, platform := range manifest.GetSupportedPlatforms() {
		supported[platform.GetOs()+"/"+platform.GetArch()] = true
	}
	if len(binaries) == 0 {
		return fmt.Errorf("%s: no binaries", id)
	}

	local := runtime.GOOS + "/" + runtime.GOARCH
	for key, asset := range binaries {
		if !supported[key] {
			return fmt.Errorf("%s: binary %s is not a supported platform", id, key)
		}
		if !strings.HasPrefix(asset.URL, "https://") {
			return fmt.Errorf("%s: binary %s url is not https", id, key)
		}
		if len(asset.Checksum) != 64 || strings.ToLower(asset.Checksum) != asset.Checksum {
			return fmt.Errorf("%s: binary %s checksum is not a lowercase sha256: %q", id, key, asset.Checksum)
		}
		body, err := download(asset.URL)
		if err != nil {
			return fmt.Errorf("%s: download %s: %w", id, key, err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != asset.Checksum {
			return fmt.Errorf("%s: binary %s does not match its checksum", id, key)
		}
		if key == local {
			reported, err := binaryIdentity(body)
			if err != nil {
				return fmt.Errorf("%s: %s manifest: %w", id, key, err)
			}
			if reported.PluginID != id || reported.Version != manifest.GetVersion() {
				return fmt.Errorf("%s: binary reports %s %s", id, reported.PluginID, reported.Version)
			}
		}
	}
	for key := range supported {
		if _, ok := binaries[key]; !ok {
			fmt.Printf("  warn %s: no binary for supported platform %s\n", id, key)
		}
	}
	fmt.Printf("ok %s %s (%d platform(s))\n", id, manifest.GetVersion(), len(binaries))
	return nil
}

type identity struct {
	PluginID string `json:"plugin_id"`
	PluginId string `json:"pluginId"`
	Version  string `json:"version"`
}

func binaryIdentity(body []byte) (identity, error) {
	dir, err := os.MkdirTemp("", "verify")
	if err != nil {
		return identity{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "plugin")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		return identity{}, err
	}
	out, err := exec.Command(path, "manifest").Output()
	if err != nil {
		return identity{}, err
	}
	var got identity
	if err := json.Unmarshal(out, &got); err != nil {
		return identity{}, err
	}
	if got.PluginID == "" {
		got.PluginID = got.PluginId
	}
	return got, nil
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

var _ pluginv1.PluginManifest
