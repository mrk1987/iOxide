package caldav

import (
	"sort"
	"strings"

	"github.com/emersion/go-ical"
)

// Clients resend a whole series when one occurrence changes. Writing every
// part to Proton again is slow, so parts that are the same as the cached copy
// are skipped. "The same" ignores bookkeeping properties that clients bump on
// every save, vendor extensions Proton does not store, and the way a value is
// written when its meaning is unchanged.

var volatileProps = map[string]bool{
	ical.PropDateTimeStamp: true,
	ical.PropLastModified:  true,
	ical.PropCreated:       true,
	ical.PropSequence:      true,
}

func isVolatile(name string) bool {
	return volatileProps[name] || strings.HasPrefix(name, "X-")
}

// canonicalLines renders the meaningful properties of a component, one line
// each, sorted. The VALUE parameter is dropped: it only restates the type
// the value already has.
func canonicalLines(comp *ical.Component) []string {
	var lines []string
	for name, props := range comp.Props {
		if isVolatile(name) {
			continue
		}
		for _, p := range props {
			var params []string
			for pname, values := range p.Params {
				if pname == ical.ParamValue {
					continue
				}
				params = append(params, pname+"="+strings.Join(values, ","))
			}
			sort.Strings(params)
			lines = append(lines, name+";"+strings.Join(params, ";")+":"+p.Value)
		}
	}
	sort.Strings(lines)
	return lines
}

// canonical renders a component and its sub-components as text that is
// equal exactly when the components mean the same. Proton keeps only the
// action and trigger of a reminder, so a VALARM is reduced to those.
func canonical(comp *ical.Component) string {
	var children []string
	for _, child := range comp.Children {
		if child.Name == ical.CompAlarm {
			action, _ := child.Props.Text(ical.PropAction)
			trigger := ""
			if p := child.Props.Get(ical.PropTrigger); p != nil {
				trigger = p.Value
			}
			children = append(children, "VALARM{"+action+" "+trigger+"}")
			continue
		}
		children = append(children, child.Name+"{"+canonical(child)+"}")
	}
	sort.Strings(children)
	return strings.Join(canonicalLines(comp), "\n") + "\n" + strings.Join(children, "\n")
}

// sameEvent reports whether two events mean the same thing.
func sameEvent(a, b *ical.Event) bool {
	if a == nil || b == nil {
		return false
	}
	return canonical(a.Component) == canonical(b.Component)
}

// changedProps names the properties and sub-components that differ between
// two events, for logging. Names only; values stay private.
func changedProps(a, b *ical.Event) []string {
	seen := map[string]bool{}
	index := func(ev *ical.Event) map[string]bool {
		m := map[string]bool{}
		for _, line := range strings.Split(canonical(ev.Component), "\n") {
			m[line] = true
		}
		return m
	}
	la, lb := index(a), index(b)
	for line := range la {
		if !lb[line] {
			seen[nameOf(line)] = true
		}
	}
	for line := range lb {
		if !la[line] {
			seen[nameOf(line)] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func nameOf(line string) string {
	if i := strings.IndexAny(line, ";:{"); i >= 0 {
		return line[:i]
	}
	return line
}
