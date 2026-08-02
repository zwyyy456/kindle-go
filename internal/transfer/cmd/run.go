package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/transfer"
)

// Run dispatches the transfer control, storage, and R2 acceptance roles.
func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(stdout)
		return nil
	}
	switch args[0] {
	case "control":
		return runControl(args[1:], stdout, stderr)
	case "storage":
		return runStorage(args[1:], stdout, stderr)
	case "r2-probe":
		return runR2Probe(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown transfer command %q", args[0])
	}
}

func runControl(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("transfer control", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8790", "address for control server C")
	dbPath := fs.String("db", "transfer-control.db", "SQLite control database path")
	publicURL := fs.String("public-url", "", "public HTTPS URL of control server C")
	storageURL := fs.String("storage-url", "", "public HTTPS URL of storage server A")
	storageSecret := fs.String("storage-secret", "", "shared HMAC secret for storage server A")
	r2Endpoint := fs.String("r2-endpoint", "", "R2 S3 endpoint, for example https://ACCOUNT.r2.cloudflarestorage.com")
	r2Bucket := fs.String("r2-bucket", "", "R2 bucket name")
	r2AccessKey := fs.String("r2-access-key-id", "", "R2 access key ID")
	r2SecretKey := fs.String("r2-secret-access-key", "", "R2 secret access key")
	r2Checksum := fs.Bool("r2-checksum-sha256", false, "sign and send x-amz-checksum-sha256 after r2-probe passes")
	stun := fs.String("stun", "", "comma-separated STUN URLs; TURN URLs are rejected")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: transfer control [options]")
	}
	return transfer.RunControl(context.Background(), transfer.ControlConfig{
		Addr: *addr, DBPath: *dbPath, PublicURL: *publicURL, STUNURLs: splitList(*stun), Stdout: stdout,
		Storage: transfer.StoragePeerConfig{BaseURL: *storageURL, Secret: []byte(*storageSecret)},
		R2: transfer.R2Config{
			Endpoint: *r2Endpoint, Bucket: *r2Bucket, AccessKeyID: *r2AccessKey,
			SecretAccessKey: *r2SecretKey, ChecksumSHA256: *r2Checksum,
		},
	})
}

func runStorage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("transfer storage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8791", "address for storage server A")
	dir := fs.String("dir", "transfer-storage-data", "storage data directory")
	origin := fs.String("allowed-origin", "", "exact HTTPS origin of control server C")
	secret := fs.String("secret", "", "shared HMAC secret")
	maxDownloads := fs.Int("max-downloads", 3, "maximum concurrent GET/Range downloads")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: transfer storage [options]")
	}
	return transfer.RunStorage(context.Background(), transfer.StorageConfig{
		Addr: *addr, Dir: *dir, AllowedOrigin: *origin, Secret: []byte(*secret),
		MaxDownloads: *maxDownloads, Stdout: stdout,
	})
}

func runR2Probe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("transfer r2-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint := fs.String("endpoint", "", "R2 S3 endpoint")
	bucket := fs.String("bucket", "", "R2 bucket")
	accessKey := fs.String("access-key-id", "", "R2 access key ID")
	secretKey := fs.String("secret-access-key", "", "R2 secret access key")
	origin := fs.String("origin", "", "browser origin to verify against bucket CORS")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: transfer r2-probe [options]")
	}
	return transfer.ProbeR2(context.Background(), transfer.R2ProbeConfig{
		R2Config: transfer.R2Config{
			Endpoint: *endpoint, Bucket: *bucket, AccessKeyID: *accessKey, SecretAccessKey: *secretKey,
		},
		Origin: *origin, Stdout: stdout,
	})
}

func splitList(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "Transfer files with a six-digit pickup code.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  transfer control [options]")
	fmt.Fprintln(out, "  transfer storage [options]")
	fmt.Fprintln(out, "  transfer r2-probe [options]")
}
