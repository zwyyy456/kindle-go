package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// StoragePeerConfig configures the independently deployed offline storage data plane.
type StoragePeerConfig struct {
	BaseURL string
	Secret  []byte
}

// R2Config configures the Cloudflare R2 offline storage adapter.
type R2Config struct {
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	ChecksumSHA256  bool
}

// ControlConfig configures six-digit sessions, WebRTC signaling, and optional offline transports.
type ControlConfig struct {
	Addr      string
	DBPath    string
	PublicURL string
	STUNURLs  []string
	Storage   StoragePeerConfig
	R2        R2Config
	Stdout    io.Writer
}

// RunControl runs the transfer control plane until the context is canceled or the listener fails.
func RunControl(ctx context.Context, config ControlConfig) error {
	if config.DBPath == "" {
		config.DBPath = "transfer-control.db"
	}
	if (config.Storage.BaseURL == "") != (len(config.Storage.Secret) == 0) {
		return errors.New("storage URL and secret must be configured together")
	}
	if len(config.Storage.Secret) > 0 && len(config.Storage.Secret) < 32 {
		return errors.New("storage secret must contain at least 32 bytes")
	}
	if config.Storage.BaseURL != "" {
		if err := ensureDataPlaneIsNotControlPlane(config.PublicURL, config.Storage.BaseURL); err != nil {
			return err
		}
	}

	var r2Backend objectBackend
	if config.R2.configured() {
		backend, err := newR2Backend(config.R2.Endpoint, config.R2.Bucket, config.R2.AccessKeyID, config.R2.SecretAccessKey)
		if err != nil {
			return err
		}
		backend.checksumSHA256 = config.R2.ChecksumSHA256
		r2Backend = backend
		if err := ensureDataPlaneIsNotControlPlane(config.PublicURL, config.R2.Endpoint); err != nil {
			return err
		}
	} else if config.R2.ChecksumSHA256 {
		return errors.New("R2 checksum requires complete R2 configuration")
	}
	stun, err := validateSTUNURLs(config.STUNURLs)
	if err != nil {
		return err
	}
	control, err := openSessionStore(config.DBPath)
	if err != nil {
		return err
	}
	defer control.Close()

	server := controlServer{
		Addr: config.Addr, Stdout: config.Stdout, control: control, publicURL: config.PublicURL,
		storageBaseURL: config.Storage.BaseURL, storageSecret: append([]byte(nil), config.Storage.Secret...),
		offlineLifetime: 2 * time.Hour, r2: r2Backend, stunURLs: stun,
	}
	return server.RunContext(ctx)
}

func (config R2Config) configured() bool {
	return strings.TrimSpace(config.Endpoint) != "" || strings.TrimSpace(config.Bucket) != "" ||
		strings.TrimSpace(config.AccessKeyID) != "" || strings.TrimSpace(config.SecretAccessKey) != ""
}

func validateSTUNURLs(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "turn:") || strings.HasPrefix(lower, "turns:") {
			return nil, errors.New("TURN is intentionally unsupported")
		}
		if !strings.HasPrefix(lower, "stun:") && !strings.HasPrefix(lower, "stuns:") {
			return nil, fmt.Errorf("only STUN URLs are allowed: %q", value)
		}
		out = append(out, value)
	}
	return out, nil
}
