package meter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/srerickson/ocfl-go"
)

const (
	barWidth  = 15
	nameWidth = 45
)

var nameStyle = lipgloss.NewStyle().Width(nameWidth + 3)

func RunProgram(ctx context.Context, m tea.Model, run func(context.Context, chan<- IOMsg) error) error {
	ctx, cancel := context.WithCancel(ctx)
	p := tea.NewProgram(m, tea.WithoutSignalHandler(), tea.WithContext(ctx))
	pErr := make(chan error, 1)
	ioMsgChan := make(chan IOMsg)
	go func() {
		_, err := p.Run()
		pErr <- err
		cancel()
		close(pErr)
	}()
	go func() {
		// this go routine accumulates file read event messages
		// and sends them periodically to the UI for screen
		// update
		ticker := time.NewTicker(60 * time.Millisecond)
		messages := make([]IOMsg, 0, 64)
		for {
			select {
			case <-ticker.C:
				// send accumulated messages
				p.Send(slices.Clone(messages))
				messages = messages[:0]
			case msg, more := <-ioMsgChan:
				messages = append(messages, msg)
				if !more {
					p.Send(messages)
					p.Quit()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	err := run(ctx, ioMsgChan)
	close(ioMsgChan)
	return errors.Join(err, <-pErr)
}

type Meter struct {
	Total  int64 // total number of bytes read across all files
	Active map[string]Progress
	Done   []string
}

func (m Meter) Names() []string {
	active := make([]string, len(m.Active))
	i := 0
	for n := range m.Active {
		active[i] = n
		i++
	}
	sort.Strings(active)
	return active
}

func (m Meter) Init() tea.Cmd { return nil }

func (m Meter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.Active == nil {
		m.Active = map[string]Progress{}
	}
	switch msg := msg.(type) {
	case []IOMsg:
		for _, v := range msg {
			m.handleIOMsg(v)
		}
	case IOMsg:
		m.handleIOMsg(msg)
	}
	return m, nil
}

// view component
func (m Meter) DoneList(limit int) string {
	builder := &strings.Builder{}
	done := m.Done
	if l := len(done); limit > 0 && l > limit {
		done = done[l-limit:]
	}
	for _, n := range done {
		fmt.Fprintf(builder, n+"\n")
	}
	return builder.String()
}

// view component
func (m Meter) ProgressBars() string {
	builder := &strings.Builder{}
	files := m.Names()
	for _, name := range files {
		stats := m.Active[name]
		count, units := scale(stats.Count)
		bar := stats.bar(barWidth)
		if l := len(name); l > nameWidth {
			name = "..." + string(name[l-nameWidth:])
		}
		name = nameStyle.SetString(name).Render()
		fmt.Fprintf(builder, "%s %s %0.1f %s\n", name, bar, count, units)
	}
	return builder.String()
}

// view component
func (m Meter) FileCounter() string {
	word := "file"
	l := len(m.Done)
	if l == 0 || l > 1 {
		word += "s" // plural
	}
	return fmt.Sprintf("%d %s", l, word)
}

// view component
func (m Meter) BytesCounter() string {
	total, unit := scale(m.Total)
	if unit == "B" {
		return fmt.Sprintf("%d %ss", int64(total), unit)
	}
	return fmt.Sprintf("%0.2f %ss", total, unit)
}

// default view
func (m Meter) View() string {
	builder := &strings.Builder{}
	// fmt.Fprint(builder, m.DoneList())
	fmt.Fprint(builder, m.ProgressBars())
	fmt.Fprintf(builder, "%s done (%s)\n", m.FileCounter(), m.BytesCounter())
	return builder.String()
}

// IOMSg represents a single read/write event
// tracked by the the meter
type IOMsg struct {
	Name  string // file name
	Size  int    // bytes written/read during event
	Total int64  // total size of file
	EOF   bool   // true if final read/write for file
}

func (m *Meter) handleIOMsg(msg IOMsg) {
	m.Total += int64(msg.Size)
	if msg.Name == "" {
		return
	}
	stats, exists := m.Active[msg.Name]
	if msg.EOF {
		m.Done = append(m.Done, msg.Name)
		if exists {
			delete(m.Active, msg.Name)
		}
		return
	}
	stats.Count += int64(msg.Size)
	if msg.Total > 0 {
		stats.Total = msg.Total
	}
	m.Active[msg.Name] = stats
}

type Progress struct {
	Count int64 // count of sizes from IOMsg
	Total int64 // expected final count
}

func (fs Progress) bar(width int) string {
	progress := "["
	bars := 0
	if fs.Total > 0 {
		bars = int(float64(width) * float64(fs.Count) / float64(fs.Total))
		if bars > width {
			bars = width
		}
	}
	for i := 0; i < width; i++ {
		if i < bars {
			progress += "="
			continue
		}
		progress += " "
	}
	return progress + "]"
}

type OnReadFS struct {
	ocfl.FS
	Msgs chan<- IOMsg
}

func (mfs *OnReadFS) OpenFile(ctx context.Context, name string) (fs.File, error) {
	f, err := mfs.FS.OpenFile(ctx, name)
	if err != nil {
		return nil, err
	}
	orFile := &onReadFile{
		File: f,
		name: name,
		msgs: mfs.Msgs,
	}
	if info, err := f.Stat(); err == nil {
		orFile.total = info.Size()
	}
	return orFile, nil
}

type onReadFile struct {
	fs.File
	name  string
	total int64
	msgs  chan<- IOMsg
}

func (mf *onReadFile) Read(p []byte) (int, error) {
	s, err := mf.File.Read(p)
	mf.msgs <- IOMsg{Name: mf.name, Size: s, EOF: errors.Is(err, io.EOF)}
	return s, err
}

type OnReadReader struct {
	Reader io.Reader
	Name   string
	Total  int64
	Msgs   chan<- IOMsg
}

func (r *OnReadReader) Read(p []byte) (int, error) {
	s, err := r.Reader.Read(p)
	r.Msgs <- IOMsg{
		Name:  r.Name,
		Size:  s,
		Total: r.Total,
		EOF:   errors.Is(err, io.EOF)}
	return s, err
}

func scale[T int64 | float64](byteSize T) (scaled float64, unit string) {
	var units = []string{"B", "KB", "MB", "GB", "TB"}
	scaled = float64(byteSize)
	for i := 0; i < len(units); i++ {
		unit = units[i]
		if scaled < 1000 {
			return
		}
		scaled = scaled / 1000
	}
	return
}
