package utils

import (
	"fmt"
	"io/fs"
	"net/url"
	"time"

	"github.com/a-h/templ"
)

func FileSize(byteSize int64) string {
	var units = []string{"Bytes", "KB", "MB", "GB", "TB"}
	scaled := float64(byteSize)
	unit := ""
	for _, u := range units {
		unit = u
		if scaled < 1000 {
			break
		}
		scaled = scaled / 1000
	}
	if unit == "Bytes" {
		return fmt.Sprintf("%d %s", int64(scaled), unit)
	}
	return fmt.Sprintf("%0.2f %s", scaled, unit)
}

func ObjectPath(id string, version string, logicalPath string) templ.SafeURL {
	if id == "" {
		return ""
	}
	result := url.URL{
		Path: "/object/" + url.PathEscape(id),
	}
	query := result.Query()
	if version != "" {
		query.Add("version", version)
	}

	if fs.ValidPath(logicalPath) {
		query.Add("path", logicalPath)
	}
	result.RawQuery = query.Encode()
	return templ.URL(result.String())
}

func DownloadPath(id string, contentPath string) templ.SafeURL {
	if contentPath == "" || id == "" {
		return ""
	}
	return templ.SafeURL("/download/" + url.PathEscape(id) + "/" + url.PathEscape(contentPath))
}

func FormatDate(t time.Time) string {
	return t.Format(time.DateOnly)
}

func ShortDigest(digest string) string {
	if len(digest) > 8 {
		return digest[0:8]
	}
	return digest
}
