package helm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
)

// LoadChart accepts an administrator-supplied archive or the fixed upstream URL.
// Both paths must have the pinned hash; a CR cannot supply a URL or local path.
func LoadChart(ctx context.Context, path string) (chart.Charter, error) {
	var body io.ReadCloser
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		body = f
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalog.ChartURL, nil)
		if err != nil {
			return nil, err
		}
		res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return nil, fmt.Errorf("chart download returned HTTP %d", res.StatusCode)
		}
		body = res.Body
	}
	defer body.Close()
	const maxArchiveBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(body, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArchiveBytes {
		return nil, fmt.Errorf("chart exceeds archive size limit")
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != catalog.ChartSHA256 {
		return nil, fmt.Errorf("chart SHA-256 mismatch")
	}
	return loader.LoadArchive(bytes.NewReader(data))
}
