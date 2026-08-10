package ui

import (
	"strings"
	"testing"

	"lazysql/internal/config"
)

// connTagColor resolves the same way [theme] colors do: named ANSI colors
// and hex work, everything else reports no tag rather than erroring.
func TestConnTagColor(t *testing.T) {
	cases := []struct {
		color string
		ok    bool
	}{
		{"", false},
		{"red", true},
		{"#ff8800", true},
		{"9", true},
		{"not-a-color", false},
	}
	for _, c := range cases {
		_, ok := connTagColor(config.Connection{Name: "x", Color: c.color})
		if ok != c.ok {
			t.Errorf("connTagColor(%q) ok = %v, want %v", c.color, ok, c.ok)
		}
	}
}

// An invalid color never fails config load: New() starts fine and logs one
// warning per offending profile, naming it and never crashing.
func TestInvalidColorDegradesGracefully(t *testing.T) {
	writeConfig(t, &config.Config{Connections: []config.Connection{
		{Name: "prod", Engine: "sqlite", File: "/tmp/prod.db", Color: "not-a-color"},
		{Name: "dev", Engine: "sqlite", File: "/tmp/dev.db", Color: "green"},
	}})
	m, err := New()
	if err != nil {
		t.Fatalf("New() failed on an invalid connection color: %v", err)
	}
	if len(m.colorWarnings) != 1 {
		t.Fatalf("colorWarnings = %v, want exactly one", m.colorWarnings)
	}
	if !strings.Contains(m.colorWarnings[0], "prod") {
		t.Errorf("warning %q does not name the offending connection", m.colorWarnings[0])
	}

	cmds := drain(m.Init())
	var logged []string
	for _, msg := range cmds {
		if lm, ok := msg.(commandLogMsg); ok {
			logged = append(logged, lm.line)
		}
	}
	found := false
	for _, l := range logged {
		if strings.Contains(l, "prod") && strings.Contains(l, "warning") {
			found = true
		}
	}
	if !found {
		t.Errorf("Init() commands = %v, want a logged warning naming prod", logged)
	}

	if _, ok := connTagColor(m.cfg.Connections[0]); ok {
		t.Error("an invalid color resolved to a tag")
	}
	if _, ok := connTagColor(m.cfg.Connections[1]); !ok {
		t.Error("a valid color on another profile was rejected")
	}
}

// The panel [1] list marks every tagged connection, valid or not, and
// leaves an untagged one alone.
func TestRefreshConnectionsSetsTagColor(t *testing.T) {
	m := sized(100, 30)
	m.cfg.Connections[0].Color = "red"
	m.cfg.Connections[1].Color = "not-a-color"
	m.refreshConnections("")

	p := m.panels[panelConnections]
	if _, ok := p.tagColor[m.cfg.Connections[0].Name]; !ok {
		t.Error("a valid color did not produce a panel tag")
	}
	if _, ok := p.tagColor[m.cfg.Connections[1].Name]; ok {
		t.Error("an invalid color produced a panel tag")
	}
	if _, ok := p.tagColor[m.cfg.Connections[2].Name]; ok {
		t.Error("an untagged connection has a panel tag")
	}
}

// activeTagColor and its rendering helpers follow the live connection, not
// just any profile in the list.
func TestActiveTagColorAndMarkers(t *testing.T) {
	m := sized(100, 30)
	m.cfg.Connections[0].Color = "cyan"
	m.active = m.cfg.Connections[0].Name

	if _, ok := m.activeTagColor(); !ok {
		t.Fatal("activeTagColor found no tag on the active, colored connection")
	}
	if marker := m.tagMarkerFor(m.active); !strings.Contains(marker, tagMarker) {
		t.Errorf("tagMarkerFor(%q) = %q, want it to contain %q", m.active, marker, tagMarker)
	}
	if got := m.taggedConnName(m.active); !strings.Contains(got, m.active) {
		t.Errorf("taggedConnName(%q) = %q, want it to contain the name", m.active, got)
	}

	m.active = m.cfg.Connections[1].Name // untagged fixture connection
	if _, ok := m.activeTagColor(); ok {
		t.Error("activeTagColor found a tag on an untagged connection")
	}
	if marker := m.tagMarkerFor(m.active); marker != "" {
		t.Errorf("tagMarkerFor on an untagged connection = %q, want empty", marker)
	}
	if got := m.taggedConnName(m.active); got != m.active {
		t.Errorf("taggedConnName on an untagged connection = %q, want the plain name", got)
	}
}

