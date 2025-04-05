package httpfs

import "github.com/srerickson/ocfl-go/fs/http"

// FS just wraps http.FS an keeps a copy of the URL for reporting (because
// http.FS doesn't have a way to get the url back out).
type FS struct {
	http.FS
	baseURL string
}

func New(url string, opts ...http.Option) *FS {
	return &FS{
		FS:      *http.New(url, opts...),
		baseURL: url,
	}
}

func (fs *FS) URL() string { return fs.baseURL }
