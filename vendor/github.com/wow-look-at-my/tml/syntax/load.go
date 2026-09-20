package syntax

import (
	"io/fs"
	"path"
	"sort"
)

// Unit is a single root file together with everything reachable from it.
type Unit struct {
	// Root is the file Load was asked for.
	Root *File
	// Files holds every loaded file, keyed by its slash-separated path.
	Files map[string]*File
	// Themes holds every theme reachable from the root, in load order.
	Themes []*Theme

	scopes map[string]map[string]*Component
}

// Lookup resolves a component name as it is visible from the given file. The name resolves against that file's own
func (u *Unit) Lookup(fromFile, name string) (*Component, bool) {
	scope, ok := u.scopes[fromFile]
	if !ok {
		return nil, false
	}
	component, ok := scope[name]
	return component, ok
}

// InScope lists the component names visible from a file, sorted. It exists so a diagnostic can say what was available
func (u *Unit) InScope(fromFile string) []string {
	names := make([]string, 0, len(u.scopes[fromFile]))
	for name := range u.scopes[fromFile] {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Load parses entry and everything it imports, transitively. Paths are slash-separated and relative to the root of
func Load(fsys fs.FS, entry string) (*Unit, error) {
	unit := &Unit{
		Files:  make(map[string]*File),
		scopes: make(map[string]map[string]*Component),
	}
	root, err := unit.loadFile(fsys, entry, Pos{File: entry, Line: 1, Col: 1})
	if err != nil {
		return nil, err
	}
	unit.Root = root
	return unit, nil
}

// loadFile parses a single file and its imports. Already-loaded files are returned from the cache, which is also what stops
func (u *Unit) loadFile(fsys fs.FS, filePath string, from Pos) (*File, error) {
	filePath = path.Clean(filePath)
	if existing, ok := u.Files[filePath]; ok {
		return existing, nil
	}
	if !fs.ValidPath(filePath) {
		return nil, errorf(from, "invalid import path %q; paths are relative to the project root and cannot escape it", filePath)
	}

	src, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		return nil, errorf(from, "cannot read %s: %v", filePath, err)
	}
	file, err := Parse(filePath, src)
	if err != nil {
		return nil, err
	}

	// Registered before imports are followed so a cycle terminates here.
	u.Files[filePath] = file
	if file.Theme != nil {
		u.Themes = append(u.Themes, file.Theme)
	}

	scope := make(map[string]*Component)
	u.scopes[filePath] = scope

	if file.Component != nil {
		if err := declare(scope, file.Component, file.Component.Pos); err != nil {
			return nil, err
		}
		for _, data := range file.Component.DataTemplates {
			if err := declare(scope, data, data.Pos); err != nil {
				return nil, err
			}
		}
		for _, imp := range file.Component.Imports {
			imported, err := u.loadFile(fsys, path.Join(path.Dir(filePath), imp.Src), imp.Pos)
			if err != nil {
				return nil, err
			}
			if imported.Component == nil {
				continue // a theme import contributes no component name
			}
			if err := declare(scope, imported.Component, imp.Pos); err != nil {
				return nil, err
			}
		}
	}
	return file, nil
}

func declare(scope map[string]*Component, component *Component, at Pos) error {
	if existing, clash := scope[component.Name]; clash {
		return errorf(at, "component %q is already in scope from %s; rename one of them",
			component.Name, existing.Pos)
	}
	scope[component.Name] = component
	return nil
}
