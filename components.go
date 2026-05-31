// `pin components` — read pin's /api/library/mdx-component catalogue
// and pretty-print it for the terminal (or dump raw JSON for piping).
//
// Three subcommands:
//   pin components               — list, grouped by category
//   pin components get <Name>    — one component's props + example
//   pin components dump          — every component's full detail
//
// All three accept --json. The library client is split out into
// libraryList / libraryGet so a future `pin skills`, `pin prompts`,
// etc. can reuse the plumbing with a different `kind`.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
)

// libraryItem mirrors the JSON returned by /api/library/{kind}/{name}
// and the items array of /api/library/{kind}. Kept loose with `Spec`
// as a json.RawMessage so we don't have to know each kind's schema
// up front.
type libraryItem struct {
	Name     string          `json:"name"`
	Summary  string          `json:"summary"`
	Category string          `json:"category,omitempty"`
	Spec     json.RawMessage `json:"spec,omitempty"`
	Examples []string        `json:"examples,omitempty"`
}

// componentSpec is the `spec` shape for kind=mdx-component. The
// pretty-printer knows about it; other kinds get their own.
type componentSpec struct {
	Props []componentProp `json:"props,omitempty"`
}

type componentProp struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Optional    bool   `json:"optional,omitempty"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

func runComponents(args []string) int {
	jsonOut := false
	pos := []string{}
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Println(`Usage:
  pin components               List MDX components, grouped by category.
  pin components get <Name>    Show one component's props + example.
  pin components dump          Print every component's full detail.
  pin components --json        Raw JSON from the API.`)
			return 0
		default:
			pos = append(pos, a)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	const kind = "mdx-component"

	if len(pos) == 0 {
		return doComponentsList(ctx, kind, jsonOut)
	}
	switch pos[0] {
	case "get":
		if len(pos) < 2 {
			fmt.Fprintln(os.Stderr, "usage: pin components get <Name>")
			return 2
		}
		return doComponentsGet(ctx, kind, pos[1], jsonOut)
	case "dump":
		return doComponentsDump(ctx, kind, jsonOut)
	default:
		fmt.Fprintf(os.Stderr, "pin components: unknown subcommand %q\n", pos[0])
		return 2
	}
}

func doComponentsList(ctx context.Context, kind string, jsonOut bool) int {
	items, err := libraryList(ctx, kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin components: %v\n", err)
		return 1
	}
	if jsonOut {
		return writeComponentsJSON(items)
	}
	printComponentList(os.Stdout, items)
	return 0
}

func doComponentsGet(ctx context.Context, kind, name string, jsonOut bool) int {
	item, err := libraryGet(ctx, kind, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin components: %v\n", err)
		return 1
	}
	if jsonOut {
		return writeComponentsJSON(item)
	}
	printComponentDetail(os.Stdout, item)
	return 0
}

func doComponentsDump(ctx context.Context, kind string, jsonOut bool) int {
	items, err := libraryList(ctx, kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pin components: %v\n", err)
		return 1
	}
	if jsonOut {
		// /api/library/{kind} already includes the full item, so we can
		// just serialise as-is.
		return writeComponentsJSON(items)
	}
	for i, item := range items {
		if i > 0 {
			fmt.Println()
			fmt.Println(strings.Repeat("─", 60))
			fmt.Println()
		}
		printComponentDetail(os.Stdout, item)
	}
	return 0
}

// ----- library client -----

func libraryList(ctx context.Context, kind string) ([]libraryItem, error) {
	var resp struct {
		Items []libraryItem `json:"items"`
	}
	if err := libraryGetJSON(ctx, "/api/library/"+kind, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func libraryGet(ctx context.Context, kind, name string) (libraryItem, error) {
	var resp struct {
		Item libraryItem `json:"item"`
	}
	if err := libraryGetJSON(ctx, "/api/library/"+kind+"/"+name, &resp); err != nil {
		return libraryItem{}, err
	}
	return resp.Item, nil
}

// libraryGetJSON does the auth dance + HTTP fetch + JSON decode for
// any GET /api/library/* endpoint. Shared so future kinds plug in
// without reimplementing the auth plumbing.
func libraryGetJSON(ctx context.Context, path string, out any) error {
	c, err := loadCreds()
	if err != nil {
		return fmt.Errorf("not logged in. Run `pin login`")
	}
	c, err = ensureFreshAccess(ctx, c)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.Issuer+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found: %s", path)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// ----- pretty-printing -----

// Category display order — preserved across runs so output is stable.
var categoryOrder = []string{
	"layout",
	"emphasis",
	"data",
	"conversation",
	"agent",
	"chronological",
	"code-media",
}

func printComponentList(w io.Writer, items []libraryItem) {
	groups := map[string][]libraryItem{}
	for _, it := range items {
		groups[it.Category] = append(groups[it.Category], it)
	}
	// Known categories in defined order; anything unrecognised gets
	// appended alphabetically so we don't silently drop entries when a
	// new category lands in the lib before this CLI catches up.
	seen := map[string]bool{}
	order := []string{}
	for _, c := range categoryOrder {
		if _, ok := groups[c]; ok {
			order = append(order, c)
			seen[c] = true
		}
	}
	other := []string{}
	for c := range groups {
		if !seen[c] {
			other = append(other, c)
		}
	}
	sort.Strings(other)
	order = append(order, other...)

	maxName := 0
	for _, it := range items {
		if n := len(it.Name); n > maxName {
			maxName = n
		}
	}

	for i, c := range order {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s\n", strings.ToUpper(c))
		entries := groups[c]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, it := range entries {
			fmt.Fprintf(w, "  %-*s  %s\n", maxName, it.Name, shortSummary(it.Summary))
		}
	}

	fmt.Fprintf(w, "\n%d components · `pin components get <Name>` for details · --json for machine output\n", len(items))
}

func printComponentDetail(w io.Writer, item libraryItem) {
	header := item.Name
	if item.Category != "" {
		header = item.Name + " · " + item.Category
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w)
	fmt.Fprintln(w, item.Summary)
	fmt.Fprintln(w)

	if len(item.Spec) > 0 {
		var spec componentSpec
		if err := json.Unmarshal(item.Spec, &spec); err == nil && len(spec.Props) > 0 {
			fmt.Fprintln(w, "PROPS")
			printPropsTable(w, spec.Props)
			fmt.Fprintln(w)
		}
	}

	if len(item.Examples) > 0 {
		fmt.Fprintln(w, "EXAMPLE")
		for _, ex := range item.Examples {
			for _, line := range strings.Split(ex, "\n") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
	}
}

func printPropsTable(w io.Writer, props []componentProp) {
	maxName := 0
	for _, p := range props {
		if n := len(p.Name); n > maxName {
			maxName = n
		}
	}
	for _, p := range props {
		mod := "required"
		if p.Default != "" {
			mod = "default " + p.Default
		} else if p.Optional {
			mod = "optional"
		}
		fmt.Fprintf(w, "  %-*s  %s  (%s)\n", maxName, p.Name, p.Type, mod)
		if p.Description != "" {
			fmt.Fprintf(w, "  %-*s  — %s\n", maxName, "", p.Description)
		}
	}
}

// shortSummary returns just the first sentence of a summary — used in
// the list view where each row needs to fit on one terminal line. The
// full prose lives in `pin components get <Name>`.
func shortSummary(s string) string {
	// Stop at the first sentence-ending mark followed by a space.
	for i := 0; i < len(s); i++ {
		if (s[i] == '.' || s[i] == '!' || s[i] == '?') &&
			(i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n') {
			return s[: i+1]
		}
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func writeComponentsJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "pin components: encode json: %v\n", err)
		return 1
	}
	return 0
}
