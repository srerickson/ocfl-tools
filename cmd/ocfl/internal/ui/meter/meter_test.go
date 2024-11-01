package meter_test

import (
	"io"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/ui/meter"
	"golang.org/x/sync/errgroup"
)

func TestMeter(t *testing.T) {
	m := meter.Meter{}
	tm := teatest.NewTestModel(t, m)

	grp := &errgroup.Group{}
	grp.Go(func() error {
		tm.Send([]meter.IOMsg{
			{Name: "file.txt", Size: 50, Total: 100},
			{Name: "file.txt", Size: 10},
		})
		return nil
	})
	grp.Go(func() error {
		tm.Send(meter.IOMsg{Name: "dir/file2.data", Size: 90, Total: 90})
		return nil
	})
	grp.Go(func() error {
		tm.Send(meter.IOMsg{Name: "very/long-Directory-Name/to-check-text-padding.extension", Size: 90})
		return nil
	})
	grp.Go(func() error {
		tm.Send(meter.IOMsg{Name: "extra.jpg", Size: 1024, EOF: true})
		return nil
	})
	be.NilErr(t, grp.Wait())
	be.NilErr(t, tm.Quit())
	out, err := io.ReadAll(tm.FinalOutput(t))
	if err != nil {
		t.Error(err)
	}
	teatest.RequireEqualOutput(t, out)
}
