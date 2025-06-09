package server

import (
	"log/slog"
	"net/http"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-tools/internal/server/model"
)

func NewServer(
	logger *slog.Logger,
	root *ocfl.Root,
	index model.RootIndex,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, root, index)
	return mux
}
