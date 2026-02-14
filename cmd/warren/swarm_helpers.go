package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"gopkg.in/yaml.v3"
)

// connectNATS connects to the NATS server using config from env, config file, or defaults.
func connectNATS() (*nats.Conn, error) {
	url := "nats://localhost:4222"
	token := ""

	// Try config file.
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".warren", "config.yaml"))
	if err == nil {
		var cfg struct {
			Hermes struct {
				URL   string `yaml:"url"`
				Token string `yaml:"token"`
			} `yaml:"hermes"`
		}
		if yaml.Unmarshal(data, &cfg) == nil {
			if cfg.Hermes.URL != "" {
				url = cfg.Hermes.URL
			}
			if cfg.Hermes.Token != "" {
				token = cfg.Hermes.Token
			}
		}
	}

	// Env overrides.
	if v := os.Getenv("NATS_URL"); v != "" {
		url = v
	}
	if v := os.Getenv("NATS_TOKEN"); v != "" {
		token = v
	}

	opts := []nats.Option{
		nats.Name("warren-cli"),
		nats.Timeout(5 * time.Second),
	}
	if token != "" {
		opts = append(opts, nats.Token(token))
	}

	return nats.Connect(url, opts...)
}

// resolveOwner attempts to resolve the current user's identity from SSH key → Alexandria.
func resolveOwner() (uuid string, name string) {
	fingerprint := getSSHFingerprint()
	if fingerprint == "" {
		return "", ""
	}

	// Query Alexandria for the person associated with this fingerprint.
	alexandriaURL := getAlexandriaURL()
	if alexandriaURL == "" {
		return "", ""
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Get devices to find matching fingerprint.
	resp, err := client.Get(alexandriaURL + "/api/v1/devices")
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var devicesResp struct {
		Data []struct {
			Identifier string                 `json:"identifier"`
			OwnerID    string                 `json:"owner_id"`
			Metadata   map[string]interface{} `json:"metadata"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&devicesResp) != nil {
		return "", ""
	}

	var ownerID string
	for _, dev := range devicesResp.Data {
		if fp, ok := dev.Metadata["ssh_fingerprint"].(string); ok && fp == fingerprint {
			ownerID = dev.OwnerID
			break
		}
	}
	if ownerID == "" {
		return "", ""
	}

	// Get person name.
	resp2, err := client.Get(alexandriaURL + "/api/v1/people")
	if err != nil {
		return ownerID, ""
	}
	defer resp2.Body.Close()

	var peopleResp struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if json.NewDecoder(resp2.Body).Decode(&peopleResp) != nil {
		return ownerID, ""
	}

	for _, p := range peopleResp.Data {
		if p.ID == ownerID {
			return ownerID, p.Name
		}
	}

	return ownerID, ""
}

// getSSHFingerprint reads the default SSH public key and computes its SHA256 fingerprint.
func getSSHFingerprint() string {
	home, _ := os.UserHomeDir()
	keyFiles := []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"}

	for _, name := range keyFiles {
		path := filepath.Join(home, ".ssh", name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		line := strings.TrimSpace(string(data))
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		keyBytes, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}

		hash := sha256.Sum256(keyBytes)
		return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
	}

	return ""
}

// getAlexandriaURL returns the Alexandria API URL from config or env.
func getAlexandriaURL() string {
	if v := os.Getenv("ALEXANDRIA_URL"); v != "" {
		return v
	}

	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".warren", "config.yaml"))
	if err != nil {
		return ""
	}

	var cfg struct {
		Alexandria struct {
			URL string `yaml:"url"`
		} `yaml:"alexandria"`
	}
	if yaml.Unmarshal(data, &cfg) == nil && cfg.Alexandria.URL != "" {
		return cfg.Alexandria.URL
	}

	return ""
}

// getGitBranch returns the current git branch name.
func getGitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getGitRoot returns the git repository root.
func getGitRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

