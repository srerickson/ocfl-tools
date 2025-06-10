package run

import (
	"net/http"

	"github.com/srerickson/ocfl-tools/internal/server"
	"github.com/srerickson/ocfl-tools/internal/server/model"
)

var serverHelp = "Start http server for accessing OCFL objects in the storage root"

type ServerCmd struct {
	ListenAddress string `name:"listen" short:"l" default:":8875" help:"port to listen for http connections"`
}

func (cmd *ServerCmd) Run(g *globals) error {
	ctx := g.ctx
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	index := &model.MapRootIndex{}
	if err := index.ReIndex(root.Objects(ctx)); err != nil {
		return err
	}
	srv := server.NewServer(g.logger, root, index)
	return http.ListenAndServe(cmd.ListenAddress, srv)
}
