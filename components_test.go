package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestShortSummary(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Single sentence.", "Single sentence."},
		{"First sentence. Second sentence.", "First sentence."},
		{"No period yet", "No period yet"},
		{"Multi-line\nsecond line should not appear.", "Multi-line"},
		{"Bang! Yes.", "Bang!"},
		{"Question? Followup.", "Question?"},
	}
	for _, c := range cases {
		if got := shortSummary(c.in); got != c.want {
			t.Errorf("shortSummary(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrintComponentList(t *testing.T) {
	items := []libraryItem{
		{Name: "Callout", Summary: "Coloured aside.", Category: "emphasis"},
		{Name: "TLDR", Summary: "Summary box.", Category: "layout"},
		{Name: "KPI", Summary: "Big number.", Category: "data"},
	}
	var buf bytes.Buffer
	printComponentList(&buf, items)
	out := buf.String()

	// Headers ALL CAPS
	for _, cat := range []string{"LAYOUT", "EMPHASIS", "DATA"} {
		if !strings.Contains(out, cat) {
			t.Errorf("missing category header %q in:\n%s", cat, out)
		}
	}
	// Items present
	for _, name := range []string{"Callout", "TLDR", "KPI"} {
		if !strings.Contains(out, name) {
			t.Errorf("missing component name %q in:\n%s", name, out)
		}
	}
	// Order: layout before emphasis (per categoryOrder)
	if i, j := strings.Index(out, "LAYOUT"), strings.Index(out, "EMPHASIS"); i == -1 || j == -1 || i > j {
		t.Errorf("category order wrong: layout=%d, emphasis=%d\n%s", i, j, out)
	}
	// Footer count
	if !strings.Contains(out, "3 components") {
		t.Errorf("missing count footer in:\n%s", out)
	}
}

func TestPrintComponentDetail(t *testing.T) {
	spec, _ := json.Marshal(componentSpec{
		Props: []componentProp{
			{Name: "type", Type: `"info" | "warning"`, Default: `"info"`, Description: "Visual tone."},
			{Name: "title", Type: "string", Optional: true, Description: "Bold title above body."},
			{Name: "children", Type: "ReactNode"}, // required, no default, no description
		},
	})
	item := libraryItem{
		Name:     "Callout",
		Summary:  "Coloured aside.",
		Category: "emphasis",
		Spec:     spec,
		Examples: []string{`<Callout type="warning">Don't merge Fridays.</Callout>`},
	}
	var buf bytes.Buffer
	printComponentDetail(&buf, item)
	out := buf.String()

	// Header
	if !strings.Contains(out, "Callout · emphasis") {
		t.Errorf("missing header:\n%s", out)
	}
	// Summary
	if !strings.Contains(out, "Coloured aside.") {
		t.Errorf("missing summary:\n%s", out)
	}
	// Section labels
	if !strings.Contains(out, "PROPS") || !strings.Contains(out, "EXAMPLE") {
		t.Errorf("missing section labels:\n%s", out)
	}
	// Default tagged
	if !strings.Contains(out, `default "info"`) {
		t.Errorf("missing default annotation:\n%s", out)
	}
	// Required tagged
	if !strings.Contains(out, "(required)") {
		t.Errorf("missing required annotation:\n%s", out)
	}
	// Example body shows up indented
	if !strings.Contains(out, `  <Callout type="warning">`) {
		t.Errorf("example not indented:\n%s", out)
	}
}

func TestPrintComponentList_unknownCategoryStillShows(t *testing.T) {
	items := []libraryItem{
		{Name: "Wonky", Summary: "Future stuff.", Category: "future-things"},
	}
	var buf bytes.Buffer
	printComponentList(&buf, items)
	out := buf.String()
	if !strings.Contains(out, "FUTURE-THINGS") || !strings.Contains(out, "Wonky") {
		t.Errorf("unknown category should still render:\n%s", out)
	}
}
