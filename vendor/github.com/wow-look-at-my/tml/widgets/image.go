package widgets

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/iterm2"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/wow-look-at-my/tml/widget"

	// Decoding is by format sniffing, so the formats a terminal is likely to be shown have to be registered.
	_ "image/gif"
	_ "image/jpeg"
)

var imageAttrs = []string{"src", "alt", "protocol"}

// protocols are the ways an image can reach the screen, best leading.
const (
	// protoKitty is the Kitty graphics protocol: real pixels, understood by Kitty, Ghostty and WezTerm.
	protoKitty = "kitty"
	// protoITerm is the iTerm2 inline image protocol: real pixels, understood by iTerm2 and WezTerm.
	protoITerm = "iterm"
	// protoMosaic draws the image out of half-block characters, a pair of pixels to a cell. It works in every terminal that can
	protoMosaic = "mosaic"
	// protoLink writes the alt text as a hyperlink to the file, for a terminal that cannot even do colour.
	protoLink = "link"
	// protoAuto picks from the environment.
	protoAuto = "auto"
)

var protocolNames = []string{protoAuto, protoITerm, protoKitty, protoLink, protoMosaic}

// cellAspect is how much taller a terminal cell is than it is wide. Nothing reports the real figure portably, and the default is
const cellAspect = 2

// imageWidget draws a picture as well as the terminal it is in allows.
type imageWidget struct {
	img      image.Image
	src      string
	alt      string
	protocol string
	dark     bool
	measure  widget.Measurer
}

func newImage(ctx widget.Context) (widget.Native, error) {
	src := ctx.Attrs.String("src", "")
	if src == "" {
		return nil, fmt.Errorf("<Image> requires a src attribute")
	}
	protocol, err := ctx.Attrs.Enum("protocol", protoAuto, protocolNames...)
	if err != nil {
		return nil, err
	}
	if protocol == protoAuto {
		protocol = detectProtocol(os.Getenv)
	}

	w := &imageWidget{
		src:      src,
		alt:      ctx.Attrs.String("alt", path.Base(src)),
		protocol: protocol,
		measure:  ctx.Measure,
		dark:     ctx.Dark,
	}
	// A link needs no pixels, which is what makes it the fallback that always works: the file may not even be readable
	if protocol == protoLink {
		return w, nil
	}

	raw, err := readImage(ctx.FS, ctx.Dir, src)
	if err != nil {
		return nil, fmt.Errorf("<Image> src %q: %w", src, err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("<Image> src %q: %w", src, err)
	}
	w.img = decoded
	return w, nil
}

// readImage resolves src against the directory of the file the element was written in, so a path in a template means
func readImage(fsys fs.FS, dir, src string) ([]byte, error) {
	if fsys == nil {
		return os.ReadFile(src)
	}
	name := path.Clean(path.Join(dir, src))
	if strings.HasPrefix(name, "../") {
		return nil, fmt.Errorf("path escapes the view's filesystem")
	}
	return fs.ReadFile(fsys, name)
}

// detectProtocol reads the environment for a terminal that can draw pixels. There is no portable query that works from
func detectProtocol(env func(string) string) string {
	switch {
	case env("TERM") == "xterm-kitty", env("KITTY_WINDOW_ID") != "":
		return protoKitty
	case env("TERM_PROGRAM") == "ghostty", env("GHOSTTY_RESOURCES_DIR") != "":
		return protoKitty
	case env("TERM_PROGRAM") == "WezTerm", env("WEZTERM_EXECUTABLE") != "":
		return protoKitty
	case env("TERM_PROGRAM") == "iTerm.app", env("LC_TERMINAL") == "iTerm2":
		return protoITerm
	default:
		// Half blocks need nothing but colour, so they are what is left when nothing claims to do better.
		return protoMosaic
	}
}

// Measure fits the image into the space on offer, keeping its shape.
func (i *imageWidget) Measure(maxW, maxH int) (int, int) {
	if i.img == nil {
		return i.measure.Width(i.alt), 1
	}
	bounds := i.img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return 0, 0
	}

	w := maxW
	if w <= 0 {
		w = bounds.Dx() / cellAspect
	}
	h := cellsHigh(bounds, w)
	if maxH > 0 && h > maxH {
		h = maxH
		w = min(w, bounds.Dx()*h*cellAspect/bounds.Dy())
	}
	return max(1, w), max(1, h)
}

