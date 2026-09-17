package embedbin

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"testing/fstest"
)

// gzipped builds an in-memory asset filesystem holding one compressed payload,
// which is the shape //go:embed hands the real callers.
func gzipped(t *testing.T, payload []byte) fs.FS {
	t.Helper()

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return fstest.MapFS{"assets/tool.gz": &fstest.MapFile{Data: buf.Bytes()}}
}

func TestUnpackWritesAnExecutable(t *testing.T) {
	payload := []byte("#!/bin/sh\necho hello\n")
	dir := t.TempDir()

	path, err := UnpackInto(dir, gzipped(t, payload), "assets/tool.gz", "tool")
	if err != nil {
		t.Fatalf("UnpackInto: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("contents = %q, want %q", got, payload)
	}

	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, want a file under %s", path, dir)
	}

	// The mode only means anything where it is enforced.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("mode = %v, want the executable bit set", info.Mode().Perm())
		}
	}
}

// A second call must reuse the file rather than rewrite it: every run of the app
// unpacks, and rewriting a binary that another instance may be executing is how
// this breaks on Windows.
func TestUnpackIsIdempotent(t *testing.T) {
	assets := gzipped(t, []byte("payload"))
	dir := t.TempDir()

	first, err := UnpackInto(dir, assets, "assets/tool.gz", "tool")
	if err != nil {
		t.Fatalf("first UnpackInto: %v", err)
	}
	before, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	second, err := UnpackInto(dir, assets, "assets/tool.gz", "tool")
	if err != nil {
		t.Fatalf("second UnpackInto: %v", err)
	}
	if second != first {
		t.Errorf("second path = %s, want %s", second, first)
	}

	after, err := os.Stat(second)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the binary was rewritten; an unchanged payload should be left alone")
	}

	// Nothing may be left behind for a later run to trip over.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want only the binary", len(entries))
	}
}

// Different contents must land on different names, so an app update installs its
// own copy instead of reusing the previous release's.
func TestUnpackNamesByContents(t *testing.T) {
	dir := t.TempDir()

	old, err := UnpackInto(dir, gzipped(t, []byte("version one")), "assets/tool.gz", "tool")
	if err != nil {
		t.Fatalf("UnpackInto: %v", err)
	}
	updated, err := UnpackInto(dir, gzipped(t, []byte("version two")), "assets/tool.gz", "tool")
	if err != nil {
		t.Fatalf("UnpackInto: %v", err)
	}

	if old == updated {
		t.Fatalf("both versions unpacked to %s", old)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("the previous binary went away: %v", err)
	}
}

func TestUnpackConcurrently(t *testing.T) {
	assets := gzipped(t, []byte("payload"))
	dir := t.TempDir()

	const workers = 8
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		paths []string
		errs  []error
	)

	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			path, err := UnpackInto(dir, assets, "assets/tool.gz", "tool")

			mu.Lock()
			defer mu.Unlock()
			paths = append(paths, path)
			errs = append(errs, err)
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("UnpackInto: %v", err)
		}
	}
	for _, path := range paths {
		if path != paths[0] {
			t.Fatalf("got %s and %s, want one path", path, paths[0])
		}
	}
}

func TestUnpackRejectsDamagedAssets(t *testing.T) {
	dir := t.TempDir()

	tests := map[string]fs.FS{
		"missing":   fstest.MapFS{},
		"not gzip":  fstest.MapFS{"assets/tool.gz": &fstest.MapFile{Data: []byte("plain text")}},
		"truncated": truncatedGzip(t),
	}

	for name, assets := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := UnpackInto(dir, assets, "assets/tool.gz", "tool"); err == nil {
				t.Fatal("UnpackInto succeeded, want an error")
			}
		})
	}

	// A failed unpack must not leave a usable-looking binary behind.
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("directory holds %d files after failures, want none", len(entries))
	}
}

func truncatedGzip(t *testing.T) fs.FS {
	t.Helper()

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(bytes.Repeat([]byte("payload"), 128)); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	data := buf.Bytes()
	return fstest.MapFS{"assets/tool.gz": &fstest.MapFile{Data: data[:len(data)-8]}}
}
