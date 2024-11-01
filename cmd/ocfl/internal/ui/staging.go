package ui

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/ui/meter"
)

func StagingUI(ctx context.Context, fsys fs.FS, alg string, skips ...*regexp.Regexp) (ocfl.PathMap, error) {
	ui := stagingUI{Alg: alg}
	digests := ocfl.PathMap{}
	run := func(ctx context.Context, msgs chan<- meter.IOMsg) error {
		var err error
		fsys = &meter.OnReadFS{FS: fsys, Msgs: msgs}
		fs.WalkDir(fsys, ".", func(n string, info fs.DirEntry, err error) error {
			if err != nil || info.Type().IsDir() {
				return err
			}
			f, err := fsys.Open(n)
			if err != nil {
				return err
			}
			defer f.Close()
			d, err := digest.DefaultRegistry().NewDigester(alg)
			if err != nil {
				return err
			}
			_, err = io.Copy(d, f)
			digests[n] = d.String()
			return err
		})
		return err
	}
	if err := meter.RunProgram(ctx, ui, run); err != nil {
		return nil, err
	}
	return digests, nil
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
