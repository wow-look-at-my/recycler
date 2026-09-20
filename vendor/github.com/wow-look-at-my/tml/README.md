# tml

Terminal Markup Language: an XML-based declarative language for reusable terminal components. XAML-flavoured, built for the Charm stack — TML solves layout and Lip Gloss renders, so a view drops straight into a Bubble Tea `View()`.

```xml
<?xml version="1.1" encoding="UTF-8"?>
<Component xmlns="urn:tml:v1" name="Card">
    <Property name="title" type="string" required="true"/>
    <Template>
        <Box style="card">
            <Text style="card.title">{title}</Text>
            <Slot/>
        </Box>
    </Template>
</Component>
```

```xml
<Card title="Deploy">
    <Text>Ready to ship.</Text>
    <Card.actions><Button label="OK"/></Card.actions>
</Card>
```

## Widgets

Buttons, fields, checkboxes, lists, tables, frames, popups, scrolling regions, progress bars, spinners, sparklines and images ship with the language and need no setup. Give a control an `id` and an `action` and it joins the focus ring:

```xml
<Button id="save" action="save" variant="primary" label="Save"/>
```

```go
for _, event := range m.view.UI().Update(msg) {
    if event.Kind == tml.Activated {
        m.act(event.Action)   // "save" -- never a coordinate, never a widget pointer
    }
}
```

Tab and the arrows move, enter activates, and the pointer hovers, clicks and scrolls against the frame the user is actually looking at. `go-toolchain && ./build/gallery` is all of it on one screen.

[![The widget gallery](https://sites.pazer.build/tml/gallery-controls.png)](https://sites.pazer.build/tml/)

[![A mock coding agent built with tml](https://sites.pazer.build/tml/agent-permission.png)](https://sites.pazer.build/tml/)

Both pictures are the examples running in a real terminal, retaken on every push. See `docs/screenshots.md` for the rest of them and how they are made.

Your own widgets plug into the same seam the library uses — see `docs/widgets.md`.

## In a Bubble Tea program

The model keeps its state and its `Update`. TML owns layout, theming and structure.

```go
widgets := widget.NewRegistry().Bind("Search", widget.Bubble(&m.input))
view, _ := tml.Load(ui, "app.tml", tml.Options{Widgets: widgets, Dark: true})

func (m *model) View() tea.View {
    out, err := m.view.Render(tml.Props{"title": sema.StringValue("Deployments")}, m.width, m.height)
    ...
}
```

Run the worked example: `go-toolchain && ./build/dashboard`, or `./build/dashboard -frame` for one frame without a terminal. `./build/agent` is a bigger one. It is a mock coding agent: a transcript that outgrows its viewport, tool output as cards, and a permission prompt that interrupts. It binds two of its own widgets alongside the library's.

## Hot reload

```go
go tml.Watch(ctx, "ui", "app.tml", opts, func(v *tml.View, err error) {
    program.Send(reloaded{view: v, err: err})
})
```

Edits show up without a restart. A reload that fails hands you the error instead of quietly keeping the last good view.

## CLI

```bash
tml check   app.tml                      # parse and check, no rendering
tml tree    app.tml                      # the expanded element tree
tml render  app.tml --width 80 --height 24
tml inspect app.tml --width 80 --height 24 --id composer

# a running program (discovers the socket; --socket or $TML_INSPECT_SOCKET overrides)
tml list
tml ids
tml query --id composer
tml tree                                 # the live frame, including boxes with no id
tml serve --addr :7777                   # the browser inspector
tml capture -o frame.html                # the same page over one frame, as a file
tml capture app.tml --width 80 --height 24 -o frame.html
```

`inspect` is the testable half of `render`. It lays the view out, then prints JSON for the elements that carry an `id`. That JSON gives the rect each one occupies, the content size it was given, its clip, its scroll position, and the lines it drew. With `--id` it reports one element. When nothing carries the id asked for, it fails and names the ids that do exist. A test can then assert where a region sits and what it says, without pinning the whole frame.

The live commands talk to any program that called `tml.Load`: `query`, `elements`, `ids`, `list`, `at`, `frame`, `input`, `restyle`, and `serve`. A program serves on `$XDG_RUNTIME_DIR/tml/<pid>.sock` with nothing set, and `tml list` names them. `tree` with a file is the expanded document. `tree` with no file is the frame on screen. See `docs/inspector.md`.

`capture` writes that browser inspector over a single frame, as a self-contained HTML file. The file holds the terminal as it was painted, the element tree, and every element's geometry, clip, scroll and drawn text. It fetches nothing when it opens, so a frame travels through a bug report or a comment box. With a file it captures a layout at the given size, and with no file it captures the frame a running program has on screen.

## What it does

Typed properties with defaults, slots with fallback content, imports, `if` and `For`. Themes carry light and dark tokens and named styles. The widget library takes keyboard and mouse interaction. The measure and arrange engine gives `auto`, fixed and star sizing across `Stack`, `Grid`, `Canvas` and `Box`. `Grid` takes XAML-style attached properties:

```xml
<Grid columns="auto,1*,2*" gap="1">
    <Text Grid.row="0" Grid.column="1" Grid.columnSpan="2">spans two columns</Text>
</Grid>
```

The load checks everything that needs no call-site value. That covers unknown elements, bad types and unresolved references, even inside a branch that never runs.

`Dock` is not implemented yet and is reported as an unknown element rather than silently ignored.

## Build

```bash
go-toolchain
```

## Docs

- `docs/language.md` — the language reference
- `docs/widgets.md` — the widget library, interaction, and writing your own
- `docs/images.md` — how a picture reaches a terminal, and what happens when it cannot
- `docs/lipgloss-contract.md` — what TML delegates to Lip Gloss, and the traps in it
- `docs/agent.md` — the mock coding agent, and what building it changed about the language
- `docs/screenshots.md` — how the pictures above are taken and published
- `CLAUDE.md` — the working index for this repo
