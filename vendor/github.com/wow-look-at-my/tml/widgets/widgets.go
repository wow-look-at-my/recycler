// Package widgets is the language's own widget library: the controls a terminal view needs before it needs anything
package widgets

import (
	"github.com/wow-look-at-my/tml/widget"
)

// Library returns a registry holding every built-in widget.
func Library() *widget.Registry {
	return widget.NewRegistry().
		BindFactory("Border", factory(newFrame(false), frameAttrs)).
		BindFactory("Popup", factory(newFrame(true), frameAttrs)).
		BindFactory("Scrollbox", factory(newScrollbox, scrollboxAttrs)).
		BindFactory("Button", slotted(newButton, buttonAttrs, buttonSlots)).
		BindFactory("Textbox", factory(newTextbox, textboxAttrs)).
		BindFactory("Checkbox", factory(newCheck("Checkbox"), checkAttrs)).
		BindFactory("Radio", factory(newCheck("Radio"), checkAttrs)).
		BindFactory("List", factory(newList, listAttrs)).
		BindFactory("Table", factory(newTable, tableAttrs)).
		BindFactory("Image", factory(newImage, imageAttrs)).
		BindFactory("Rule", factory(newRule, ruleAttrs)).
		BindFactory("ProgressBar", factory(newProgressBar, progressAttrs)).
		BindFactory("Spinner", factory(newSpinner, spinnerAttrs)).
		BindFactory("Sparkline", factory(newSparkline, sparklineAttrs)).
		BindFactory("Badge", factory(newBadge, badgeAttrs))
}

// Names lists every element the library binds, for documentation and for a host that wants to know what it is getting.
func Names() []string { return Library().Names() }

// builder makes a single widget from a single element's context.
type builder func(widget.Context) (widget.Native, error)

// factory pairs a builder with the attribute names it reads. The engine hands anything not on that list to the
func factory(build builder, attrs []string) widget.Factory {
	return widget.NewFactory(attrs, build)
}

// slotted is a factory that also names the regions content can be written into, so a misspelt <Button.Contnt> is
func slotted(build builder, attrs, slots []string) widget.Factory {
	return widget.NewSlottedFactory(attrs, slots, build)
}
