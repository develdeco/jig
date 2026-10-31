package recall

import "strings"

// MapSections is what a chart's map.md says, by the sections of the chart
// template: its title (the first top-level heading) and the items of
// Destination, Decisions so far, Not yet specified (fog) and Out of scope. A
// section the map lacks is empty; Notes is prose for people and is not read.
type MapSections struct {
	Title       string   `json:"title,omitempty"`
	Destination []string `json:"destination"`
	Decided     []string `json:"decided"`
	Fog         []string `json:"fog"`
	OutOfScope  []string `json:"out_of_scope"`
}

// ParseMap reads the sections of a chart's map.md, tolerating a map that has
// only some of them or names them differently (those come back empty).
func ParseMap(mapMD string) MapSections {
	return MapSections{
		Title:       titleOf(mapMD),
		Destination: nonNil(SectionItems(mapMD, headingStarts("destination"))),
		Decided:     nonNil(SectionItems(mapMD, headingStarts("decisions so far", "decided"))),
		Fog:         nonNil(FogItems(mapMD)),
		OutOfScope:  nonNil(SectionItems(mapMD, headingStarts("out of scope"))),
	}
}

// MapTitle is the title of a chart's map.md, its first top-level heading: ParseMap's
// Title without reading the sections, for a listing of every chart.
func MapTitle(mapMD string) string { return titleOf(mapMD) }

// headingStarts matches a heading whose text, in lower case, starts with one of
// the prefixes.
func headingStarts(prefixes ...string) func(string) bool {
	return func(text string) bool {
		t := strings.ToLower(text)
		for _, p := range prefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}
}

// titleOf is the text of the first top-level heading outside a code fence, ""
// when the map has none.
func titleOf(mapMD string) string {
	fence := false
	for _, line := range strings.Split(strings.ReplaceAll(mapMD, "\r\n", "\n"), "\n") {
		if isFence(line) {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if lvl, text := headingOf(line); lvl == 1 {
			return text
		}
	}
	return ""
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}
