package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "storage" {
		return runStorage(args[1:])
	}
	if len(args) > 0 && args[0] == "r2-probe" {
		return runR2Probe(args[1:])
	}

	fs := flag.NewFlagSet("transferdemo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	addr := fs.String("addr", ":8790", "address for control server C")
	dbPath := fs.String("db", "transfer-control.db", "SQLite control database path")
	publicURL := fs.String("public-url", "", "public HTTPS URL of control server C")
	storageBaseURL := fs.String("storage-url", "", "public HTTPS URL of storage server A")
	storageSecret := fs.String("storage-secret", "", "shared HMAC secret for storage server A")
	r2Endpoint := fs.String("r2-endpoint", "", "R2 S3 endpoint, for example https://ACCOUNT.r2.cloudflarestorage.com")
	r2Bucket := fs.String("r2-bucket", "", "R2 bucket name")
	r2AccessKey := fs.String("r2-access-key-id", "", "R2 access key ID")
	r2SecretKey := fs.String("r2-secret-access-key", "", "R2 secret access key")
	r2ChecksumSHA256 := fs.Bool("r2-checksum-sha256", false, "sign and send x-amz-checksum-sha256 after r2-probe passes")
	stunURLs := fs.String("stun", "", "comma-separated STUN URLs; TURN URLs are rejected")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: go run ./tools/transferdemo [options]")
		fmt.Fprintln(os.Stderr, "       go run ./tools/transferdemo storage [options]")
		fmt.Fprintln(os.Stderr, "       go run ./tools/transferdemo r2-probe [options]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
	}

	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: go run ./tools/transferdemo [options]")
	}

	control, err := openSessionStore(*dbPath)
	if err != nil {
		return err
	}
	defer control.Close()
	if err := ensureDataPlaneIsNotControlPlane(*publicURL, *storageBaseURL); err != nil && *storageBaseURL != "" {
		return err
	}
	if (*storageBaseURL == "") != (*storageSecret == "") {
		return fmt.Errorf("-storage-url and -storage-secret must be configured together")
	}
	if *storageSecret != "" && len(*storageSecret) < 32 {
		return fmt.Errorf("-storage-secret must contain at least 32 bytes")
	}
	var r2Backend objectBackend
	if *r2Endpoint != "" || *r2Bucket != "" || *r2AccessKey != "" || *r2SecretKey != "" {
		backend, backendErr := newR2Backend(*r2Endpoint, *r2Bucket, *r2AccessKey, *r2SecretKey)
		err = backendErr
		if err != nil {
			return err
		}
		backend.checksumSHA256 = *r2ChecksumSHA256
		r2Backend = backend
		if err := ensureDataPlaneIsNotControlPlane(*publicURL, *r2Endpoint); err != nil {
			return err
		}
	}
	if *r2ChecksumSHA256 && r2Backend == nil {
		return fmt.Errorf("-r2-checksum-sha256 requires complete R2 configuration")
	}
	stun, err := parseSTUNURLs(*stunURLs)
	if err != nil {
		return err
	}
	srv := Server{
		Addr:            *addr,
		Stdout:          os.Stdout,
		control:         control,
		publicURL:       *publicURL,
		storageBaseURL:  *storageBaseURL,
		storageSecret:   []byte(*storageSecret),
		offlineLifetime: 2 * time.Hour,
		r2:              r2Backend,
		stunURLs:        stun,
	}
	return srv.Run()
}
