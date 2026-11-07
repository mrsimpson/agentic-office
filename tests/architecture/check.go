// Package architecture checks the code against the arc42 model in docs/architecture/.
//
// The model comes from the arc42 CLI itself ("arc42 get --format json"), not from an own parser.
// The packages and their imports come from "go list". The rules are those of
// concept-module-boundaries and dec-architecture-test:
//
//   - Every building block has exactly one path. The path of a child lies under the path of its
//     parent. Every interface has a path inside the path of its provider.
//   - Every Go package belongs to the deepest matching path. A package in the path of an interface
//     belongs to that interface. Packages under tests/ are external actors. Any other package
//     without an owner is an error.
//   - An import P -> Q inside the module is allowed only if Q belongs to the same building block as
//     P, or Q lies in the path of an interface that the building block of P provides or lists in
//     requires, or the building block of P is the direct parent of the building block that owns Q
//     (wiring of a white box), or P is an actor and Q an interface.
//   - Interfaces that only external actors consume are not used for import checks.
//   - Every interface a building block lists in requires must be imported by that building block.
package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Arc42CLI is the pinned arc42 CLI. The test fails if it cannot run.
var Arc42CLI = []string{"npx", "-y", "@doctc/arc42@0.38.0"}

// ActorDir is the directory of external actors (tests). They may only use interfaces.
const ActorDir = "tests"

// Element is a building block, an interface or an actor of the model.
type Element struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Path     string   `json:"path"`
	Parent   string   `json:"parent"`
	Provider string   `json:"provider"`
	Requires []string `json:"requires"`
}

// Model is the part of the arc42 model that the check needs.
type Model struct {
	Blocks     map[string]*Element
	Interfaces map[string]*Element
	// RequiredBy lists, per interface, the building blocks that require it.
	RequiredBy map[string][]string
}