// cellsHigh is the height in cells that keeps the image's shape at the given width, allowing for a cell being taller
func cellsHigh(bounds image.Rectangle, w int) int {
	return max(1, bounds.Dy()*w/(bounds.Dx()*cellAspect))
}

func (i *imageWidget) Render(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	switch {
	case i.img == nil:
		return i.link()
	case i.protocol == protoKitty:
		return i.reserve(i.kitty(w, h), w, h)
	case i.protocol == protoITerm:
		return i.reserve(i.iterm(w, h), w, h)
	default:
		return i.mosaic(w, h)
	}
}

// link writes the alt text as a hyperlink to the file. Every terminal shows the text; a terminal that understands the hyperlink escape makes
func (i *imageWidget) link() string {
	return ansi.SetHyperlink(i.uri()) + i.alt + ansi.ResetHyperlink()
}

func (i *imageWidget) uri() string {
	if strings.Contains(i.src, "://") {
		return i.src
	}
	return "file://" + i.src
}

// reserve pairs a graphics escape with the cells it will cover. The escape itself measures nothing, because that is what
func (i *imageWidget) reserve(escape string, w, h int) string {
	row := strings.Repeat(" ", w)
	lines := make([]string, h)
	for n := range lines {
		lines[n] = row
	}
	return escape + strings.Join(lines, "\n")
}

func (i *imageWidget) kitty(w, h int) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, i.img); err != nil {
		return ""
	}
	opts := kitty.Options{
		Action:          kitty.TransmitAndPut,
		Transmission:    kitty.Direct,
		Format:          kitty.PNG,
		Columns:         w,
		Rows:            h,
		DoNotMoveCursor: true,
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())
	return ansi.KittyGraphics([]byte(payload), opts.String())
}

func (i *imageWidget) iterm(w, h int) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, i.img); err != nil {
		return ""
	}
	return ansi.ITerm2(iterm2.File{
		Name:            path.Base(i.src),
		Inline:          true,
		Width:           iterm2.Cells(w),
		Height:          iterm2.Cells(h),
		DoNotMoveCursor: true,
		Content:         []byte(base64.StdEncoding.EncodeToString(buf.Bytes())),
	})
}

// mosaic draws the image out of half-block characters: the upper half is the foreground colour and the lower half the
func (i *imageWidget) mosaic(w, h int) string {
	pixels := sample(i.img, w, h*2, i.backdrop())

	var b strings.Builder
	for y := 0; y < h; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := 0; x < w; x++ {
			top := pixels[y*2][x]
			bottom := pixels[y*2+1][x]
			b.WriteString(lipgloss.NewStyle().
				Foreground(rgb(top)).
				Background(rgb(bottom)).
				Render("▀"))
		}
	}
	return b.String()
}

func rgb(c color.RGBA) color.Color {
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
}

// sample reduces the image to a w by h grid by averaging each source region, which keeps detail that picking a single pixel
func sample(img image.Image, w, h int, back backdrop) [][]color.RGBA {
	bounds := img.Bounds()
	grid := make([][]color.RGBA, h)

	for y := range grid {
		grid[y] = make([]color.RGBA, w)
		for x := range grid[y] {
			x0 := bounds.Min.X + bounds.Dx()*x/w
			x1 := max(x0+1, bounds.Min.X+bounds.Dx()*(x+1)/w)
			y0 := bounds.Min.Y + bounds.Dy()*y/h
			y1 := max(y0+1, bounds.Min.Y+bounds.Dy()*(y+1)/h)
			grid[y][x] = average(img, x0, y0, x1, y1, back)
		}
	}
	return grid
}

// backdrop is the theme's own background. Nothing reports the terminal's real portably, so which half of the theme
func (i *imageWidget) backdrop() backdrop {
	if i.dark {
		return backdrop{}
	}
	return backdrop{r: 0xffff, g: 0xffff, b: 0xffff}
}

func average(img image.Image, x0, y0, x1, y1 int, back backdrop) color.RGBA {
	var sr, sg, sb, n uint32
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// Straight alpha onto the backdrop: Go hands back premultiplied values, so the backdrop's share is what the alpha
			gap := 0xffff - a
			sr += (r + back.r*gap/0xffff) >> 8
			sg += (g + back.g*gap/0xffff) >> 8
			sb += (b + back.b*gap/0xffff) >> 8
			n++
		}
	}
	if n == 0 {
		return color.RGBA{A: 0xff}
	}
	return color.RGBA{R: uint8(sr / n), G: uint8(sg / n), B: uint8(sb / n), A: 0xff}
}
