// Package fswatch provides a file watcher for [github.com/go-rotini/recon]
// registries, backed by go-rotini/fs: kernel notifications where the
// platform has them, polling elsewhere, atomic-save aware and debounced.
//
//	r, err := recon.New(
//		recon.WithWatcher(fswatch.New()),
//		recon.WithSource(fileSrc),
//	)
//
// recon itself installs no watcher, so a program that only reads its
// configuration once doesn't link the watcher.
package fswatch
