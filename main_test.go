package main

import "testing"

func TestParseShareRef(t *testing.T) {
	cases := []struct {
		in       string
		wantID   string
		wantHost string
	}{
		{"01HXYZ", "01HXYZ", ""},
		{"p/01HXYZ", "01HXYZ", ""},
		{"01HXYZ.mdx", "01HXYZ", ""},
		{"01HXYZ.html", "01HXYZ", ""},
		{"https://pin.bitcomplete.dev/p/01HXYZ", "01HXYZ", "https://pin.bitcomplete.dev"},
		{"https://pin.bitcomplete.dev/p/01HXYZ.mdx", "01HXYZ", "https://pin.bitcomplete.dev"},
		{"http://localhost:8080/p/01HXYZ", "01HXYZ", "http://localhost:8080"},
		{"  01HXYZ  ", "01HXYZ", ""}, // whitespace tolerated
		// Garbage paths return empty id.
		{"", "", ""},
		{"https://pin.bitcomplete.dev/", "", ""},
		{"01HX YZ", "", ""},      // space in id
		{"foo/bar/01HXYZ", "", ""},
	}
	for _, tc := range cases {
		gotID, gotHost := parseShareRef(tc.in)
		if gotID != tc.wantID || gotHost != tc.wantHost {
			t.Errorf("parseShareRef(%q) = (%q, %q), want (%q, %q)",
				tc.in, gotID, gotHost, tc.wantID, tc.wantHost)
		}
	}
}