// LoadModel runs the arc42 CLI on the workspace dir.
func LoadModel(dir string) (*Model, error) {
	args := append(append([]string{}, Arc42CLI[1:]...), "--dir", dir, "get", "--format", "json")
	cmd := exec.Command(Arc42CLI[0], args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run the arc42 CLI (%s): %v\n%s", strings.Join(Arc42CLI, " "), err, stderr.String())
	}
	var doc struct {
		Elements []*Element `json:"elements"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("read the arc42 model: %v", err)
	}
	m := &Model{Blocks: map[string]*Element{}, Interfaces: map[string]*Element{}, RequiredBy: map[string][]string{}}
	for _, e := range doc.Elements {
		e.Path = strings.Trim(filepath.ToSlash(e.Path), "/")
		switch e.Kind {
		case "building-block":
			m.Blocks[e.ID] = e
		case "interface":
			m.Interfaces[e.ID] = e
		}
	}
	for _, b := range m.Blocks {
		for _, i := range b.Requires {
			m.RequiredBy[i] = append(m.RequiredBy[i], b.ID)
		}
	}
	return m, nil
}

// Package is a Go package of the module, with its imports inside the module.
type Package struct {
	Path    string // relative to the module root, for example "server/session"
	Imports []string
}

// LoadPackages lists the packages of the module in root, with all build tags in tags.
func LoadPackages(root string, tags []string) ([]Package, error) {
	args := []string{"list", "-e", "-json"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "./...")
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v\n%s", err, stderr.String())
	}
	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(out))
	module := ""
	for dec.More() {
		var p struct {
			ImportPath   string
			Dir          string
			Module       *struct{ Path, Dir string }
			Imports      []string
			TestImports  []string
			XTestImports []string
		}
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		if p.Module == nil {
			continue
		}
		module = p.Module.Path
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		pkg := Package{Path: filepath.ToSlash(rel)}
		seen := map[string]bool{}
		for _, list := range [][]string{p.Imports, p.TestImports, p.XTestImports} {
			for _, imp := range list {
				if imp == module || strings.HasPrefix(imp, module+"/") {
					r := strings.TrimPrefix(strings.TrimPrefix(imp, module), "/")
					if r != pkg.Path && !seen[r] {
						seen[r] = true
						pkg.Imports = append(pkg.Imports, r)
					}
				}
			}
		}
		sort.Strings(pkg.Imports)
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// Violation is one broken rule.
type Violation struct {
	Rule string // short name of the rule, for example "import"
	Msg  string
}

func (v Violation) String() string { return v.Rule + ": " + v.Msg }

// owner is who a package belongs to.
type owner struct {
	actor  bool
	block  string   // the building block, also for a package of an interface (its provider)
	ifaces []string // set if the package lies in the path of interfaces; several may share a path
}

// Check checks the packages in root against the model.
func Check(root string, m *Model, pkgs []Package) []Violation {
	var vs []Violation
	add := func(rule, format string, a ...any) { vs = append(vs, Violation{rule, fmt.Sprintf(format, a...)}) }

	// Model checks.
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); return err == nil }
	for _, b := range sortedElements(m.Blocks) {
		switch {
		case b.Path == "":
			add("model", "building block %s has no path: give it exactly one path", b.ID)
			continue
		case strings.Contains(b.Path, ","):
			add("model", "building block %s has more than one path (%s): give it exactly one", b.ID, b.Path)
			continue
		case !exists(b.Path):
			add("model", "building block %s: path %s does not exist", b.ID, b.Path)
		}
		if b.Parent != "" {
			if p := m.Blocks[b.Parent]; p != nil && p.Path != "" && !under(b.Path, p.Path) {
				add("model", "building block %s: path %s is not under the path %s of its parent %s",
					b.ID, b.Path, p.Path, p.ID)
			}
		}
	}
	for _, i := range sortedElements(m.Interfaces) {
		prov := m.Blocks[i.Provider]
		switch {
		case i.Path == "":
			add("model", "interface %s has no path: give it the path where its contract is defined", i.ID)
		case strings.Contains(i.Path, ","):
			add("model", "interface %s has more than one path (%s): give it exactly one", i.ID, i.Path)
		case !exists(i.Path):
			add("model", "interface %s: path %s does not exist", i.ID, i.Path)
		case prov != nil && prov.Path != "" && !under(i.Path, prov.Path):
			add("model", "interface %s: path %s is not inside the path %s of its provider %s",
				i.ID, i.Path, prov.Path, prov.ID)
		}
	}

	// Ownership: the deepest matching path wins; an interface wins over a building block with the
	// same path. Only interfaces that a building block requires take part (code interfaces).
	ownerOf := func(pkg string) (owner, bool) {
		if under(pkg, ActorDir) {
			return owner{actor: true}, true
		}
		block, blockLen := "", -1
		for _, b := range m.Blocks {
			if b.Path != "" && under(pkg, b.Path) && len(b.Path) > blockLen {
				block, blockLen = b.ID, len(b.Path)
			}
		}
		var ifaces []string
		ifaceLen := -1
		for _, i := range sortedElements(m.Interfaces) {
			if len(m.RequiredBy[i.ID]) == 0 || i.Path == "" || !under(pkg, i.Path) {
				continue
			}
			switch {
			case len(i.Path) > ifaceLen:
				ifaces, ifaceLen = []string{i.ID}, len(i.Path)
			case len(i.Path) == ifaceLen:
				ifaces = append(ifaces, i.ID)
			}
		}
		switch {
		case ifaceLen >= 0 && ifaceLen >= blockLen:
			return owner{block: m.Interfaces[ifaces[0]].Provider, ifaces: ifaces}, true
		case blockLen >= 0:
			return owner{block: block}, true
		default:
			return owner{}, false
		}
	}

	owners := map[string]owner{}
	for _, p := range pkgs {
		o, ok := ownerOf(p.Path)
		if !ok {
			add("owner", "package %s belongs to no building block: move it into the path of the building block "+
				"that is responsible for it, or add a building block for it", p.Path)
			continue
		}
		owners[p.Path] = o
	}

	// Imports.
	used := map[[2]string]bool{} // building block, interface
	for _, p := range pkgs {
		op, ok := owners[p.Path]
		if !ok {
			continue
		}
		for _, q := range p.Imports {
			oq, ok := owners[q]
			if !ok {
				continue // reported as a package without owner
			}
			if op.block != "" {
				for _, i := range oq.ifaces {
					used[[2]string{op.block, i}] = true
				}
			}
			if msg := checkImport(m, op, oq); msg != "" {
				add("import", "%s imports %s: %s", p.Path, q, msg)
			}
		}
	}

	// Strict requires: every required interface is used by the building block itself.
	for _, b := range sortedElements(m.Blocks) {
		for _, i := range b.Requires {
			if m.Interfaces[i] == nil {
				continue // the arc42 validator reports unknown references
			}
			if !used[[2]string{b.ID, i}] {
				add("requires", "building block %s requires %s but does not import its path %s: "+
					"remove it from requires, or use the interface", b.ID, i, m.Interfaces[i].Path)
			}
		}
	}
	return vs
}

// checkImport returns "" if the import from op to oq is allowed, otherwise what to change.
func checkImport(m *Model, op, oq owner) string {
	switch {
	case op.actor && oq.actor:
		return ""
	case op.actor:
		if len(oq.ifaces) > 0 {
			return ""
		}
		return fmt.Sprintf("an actor (tests) uses the inside of %s; use one of its interfaces instead", oq.block)
	case oq.actor:
		return "a building block depends on a test; remove the import"
	}
	if len(oq.ifaces) > 0 {
		if oq.block == op.block {
			return "" // the provider uses its own interface
		}
		for _, r := range m.Blocks[op.block].Requires {
			for _, i := range oq.ifaces {
				if r == i {
					return ""
				}
			}
		}
		if m.Blocks[oq.block].Parent == op.block {
			return "" // a parent wires the interface of a direct child
		}
		names := strings.Join(oq.ifaces, " or ")
		return fmt.Sprintf("%s uses %s without requiring it; add %s to requires of %s", op.block, names,
			names, op.block)
	}
	if oq.block == op.block {
		return ""
	}
	if m.Blocks[oq.block].Parent == op.block {
		return "" // a parent wires its direct children (white box)
	}
	return fmt.Sprintf("%s uses the inside of %s without an interface; add an interface to %s and list it in "+
		"requires of %s, or remove the import", op.block, oq.block, oq.block, op.block)
}

// under reports whether path p lies in dir (or is dir).
func under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

func sortedElements(m map[string]*Element) []*Element {
	var es []*Element
	for _, e := range m {
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
	return es
}
