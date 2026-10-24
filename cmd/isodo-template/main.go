package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/postgres"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: isodo-template verify|probe|probe-bound|probe-node|probe-warm|build-warm|probe-artifact|publish-system --dir DIRECTORY --digest SHA256 [--vmm-binary PATH]")
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	directory := flags.String("dir", "", "candidate artifact directory")
	digest := flags.String("digest", "", "expected manifest SHA-256")
	binary := flags.String("vmm-binary", "cloud-hypervisor", "Cloud Hypervisor executable")
	outputDirectory := flags.String("out-dir", "", "new immutable warm artifact directory")
	cache := flags.String("cache-dir", "", "download artifact with Node read-only S3 credentials before probe-bound")
	name := flags.String("name", "linux-base", "system catalog name")
	description := flags.String("description", "Linux workspace with Isodo Guest Agent", "system template description")
	restoreMode := flags.String("restore-mode", "copyonwrite", "probe-warm memory mode: copy, ondemand, copyonwrite")
	templateID := flags.String("template-id", "", "published template ID for probe-node")
	buildID := flags.String("build-id", "", "published build ID for probe-node")
	expected := flags.String("expected-build-id", "", "current catalog build ID required for replacement")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch os.Args[1] {
	case "verify":
		m, err := templateartifact.Verify(*directory, *digest)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"integrity_verified": true, "manifest_digest": *digest, "files": len(m.Files())})
	case "probe":
		result, err := templatebuilder.Probe(ctx, *binary, *directory, *digest)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "probe-bound":
		probeCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
		defer stop()
		artifact := *directory
		if *cache != "" {
			storage, err := templateartifact.NewS3Store(templateartifact.S3Config{
				Endpoint: os.Getenv("ISODO_TEMPLATE_S3_ENDPOINT"), Region: os.Getenv("ISODO_TEMPLATE_S3_REGION"), Bucket: os.Getenv("ISODO_TEMPLATE_S3_BUCKET"),
				AccessKey: os.Getenv("SEAWEEDFS_READ_ACCESS_KEY"), SecretKey: os.Getenv("SEAWEEDFS_READ_SECRET_KEY"),
			})
			if err != nil {
				return err
			}
			artifact, err = storage.Fetch(probeCtx, *cache, *digest)
			if err != nil {
				return err
			}
		}
		result, err := probeBound(probeCtx, *binary, artifact, *digest)
		if err != nil {
			return err
		}
		result.StorageVerified = *cache != ""
		return json.NewEncoder(os.Stdout).Encode(result)
	case "probe-node":
		probeCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
		defer stop()
		result, err := probeNode(probeCtx, *binary, *cache, *digest, *templateID, *buildID)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "probe-warm":
		modes := map[string]vmm.MemoryRestoreMode{"copy": vmm.MemoryRestoreCopy, "ondemand": vmm.MemoryRestoreOnDemand, "copyonwrite": vmm.MemoryRestoreCopyOnWrite}
		mode, ok := modes[*restoreMode]
		if !ok {
			return errors.New("unsupported restore-mode")
		}
		probeCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
		defer stop()
		result, err := templatebuilder.ProbeWarm(probeCtx, *binary, *directory, *digest, mode)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "build-warm":
		if *outputDirectory == "" {
			return errors.New("out-dir is required")
		}
		buildCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
		defer stop()
		digest, err := templatebuilder.BuildWarmArtifact(buildCtx, *binary, *directory, *digest, *outputDirectory)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"directory": *outputDirectory, "manifest_digest": digest})
	case "probe-artifact":
		probeCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
		defer stop()
		result, err := probeArtifact(probeCtx, *binary, *directory, *digest)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "publish-system":
		// Credentials remain in the operator environment; never command arguments.
		if os.Getenv("ISODO_DATABASE_URL") == "" {
			return errors.New("ISODO_DATABASE_URL is required")
		}
		store, err := postgres.New(ctx, os.Getenv("ISODO_DATABASE_URL"))
		if err != nil {
			return err
		}
		defer store.Close()
		bucket := os.Getenv("ISODO_TEMPLATE_S3_BUCKET")
		storage, err := templateartifact.NewS3Store(templateartifact.S3Config{
			Endpoint: os.Getenv("ISODO_TEMPLATE_S3_ENDPOINT"), Bucket: bucket,
			Region:    os.Getenv("ISODO_TEMPLATE_S3_REGION"),
			AccessKey: os.Getenv("SEAWEEDFS_ACCESS_KEY"), SecretKey: os.Getenv("SEAWEEDFS_SECRET_KEY"),
		})
		if err != nil {
			return err
		}
		publishCtx, stop := context.WithTimeout(ctx, 30*time.Minute)
		defer stop()
		manifest, err := templateartifact.Verify(*directory, *digest)
		if err != nil {
			return err
		}
		var probe any
		if manifest.RuntimeSnapshot != nil {
			probe, err = probeArtifact(publishCtx, *binary, *directory, *digest)
		} else {
			probe, err = templatebuilder.Probe(publishCtx, *binary, *directory, *digest)
		}
		if err != nil {
			return fmt.Errorf("system template probe: %w", err)
		}
		if err = storage.Publish(publishCtx, *directory, *digest); err != nil {
			return fmt.Errorf("publish system artifact: %w", err)
		}
		publication, err := store.RegisterSystemTemplate(publishCtx, *name, *description, *digest, bucket, *expected)
		if err != nil {
			return fmt.Errorf("register system template: %w", err)
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			postgres.SystemPublication
			Probe any `json:"probe"`
		}{publication, probe})
	default:
		return errors.New("unknown template command")
	}
}
