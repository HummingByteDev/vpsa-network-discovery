package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// EnsureBview makes sure a bview file exists at path, downloading from url
// when the local copy is missing or older than maxAge. RIPE RIS publishes
// bviews every 8 hours, so a fresh-enough file is reused rather than
// re-downloading ~4 GB per build. The download goes to a temp file and is
// renamed into place only on success — a killed build never leaves a
// truncated bview for the next run to parse.
func EnsureBview(ctx context.Context, url, path string, maxAge time.Duration, log *slog.Logger) error {
	if url == "" {
		return nil // operator provides the file out of band
	}
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < maxAge && st.Size() > 0 {
		log.Info("reusing local bview", "path", path, "age", time.Since(st.ModTime()).Round(time.Minute))
		return nil
	}
	log.Info("downloading RIS bview", "url", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("download bview: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download bview: %s from %s", resp.Status, url)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bview-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download bview: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	log.Info("bview downloaded", "bytes", n, "path", path)
	return nil
}
