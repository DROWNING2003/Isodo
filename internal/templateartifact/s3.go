package templateartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/sync/singleflight"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type S3Config struct{ Endpoint, Region, Bucket, AccessKey, SecretKey string }
type S3Store struct {
	verifiedMu   sync.Mutex
	verified     map[string]*Artifact
	verification singleflight.Group
	client       *s3.Client
	transfers    *transfermanager.Client
	bucket       string
}

func NewS3Store(config S3Config) (*S3Store, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || config.Bucket == "" || config.AccessKey == "" || config.SecretKey == "" {
		return nil, errors.New("S3 endpoint, bucket and credentials are required")
	}
	if config.Region == "" {
		config.Region = "us-east-1"
	}
	client := s3.NewFromConfig(aws.Config{Region: config.Region, Credentials: credentials.NewStaticCredentialsProvider(config.AccessKey, config.SecretKey, ""), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}, func(o *s3.Options) { o.BaseEndpoint = aws.String(config.Endpoint); o.UsePathStyle = true })
	transfers := transfermanager.New(client, func(o *transfermanager.Options) {
		o.PartSizeBytes = 64 << 20
		o.MultipartUploadThreshold = 64 << 20
		o.Concurrency = 2
		o.FailTimeout = 10 * time.Second
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return &S3Store{client: client, transfers: transfers, bucket: config.Bucket}, nil
}
func artifactKey(digest, name string) string { return "templates/sha256/" + digest + "/" + name }

// Publish uploads validated files before the manifest commit marker. The caller
// must own an immutable source directory for the duration of publication.
func (s *S3Store) Publish(ctx context.Context, directory, digest string) error {
	m, err := Verify(directory, digest)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, entry := range m.Files() {
		f, err := openRegular(root, entry.Name)
		if err != nil {
			return err
		}
		uploadErr := s.putImmutable(ctx, digest, entry.Name, f, entry.Size, entry.SHA256)
		closeErr := f.Close()
		if err = errors.Join(uploadErr, closeErr); err != nil {
			return err
		}
	}
	f, err := openRegular(root, "manifest.json")
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return err
	}
	if _, err = ParseManifest(data, digest); err != nil {
		return err
	}
	return s.putImmutable(ctx, digest, "manifest.json", bytes.NewReader(data), int64(len(data)), digest)
}
func (s *S3Store) putImmutable(ctx context.Context, digest, name string, body io.Reader, size int64, hash string) error {
	_, headErr := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(artifactKey(digest, name))})
	if headErr == nil {
		return s.download(ctx, digest, File{Name: name, Size: size, SHA256: hash}, io.Discard)
	}
	if response, ok := errors.AsType[*smithyhttp.ResponseError](headErr); !ok || response.HTTPStatusCode() != http.StatusNotFound {
		return headErr
	}
	_, err := s.transfers.UploadObject(ctx, &transfermanager.UploadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(artifactKey(digest, name)), Body: body, ContentLength: aws.Int64(size), IfNoneMatch: aws.String("*")})
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && response.HTTPStatusCode() == http.StatusPreconditionFailed {
		// An interrupted retry may find an existing object; never trust ETag as SHA-256.
		return s.download(ctx, digest, File{Name: name, Size: size, SHA256: hash}, io.Discard)
	}
	return err
}
func (s *S3Store) download(ctx context.Context, digest string, entry File, destination io.Writer) error {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(artifactKey(digest, entry.Name))})
	if err != nil {
		return err
	}
	defer object.Body.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(object.Body, entry.Size+1))
	if err != nil {
		return err
	}
	if n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("S3 artifact %s size/hash mismatch", entry.Name)
	}
	return nil
}

// Fetch publishes a cache directory only after every file passes its manifest hash.
// A failed download is discarded; an existing cache directory is never replaced.
func (s *S3Store) Fetch(ctx context.Context, cacheRoot, digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", errors.New("invalid manifest digest")
	}
	destination := filepath.Join(cacheRoot, digest)
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() {
			return "", errors.New("cache directory must not be a symlink")
		}
		if _, err = Verify(destination, digest); err != nil {
			return "", err
		}
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(cacheRoot, 0700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(cacheRoot, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(artifactKey(digest, "manifest.json"))})
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(object.Body, (64<<10)+1))
	closeErr := object.Body.Close()
	if err = errors.Join(readErr, closeErr); err != nil {
		return "", err
	}
	m, err := ParseManifest(data, digest)
	if err != nil {
		return "", err
	}
	manifestFile, err := os.OpenFile(filepath.Join(staging, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return "", err
	}
	_, writeErr := manifestFile.Write(data)
	syncErr := manifestFile.Sync()
	closeErr = manifestFile.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	for _, entry := range m.Files() {
		f, err := os.OpenFile(filepath.Join(staging, entry.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
		if err != nil {
			return "", err
		}
		fetchErr := s.download(ctx, digest, entry, f)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err = errors.Join(fetchErr, syncErr, closeErr); err != nil {
			return "", err
		}
	}
	if _, err = Verify(staging, digest); err != nil {
		return "", err
	}
	if err = syncDirectory(staging); err != nil {
		return "", err
	}
	if err = os.Rename(staging, destination); err != nil {
		if _, verifyErr := Verify(destination, digest); verifyErr != nil {
			return "", errors.Join(err, verifyErr)
		}
	}
	if err = syncDirectory(cacheRoot); err != nil {
		return "", err
	}
	return destination, nil
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// FetchVerified keeps full-content verification off the hot create path. The
// receipt is not serialized and is invalidated by any observed cache mutation.
func (s *S3Store) FetchVerified(ctx context.Context, cacheRoot, digest string) (*Artifact, error) {
	if !digestPattern.MatchString(digest) {
		return nil, errors.New("invalid manifest digest")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(cacheRoot)
	if err != nil {
		return nil, err
	}
	key := filepath.Join(absolute, digest)
	s.verifiedMu.Lock()
	cached := s.verified[key]
	s.verifiedMu.Unlock()
	if cached != nil && cached.Directory() == key && cached.Digest() == digest && cached.Check() == nil {
		return cached, nil
	}
	value, err, _ := s.verification.Do(key, func() (any, error) {
		s.verifiedMu.Lock()
		cached := s.verified[key]
		s.verifiedMu.Unlock()
		if cached != nil && cached.Directory() == key && cached.Digest() == digest && cached.Check() == nil {
			return cached, nil
		}
		directory, err := s.Fetch(ctx, absolute, digest)
		if err != nil {
			return nil, err
		}
		artifact, err := VerifyArtifact(directory, digest)
		if err != nil {
			return nil, err
		}
		s.verifiedMu.Lock()
		if s.verified == nil {
			s.verified = make(map[string]*Artifact)
		}
		s.verified[key] = artifact
		s.verifiedMu.Unlock()
		return artifact, nil
	})
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return value.(*Artifact), nil
}
