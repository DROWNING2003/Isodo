package templateartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestSeaweedFSArtifactRoundTrip(t *testing.T) {
	endpoint := os.Getenv("ISODO_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires isolated SeaweedFS test endpoint")
	}
	config := S3Config{Endpoint: endpoint, Bucket: os.Getenv("ISODO_TEMPLATE_S3_BUCKET"), AccessKey: os.Getenv("SEAWEEDFS_ACCESS_KEY"), SecretKey: os.Getenv("SEAWEEDFS_SECRET_KEY")}
	writer, err := NewS3Store(config)
	if err != nil {
		t.Fatal(err)
	}
	directory, m := fixture(t)
	m.Version = fmt.Sprint(time.Now().UnixNano())
	// Cross the configured 64 MiB multipart threshold with a sparse integrity fixture.
	rootfs := filepath.Join(directory, m.RootFS.Name)
	if err = os.Truncate(rootfs, 68<<20); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(rootfs)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	m.RootFS.Size, err = io.Copy(hash, file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	m.RootFS.SHA256 = hex.EncodeToString(hash.Sum(nil))

	digest := saveManifest(t, directory, m)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range []string{m.Kernel.Name, m.RootFS.Name, "manifest.json"} {
			if _, err := writer.client.DeleteObject(cleanup, &s3.DeleteObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(artifactKey(digest, name))}); err != nil {
				t.Error(err)
			}
		}
	})
	if err = writer.Publish(t.Context(), directory, digest); err != nil {
		t.Fatal(err)
	}
	if err = writer.Publish(t.Context(), directory, digest); err != nil {
		t.Fatalf("immutable retry failed: %v", err)
	}
	anonymous, err := http.Get(endpoint + "/" + config.Bucket + "/" + artifactKey(digest, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous artifact GET status: %d", anonymous.StatusCode)
	}

	if os.Getenv("ISODO_TEST_S3_RECREATE") == "1" {
		command := exec.CommandContext(t.Context(), "docker", "compose", "-f", "../../compose.yaml", "up", "-d", "--force-recreate", "--no-deps", "--pull", "never", "seaweedfs")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("recreate storage: %v %s", err, output)
		}
		client := http.Client{Timeout: 2 * time.Second}
		deadline := time.Now().Add(45 * time.Second)
		for {
			response, err := client.Get(endpoint + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("S3 did not recover after recreation")
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	config.AccessKey = os.Getenv("SEAWEEDFS_READ_ACCESS_KEY")
	config.SecretKey = os.Getenv("SEAWEEDFS_READ_SECRET_KEY")
	reader, err := NewS3Store(config)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := reader.Fetch(t.Context(), t.TempDir(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(cached, digest); err != nil {
		t.Fatal(err)
	}
	m.Version += "-read-only"
	deniedDigest := saveManifest(t, directory, m)
	if err = reader.Publish(t.Context(), directory, deniedDigest); err == nil {
		t.Fatal("read-only Node credentials allowed writing")
	}
	// A corrupted object must not become a verified cache entry.
	_, err = writer.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(artifactKey(digest, m.RootFS.Name)), Body: strings.NewReader("corrupt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Fetch(t.Context(), t.TempDir(), digest); err == nil {
		t.Fatal("corrupt S3 object accepted")
	}
}
