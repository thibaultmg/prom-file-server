package filewatch_test

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thibaultmg/prom-file-server/internal/filewatch"
)

func TestFileWatchError(t *testing.T) {
	// invalid file path
	_, err := filewatch.Watch(context.Background(), "invalid")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestFileWatch(t *testing.T) {
	testCases := map[string]struct {
		setup                    func(t *testing.T) (string, string)
		scenario                 func(t *testing.T, filePath string)
		expectedEventsCount      int
		stopWatcherAfterScenario bool
	}{
		"no change does not trigger event": {
			setup:                    createMetricsFile,
			scenario:                 func(t *testing.T, filePath string) {},
			expectedEventsCount:      0,
			stopWatcherAfterScenario: true,
		},
		"file update triggers event": {
			setup: createMetricsFile,
			scenario: func(t *testing.T, filePath string) {
				err := ioutil.WriteFile(filePath, []byte("test"), 0644)
				if err != nil {
					t.Fatalf("failed to write to file: %v", err)
				}
			},
			expectedEventsCount:      2, // 1 for chmod, 1 for write
			stopWatcherAfterScenario: true,
		},
		"file delete stops watcher": {
			setup: createMetricsFile,
			scenario: func(t *testing.T, filePath string) {
				if err := os.Remove(filePath); err != nil {
					t.Fatalf("failed to remove file: %v", err)
				}
			},
			expectedEventsCount:      0, // watcher should be stopped
			stopWatcherAfterScenario: false,
		},
		"file rename stops watcher": {
			setup: createMetricsFile,
			scenario: func(t *testing.T, filePath string) {
				if err := os.Rename(filePath, filepath.Join(filepath.Dir(filePath), "new_name")); err != nil {
					t.Fatalf("failed to remove dir: %v", err)
				}
			},
			expectedEventsCount:      0, // watcher should be stopped
			stopWatcherAfterScenario: false,
		},
		"file symlink target change stops watcher": {
			setup:                    createMetricsFileWithSymlinks,
			scenario:                 updateMetricsFileWithSymlinks,
			expectedEventsCount:      0, // watcher should be stopped
			stopWatcherAfterScenario: false,
		},
	}

	t.Parallel()
	for name, tc := range testCases {
		tc := tc

		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			filepath, dir := tc.setup(t)
			defer os.RemoveAll(dir)

			c, err := filewatch.Watch(ctx, filepath)
			if err != nil {
				t.Fatalf("failed to watch file: %v", err)
			}

			cancelWithDelay := func() {
				time.Sleep(1 * time.Second)
				cancel()
			}

			// Check the number of events
			var wg sync.WaitGroup
			eventsCount := 0
			wg.Add(1)
			go func() {
				for range c {
					eventsCount++
				}
				wg.Done()
			}()

			// Run the scenario
			tc.scenario(t, filepath)
			if tc.stopWatcherAfterScenario {
				go cancelWithDelay() // The delay let the watcher process all the events
			}

			wg.Wait()

			// Check the number of events
			if eventsCount != tc.expectedEventsCount {
				t.Fatalf("expected %d events, got %d", tc.expectedEventsCount, eventsCount)
			}
		})
	}
}

func createMetricsFile(t *testing.T) (string, string) {
	dir, err := os.MkdirTemp(os.TempDir(), "filewatch")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create a metrics file
	filePath := filepath.Join(dir, "metrics.txt")
	if err := ioutil.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write to file: %v", err)
	}

	return filePath, dir
}

// createMetricsFileWithSymlinks creates a metrics file with symlinks as it
// happens when watching a configmap mounted in a pod in kubernetes.
func createMetricsFileWithSymlinks(t *testing.T) (string, string) {
	// Create a temp directory
	dir, err := os.MkdirTemp(os.TempDir(), "filewatch")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// create a directory inside the temp directory and create a symlink to it
	// inside the temp directory
	subDir := filepath.Join(dir, "datav1")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	subDirSymlink := filepath.Join(dir, "data_symlink")
	if err := os.Symlink(subDir, subDirSymlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Create a metrics file inside the sub directory
	// and create a symlink to it inside the sub directory
	filePath := filepath.Join(subDir, "metrics.txt")
	if err := ioutil.WriteFile(filePath, []byte("testv1"), 0644); err != nil {
		t.Fatalf("failed to write to file: %v", err)
	}

	filePathSymlink := filepath.Join(dir, "metrics.txt")
	if err := os.Symlink(filepath.Join(subDirSymlink, "metrics.txt"), filePathSymlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	return filePathSymlink, dir
}

// updateMetricsFileWithSymlinks updates the metrics file with symlinks as it
// happens when updating a configmap mounted as a volume in a pod in kubernetes.
func updateMetricsFileWithSymlinks(t *testing.T, filePath string) {
	dir := filepath.Dir(filePath)

	subDir := filepath.Join(dir, "datav2")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	newFilePath := filepath.Join(subDir, "metrics.txt")
	if err := ioutil.WriteFile(newFilePath, []byte("testv2"), 0644); err != nil {
		t.Fatalf("failed to write to file: %v", err)
	}

	//tempLink := symlinkPath + ".tmp"
	symlinkPath := filepath.Join(dir, "data_symlink")
	tempLink := symlinkPath + ".tmp"

	// Create a temporary symlink
	if err := os.Symlink(subDir, tempLink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Atomically replace the existing symlink with the new one
	if err := os.Rename(tempLink, symlinkPath); err != nil {
		// Cleanup temp symlink in case of failure
		os.Remove(tempLink)
		t.Fatalf("failed to rename symlink: %v", err)
	}
}
