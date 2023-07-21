package filewatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch starts watching the file for changes
// It returns a channel that will:
//   - be closed when the context is cancelled
//   - receive a message when the file is changed and must be read again
//   - be closed when the file is deleted, renamed or a symlink is changed.
//     In this case, the watcher must be recreated.
func Watch(ctx context.Context, filepath string) (chan struct{}, error) {
	// Check if the file exists
	_, err := os.Stat(filepath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist: %v", err)
	}

	// Watch the file
	fileWatcher, err := watchFile(ctx, filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file watcher: %v", err)
	}

	// Watch the symlinks
	symlinksChan := watchSymlinks(ctx, filepath)

	retChan := make(chan struct{})

	go func() {
		defer close(retChan)

		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-fileWatcher:
				if !ok {
					return
				}

				retChan <- struct{}{}
			case <-symlinksChan:
				// The symlink has changed, the file must be rewatched
				return

			}
		}
	}()

	return retChan, nil
}

// watchFile starts watching the file for changes
func watchFile(ctx context.Context, filepath string) (<-chan struct{}, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %v", err)
	}

	if watcher.Add(filepath); err != nil {
		return nil, fmt.Errorf("failed to watch file: %v", err)
	}

	ret := make(chan struct{})

	go func() {
		defer close(ret)
		defer watcher.Close()

		for {
			select {
			case <-ctx.Done():

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					return
				}
				if event.Has(fsnotify.Create) {
					continue
				}

				ret <- struct{}{}
			case <-watcher.Errors:
				return
			}
		}
	}()

	return ret, nil
}

type symlink struct {
	target string
	link   string
}

// watchSymlinks resolves the symlinks resolving to the path at regular intervals
// and returns a channel that will be closed when a symlink is changed.
// fsnotify is not used because it does not support symlink monitoring.
func watchSymlinks(ctx context.Context, path string) <-chan struct{} {
	retChan := make(chan struct{})

	symlinks := traceSymlinks(path)

	go func() {
		defer close(retChan)

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				for _, symlink := range symlinks {
					// Check if the symlink has changed
					target, err := os.Readlink(symlink.link)
					if err != nil {
						return
					}

					if target != symlink.target {
						return
					}
				}
			}
		}
	}()

	return retChan
}

// traceSymlinks returns the list of symlinks and their corresponding target
// that resolve to the path
func traceSymlinks(path string) []symlink {
	var ret []symlink
	for {
		target, err := os.Readlink(path)
		if err != nil {
			// not a symlink, ascend to parent directory
			parent := filepath.Dir(path)
			if parent == path {
				// at root, traversal complete
				break
			}

			path = parent
			continue
		}

		ret = append(ret, symlink{target: target, link: path})
		path = target
	}
	return ret
}
