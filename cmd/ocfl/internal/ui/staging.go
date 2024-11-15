package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/ui/meter"
)

func StageDir(ctx context.Context, fsys ocfl.FS, dir string, alg digest.Algorithm, fixity ...digest.Algorithm) (*ocfl.Stage, error) {
	ui := stagingUI{Alg: alg.ID()}
	var stage *ocfl.Stage
	run := func(ctx context.Context, msgs chan<- meter.IOMsg) error {
		meterFS := &meter.OnReadFS{FS: fsys, Msgs: msgs}
		// this is a little more complicated than I originally thought
		// because we only want meterFS to be used for the file digest.
		// The stage should be built with ocfl.FileDigests that use
		// the fsys, not meterFS
		files, walkErrFn := ocfl.WalkFiles(ctx, meterFS, dir)
		var digestedFiles ocfl.FileDigestsSeq = func(yield func(*ocfl.FileDigests) bool) {
			for df := range files.IgnoreHidden().Digest(ctx, alg, fixity...) {
				df.FS = fsys // replace the meterFS with fsys
				if !yield(df) {
					break
				}
			}
		}
		var err error
		stage, err = digestedFiles.Stage()
		if err != nil {
			return err
		}
		return walkErrFn()
	}
	if err := meter.RunProgram(ctx, ui, run); err != nil {
		return nil, err
	}
	return stage, nil
}

type stagingUI struct {
	meter.Meter
	Alg string
}

func (m stagingUI) Init() tea.Cmd { return nil }

func (m stagingUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
	case []meter.IOMsg, meter.IOMsg:
		metModel, cmd := m.Meter.Update(msg)
		m.Meter = metModel.(meter.Meter)
		return m, cmd
	}
	return m, nil
}

func (m stagingUI) View() string {
	b := &strings.Builder{}
	fmt.Fprint(b, m.ProgressBars())
	fmt.Fprintf(b, "%s: %s (%s)\n", m.Alg, m.FileCounter(), m.BytesCounter())
	return b.String()
}
