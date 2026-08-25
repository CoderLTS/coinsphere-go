package service

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteWorkflowArtifactCompressesByContentHash(t *testing.T) {
	root := t.TempDir()
	content := `{"rows":[1,2,3]}`
	digest, key, size, storedSize, err := writeWorkflowArtifact(context.Background(), root, strings.NewReader(content))
	if err != nil || !validWorkflowArtifactDigest(digest) || size != int64(len(content)) || storedSize <= 0 || key != workflowArtifactKey(digest) {
		t.Fatalf("artifact digest=%q key=%q size=%d stored=%d err=%v", digest, key, size, storedSize, err)
	}
	secondDigest, secondKey, _, secondStoredSize, err := writeWorkflowArtifact(context.Background(), root, strings.NewReader(content))
	if err != nil || secondDigest != digest || secondKey != key || secondStoredSize != storedSize {
		t.Fatalf("duplicate artifact digest=%q key=%q stored=%d err=%v", secondDigest, secondKey, secondStoredSize, err)
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := errors.Join(reader.Close(), file.Close())
	if readErr != nil || closeErr != nil || string(decoded) != content {
		t.Fatalf("decoded=%q read=%v close=%v", decoded, readErr, closeErr)
	}
	artifactPath := filepath.Join(root, filepath.FromSlash(key))
	file, err = os.OpenFile(artifactPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, _, existingStoredSize, err := writeWorkflowArtifact(context.Background(), root, strings.NewReader(content))
	if err != nil || existingStoredSize != storedSize+1 {
		t.Fatalf("existing artifact stored=%d err=%v", existingStoredSize, err)
	}
}
