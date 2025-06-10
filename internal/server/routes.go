package server

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-tools/internal/server/assets"
	"github.com/srerickson/ocfl-tools/internal/server/model"
	"github.com/srerickson/ocfl-tools/internal/server/ui/pages"
)

// addRoutes sets up all routes for the mux
func addRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	root *ocfl.Root,
	index model.RootIndex,
) {
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServerFS(assets.FS)))
	mux.HandleFunc("GET /{$}", handleIndex(logger, index))
	mux.HandleFunc("GET /object/{id...}", handleObject(logger, root, index))
	mux.HandleFunc("GET /download/{id}/{name}", handleDownload(logger, root, index))
}

func handleDownload(logger *slog.Logger, root *ocfl.Root, index model.RootIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := r.PathValue("id")
		name := r.PathValue("name")
		if !fs.ValidPath(name) {
			http.Error(w, "invalid file name", http.StatusBadRequest)
			return
		}
		idxObj := index.Get(id)
		if idxObj == nil {
			http.NotFound(w, r)
			return
		}
		fullPath := path.Join(idxObj.Path, name)
		f, err := root.FS().OpenFile(ctx, fullPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Length", strconv.FormatInt(info.Size(), 10))
		if _, err := io.Copy(w, f); err != nil {
			// log error
			logger.Error(err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
}

func handleIndex(logger *slog.Logger, index model.RootIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if err := pages.Index(index.Objects()).Render(ctx, w); err != nil {
			logger.Error(err.Error())
		}
	}
}

func handleObject(logger *slog.Logger, root *ocfl.Root, index model.RootIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, _ := url.PathUnescape(r.PathValue("id"))
		statePath := r.URL.Query().Get("path")
		version := r.URL.Query().Get("version")
		//logger.Debug("get oject", "id", id, "version", version, "path", statePath)
		var obj *ocfl.Object
		var err error
		switch {
		case root.Layout() == nil:
			idxObj := index.Get(id)
			if idxObj == nil {
				http.NotFound(w, r)
				return
			}
			obj, err = ocfl.NewObject(ctx, root.FS(), idxObj.Path, ocfl.ObjectMustExist())
		default:
			obj, err = root.NewObject(ctx, id, ocfl.ObjectMustExist())
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		templateData, err := model.NewObject(ctx, obj, version, statePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := pages.Object(templateData).Render(ctx, w); err != nil {
			logger.Error(err.Error())
		}
	}
}