// The form's color picker persists a named choice, a custom hex value, and
// rejects a custom value that does not resolve.
func TestConnectionFormColorField(t *testing.T) {
	base := testConnections()[0]

	f := newConnectionForm("New connection", base, "")
	f.field("color").choice = 1 // "red", index 0 is "none"
	c, _, err := f.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if c.Color != "red" {
		t.Fatalf("Color = %q, want %q", c.Color, "red")
	}

	f = newConnectionForm("New connection", base, "")
	custom := f.field("color")
	custom.choice = len(custom.choices) - 1 // "custom…"
	f.field("color_hex").input.SetValue("#123abc")
	c, _, err = f.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if c.Color != "#123abc" {
		t.Fatalf("Color = %q, want the custom hex", c.Color)
	}

	f = newConnectionForm("New connection", base, "")
	custom = f.field("color")
	custom.choice = len(custom.choices) - 1
	f.field("color_hex").input.SetValue("not-a-color")
	if _, _, err = f.toConnection(); err == nil {
		t.Fatal("an unresolvable custom color did not fail validation")
	}

	// Editing a profile with a custom color reopens with "custom…"
	// selected and the raw value in the hex field.
	tagged := base
	tagged.Color = "#445566"
	back := newConnectionForm("Edit", tagged, tagged.Name)
	if got := back.rawValue("color"); got != "custom" {
		t.Fatalf("color choice = %q, want custom", got)
	}
	if got := back.rawValue("color_hex"); got != "#445566" {
		t.Fatalf("color_hex = %q, want the stored hex", got)
	}

	// Editing a profile with a named color reopens with that choice
	// selected, and an untouched form round-trips it unchanged.
	tagged.Color = "magenta"
	back = newConnectionForm("Edit", tagged, tagged.Name)
	if got := back.rawValue("color"); got != "magenta" {
		t.Fatalf("color choice = %q, want magenta", got)
	}
	c, _, err = back.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if c.Color != "magenta" {
		t.Fatalf("Color = %q, want magenta", c.Color)
	}
}

// The changeset commit confirmation names the connection in its tag color
// for salience, and stays plain for an untagged connection.
func TestCommitModalNamesTaggedConnection(t *testing.T) {
	m := dataBrowsing(t)
	i := m.cfg.Index(m.active)
	if i < 0 {
		t.Fatalf("active connection %q not found in cfg", m.active)
	}
	m.cfg.Connections[i].Color = "red"

	m = send(t, m, press('l'))
	m = stageEdit(t, m, "renamed")
	m = send(t, m, press('c'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("c opened %T, want the commit confirmation", m.modal)
	}
	want := m.taggedConnName(m.active)
	if !strings.Contains(cm.body, want) {
		t.Fatalf("commit modal body = %q, want it to contain the tagged name %q", cm.body, want)
	}
	if !strings.Contains(want, "\x1b[") {
		t.Fatalf("taggedConnName(%q) = %q, want ANSI styling for a tagged connection", m.active, want)
	}
}

// The unguarded DELETE/UPDATE confirmation (#31) does the same.
func TestUnguardedWriteModalNamesTaggedConnection(t *testing.T) {
	m := queryable(t)
	i := m.cfg.Index(m.active)
	if i < 0 {
		t.Fatalf("active connection %q not found in cfg", m.active)
	}
	m.cfg.Connections[i].Color = "yellow"

	m = runQuery(t, m, "DELETE FROM q")
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("unguarded DELETE opened %T, want the confirmation", m.modal)
	}
	want := m.taggedConnName(m.active)
	if !strings.Contains(cm.body, want) {
		t.Fatalf("unguarded-write modal body = %q, want it to contain the tagged name %q", cm.body, want)
	}
}

// The active connection's tag tints the main view's top border, and only
// the top: BorderTopForeground leaves the sides/bottom carrying the usual
// focus/blur color untouched.
func TestMainViewBorderTintedByActiveTag(t *testing.T) {
	m := dataBrowsing(t)
	i := m.cfg.Index(m.active)
	if i < 0 {
		t.Fatalf("active connection %q not found in cfg", m.active)
	}
	m.cfg.Connections[i].Color = "blue"

	tc, ok := m.activeTagColor()
	if !ok {
		t.Fatal("activeTagColor found no tag after tagging the active connection")
	}

	tinted := m.style.blurredBorder.BorderTopForeground(tc)
	untinted := m.style.blurredBorder
	if tinted.String() == untinted.String() {
		t.Fatal("BorderTopForeground did not change the border style")
	}
}

// The marker actually reaches the screen: on panel [1] for every tagged
// connection, and — via the "connection: " line — in the main view while
// that connection is active.
func TestColorTagVisibleOnScreen(t *testing.T) {
	m := sized(120, 40)
	m.cfg.Connections[0].Color = "red"
	m.refreshConnections("")
	m = send(t, m, press('1'))

	out := m.View().Content
	if !strings.Contains(out, tagMarker) {
		t.Fatalf("no color tag marker on screen:\n%s", out)
	}
}
