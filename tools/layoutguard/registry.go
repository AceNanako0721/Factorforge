package layoutguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RegistryPath is the machine-readable registry of layers, modules and specs.
const RegistryPath = "doc/layers.json"

// Layer is an abstract dependency level. A layer holds any number of modules
// and specs; it only says which other layers its modules may use.
type Layer struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Uses    []string `json:"uses"`
}

// Module is a buildable unit with its own directory, entry points and public
// surface. Every module belongs to exactly one layer.
type Module struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Layer     string   `json:"layer"`
	Path      string   `json:"path"`
	Tests     string   `json:"tests"`
	Languages []string `json:"languages"`
	// Standard modules use the fixed package areas (domain, ports, ...).
	Standard bool `json:"standard"`
	// Public lists the package areas other modules may import.
	Public []string `json:"public"`
	// Protocol names a contracts directory through which lower layers may call
	// this module as a separate process. Importing its code stays forbidden.
	Protocol string `json:"protocol,omitempty"`
}

// Spec is one specification book with its design book. A layer can have several
// specs and a spec can cover several modules.
type Spec struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Layer   string   `json:"layer"`
	Modules []string `json:"modules"`
	Spec    string   `json:"spec"`
	Design  string   `json:"design"`
}

type Registry struct {
	SchemaVersion int      `json:"schema_version"`
	Layers        []Layer  `json:"layers"`
	Modules       []Module `json:"modules"`
	Specs         []Spec   `json:"specs"`
}

func LoadRegistry(root string) (Registry, error) {
	var r Registry
	raw, err := os.ReadFile(filepath.Join(root, RegistryPath))
	if err != nil {
		return r, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&r); err != nil {
		return r, err
	}
	return r, nil
}

func (r Registry) layer(id string) *Layer {
	for i := range r.Layers {
		if r.Layers[i].ID == id {
			return &r.Layers[i]
		}
	}
	return nil
}

// uses reports whether modules of layer from may use modules of layer to.
func (r Registry) uses(from, to string) bool {
	l := r.layer(from)
	return l != nil && has(l.Uses, to)
}

// moduleOf returns the module whose directory contains the source path.
func (r Registry) moduleOf(path string) *Module {
	var best *Module
	for i := range r.Modules {
		m := &r.Modules[i]
		if path == m.Path || strings.HasPrefix(path, m.Path+"/") {
			if best == nil || len(m.Path) > len(best.Path) {
				best = m
			}
		}
	}
	return best
}

// Validate checks the registry on its own: identifiers, references and that the
// layers form an acyclic dependency order.
func (r Registry) Validate() []string {
	failures := []string{}
	fail := func(reason string) { failures = append(failures, RegistryPath+": "+reason) }
	if r.SchemaVersion != 1 {
		fail("unsupported schema version")
	}
	seen := map[string]bool{}
	for _, l := range r.Layers {
		if l.ID == "" || seen[l.ID] {
			fail("layer identifier missing or duplicated: " + l.ID)
		}
		seen[l.ID] = true
	}
	for _, l := range r.Layers {
		for _, u := range l.Uses {
			if !seen[u] || u == l.ID {
				fail("layer " + l.ID + " uses unknown or own layer " + u)
			}
		}
	}
	// A cycle would let two layers depend on each other.
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return false
		case 2:
			return true
		}
		state[id] = 1
		if l := r.layer(id); l != nil {
			for _, u := range l.Uses {
				if !visit(u) {
					return false
				}
			}
		}
		state[id] = 2
		return true
	}
	for _, l := range r.Layers {
		if !visit(l.ID) {
			fail("layers form a dependency cycle")
			break
		}
	}
	modules := map[string]bool{}
	for _, m := range r.Modules {
		if m.ID == "" || modules[m.ID] {
			fail("module identifier missing or duplicated: " + m.ID)
		}
		modules[m.ID] = true
		if !seen[m.Layer] {
			fail("module " + m.ID + " names unknown layer " + m.Layer)
		}
		if m.Path == "" || !strings.HasPrefix(m.Path, "src/factorforge/") || m.Tests == "" || len(m.Languages) == 0 {
			fail("module " + m.ID + " lacks path, tests or languages")
		}
		for _, other := range r.Modules {
			if other.ID != m.ID && (strings.HasPrefix(other.Path, m.Path+"/") || other.Path == m.Path) {
				fail("module " + other.ID + " nested in or equal to module " + m.ID)
			}
		}
	}
	covered := map[string]bool{}
	specs := map[string]bool{}
	for _, s := range r.Specs {
		if s.ID == "" || specs[s.ID] {
			fail("spec identifier missing or duplicated: " + s.ID)
		}
		specs[s.ID] = true
		if !seen[s.Layer] || s.Spec == "" || s.Design == "" || len(s.Modules) == 0 {
			fail("spec " + s.ID + " lacks layer, books or modules")
		}
		for _, id := range s.Modules {
			m := r.module(id)
			if m == nil || m.Layer != s.Layer {
				fail("spec " + s.ID + " covers module " + id + " that is unknown or in another layer")
				continue
			}
			covered[id] = true
		}
	}
	for _, m := range r.Modules {
		if !covered[m.ID] {
			fail("module " + m.ID + " is covered by no spec")
		}
	}
	sort.Strings(failures)
	return failures
}

func (r Registry) module(id string) *Module {
	for i := range r.Modules {
		if r.Modules[i].ID == id {
			return &r.Modules[i]
		}
	}
	return nil
}

// CheckFiles verifies the registry against the file system: every registered
// directory and book exists and every book is registered.
func (r Registry) CheckFiles(root string) []string {
	failures := []string{}
	fail := func(reason string) { failures = append(failures, RegistryPath+": "+reason) }
	isDir := func(path string) bool {
		info, err := os.Stat(filepath.Join(root, path))
		return err == nil && info.IsDir()
	}
	isFile := func(path string) bool {
		info, err := os.Stat(filepath.Join(root, path))
		return err == nil && info.Mode().IsRegular()
	}
	for _, m := range r.Modules {
		if !isDir(m.Path) {
			fail("module " + m.ID + " directory missing: " + m.Path)
		}
		if !isDir(m.Tests) {
			fail("module " + m.ID + " tests missing: " + m.Tests)
		}
		if m.Protocol != "" && !isDir(m.Protocol) {
			fail("module " + m.ID + " protocol directory missing: " + m.Protocol)
		}
	}
	books := map[string]bool{}
	for _, s := range r.Specs {
		for _, book := range []string{s.Spec, s.Design} {
			books[book] = true
			if !isFile(book) {
				fail("spec " + s.ID + " book missing: " + book)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "doc"))
	if err == nil {
		for _, e := range entries {
			name := "doc/" + e.Name()
			if e.Type().IsRegular() && (strings.HasSuffix(name, "式样书.md") || strings.HasSuffix(name, "设计书.md")) && !books[name] {
				fail("book not registered in any spec: " + name)
			}
		}
	}
	sort.Strings(failures)
	return failures
}
