package widgets

import (
	"github.com/wow-look-at-my/tml/widget"
)

var spinnerAttrs = []string{"frame", "kind", "color"}

// spinners are the frame sets, keyed by the name a template asks for.
var spinners = map[string][]string{
	"dots":   {"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	"line":   {"|", "/", "-", "\\"},
	"bar":    {"▁", "▃", "▄", "▅", "▆", "▇", "▆", "▅", "▄", "▃"},
	"circle": {"◐", "◓", "◑", "◒"},
	"arrow":  {"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"},
	"dot":    {"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
}

// spinnerKinds is sorted so the diagnostic listing them is stable.
var spinnerKinds = []string{"arrow", "bar", "circle", "dot", "dots", "line"}

// spinner shows a single frame of an animation. Which frame is the host's business: TML has no clock, and a widget that
type spinner struct {
	frames  []string
	frame   int
	color   string
	measure widget.Measurer
}

func newSpinner(ctx widget.Context) (widget.Native, error) {
	kind, err := ctx.Attrs.Enum("kind", "dots", spinnerKinds...)
	if err != nil {
		return nil, err
	}
	frame, err := ctx.Attrs.Int("frame", 0)
	if err != nil {
		return nil, err
	}
	frames := spinners[kind]
	// A frame counter is normally a tick count that only goes up, so it wraps here rather than making every caller do the
	frame = ((frame % len(frames)) + len(frames)) % len(frames)
	return &spinner{
		frames:  frames,
		frame:   frame,
		color:   ctx.Attrs.String("color", ""),
		measure: ctx.Measure,
	}, nil
}

func (s *spinner) Measure(_, _ int) (int, int) {
	return s.measure.Width(s.frames[s.frame]), 1
}

func (s *spinner) Render(_, _ int) string {
	return paint(s.color).Render(s.frames[s.frame])
}
