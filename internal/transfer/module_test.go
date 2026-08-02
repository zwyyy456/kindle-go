package transfer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunControlValidatesConfigurationBeforeCreatingState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "control.db")
	err := RunControl(context.Background(), ControlConfig{
		DBPath:  dbPath,
		Storage: StoragePeerConfig{BaseURL: "https://storage.example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("RunControl error = %v", err)
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid configuration created database: %v", statErr)
	}
}

func TestRunControlRejectsTURNBeforeCreatingState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "control.db")
	err := RunControl(context.Background(), ControlConfig{DBPath: dbPath, STUNURLs: []string{"turn:relay.example.com"}})
	if err == nil || !strings.Contains(err.Error(), "TURN") {
		t.Fatalf("RunControl error = %v", err)
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid configuration created database: %v", statErr)
	}
}

func TestRunStorageValidatesOwnedConfiguration(t *testing.T) {
	err := RunStorage(context.Background(), StorageConfig{AllowedOrigin: "https://transfer.example.com"})
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("RunStorage error = %v", err)
	}
}
