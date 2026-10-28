package appimage

// AppStream metadata is the only machine-readable description of the product that
// a software centre reads, so it is checked as a contract rather than trusted: the
// component must identify the application the package actually installs, and it
// must not claim a licence, an identity or a release the project has not approved.
//
// This test reads the source file directly, so it runs in a plain `go test ./...`
// as well as in the artifact job.

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// metainfoSource is the AppStream file that scripts/build-appimage.sh packages.
func metainfoSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "packaging", "appstream", "radanor-sparrow.metainfo.xml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("AppStream metadata: %v", err)
	}
	return path
}

type appstreamComponent struct {
	XMLName         xml.Name `xml:"component"`
	Type            string   `xml:"type,attr"`
	ID              string   `xml:"id"`
	MetadataLicense string   `xml:"metadata_license"`
	ProjectLicense  string   `xml:"project_license"`
	Name            string   `xml:"name"`
	Summary         string   `xml:"summary"`
	Description     struct {
		Inner string `xml:",innerxml"`
	} `xml:"description"`
	Launchable struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"launchable"`
	Categories struct {
		Items []string `xml:"category"`
	} `xml:"categories"`
	URL struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"url"`
	Releases struct {
		Items []string `xml:"release"`
	} `xml:"releases"`
	Screenshots struct {
		Items []string `xml:"screenshot"`
	} `xml:"screenshots"`
}

func TestAppStreamMetadataDescribesTheInstalledApplication(t *testing.T) {
	raw, err := os.ReadFile(metainfoSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<?xml")) {
		t.Error("the metadata does not start with an XML declaration")
	}
	var component appstreamComponent
	if err := xml.Unmarshal(raw, &component); err != nil {
		t.Fatalf("the metadata is not well-formed XML: %v", err)
	}

	// The identity has to be the one the package installs: the desktop entry, the
	// icon, the launcher argument and the window class all use this name.
	const desktopID = "radanor-sparrow.desktop"
	if component.Type != "desktop-application" {
		t.Errorf("component type = %q, want desktop-application", component.Type)
	}
	if component.ID != desktopID {
		t.Errorf("component id = %q, want %q so it matches the installed desktop entry", component.ID, desktopID)
	}
	if component.Launchable.Type != "desktop-id" || component.Launchable.Value != desktopID {
		t.Errorf("launchable = %q (%s), want type desktop-id and value %s", component.Launchable.Value, component.Launchable.Type, desktopID)
	}
	if component.Name != "Radanor Sparrow" {
		t.Errorf("name = %q, want %q", component.Name, "Radanor Sparrow")
	}
	if strings.TrimSpace(component.Summary) == "" {
		t.Error("summary is empty")
	}
	if len(component.Summary) > 120 {
		t.Errorf("summary is %d characters; short summaries are what a software centre shows", len(component.Summary))
	}
	if !strings.Contains(component.Description.Inner, "<p>") {
		t.Error("the description has no paragraph")
	}
	if len(component.Categories.Items) == 0 {
		t.Error("no categories are declared")
	}
	if component.URL.Type != "homepage" || !strings.HasPrefix(component.URL.Value, "https://") {
		t.Errorf("homepage = %q (%s), want an https homepage", component.URL.Value, component.URL.Type)
	}

	// Licences: the metadata's own licence is required by AppStream, and the
	// application licence must stay the Business Source License the repository
	// ships. Neither may be invented or swapped for something more permissive.
	switch component.MetadataLicense {
	case "FSFAP", "CC0-1.0", "MIT", "0BSD":
	default:
		t.Errorf("metadata_license = %q, which is not one of the permissive metadata licences this project allows", component.MetadataLicense)
	}
	if component.ProjectLicense != "BUSL-1.1" {
		t.Errorf("project_license = %q, want BUSL-1.1", component.ProjectLicense)
	}

	// No release history and no screenshots are claimed here: the project has no
	// public release and no approved screenshots. Adding either is a deliberate
	// change that should update this test in the same commit.
	if len(component.Releases.Items) != 0 {
		t.Errorf("the metadata claims %d releases; there is no public release to describe yet", len(component.Releases.Items))
	}
	if len(component.Screenshots.Items) != 0 {
		t.Errorf("the metadata carries %d screenshots; publish only approved artwork", len(component.Screenshots.Items))
	}
}

// The packaged copy must be the approved file, byte for byte: an image carrying
// different metadata from the repository would describe the application wrongly
// wherever it is installed.
func TestAppImageCarriesTheApprovedAppStreamMetadata(t *testing.T) {
	info, err := Read(artifact(t))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := os.ReadFile(metainfoSource(t))
	if err != nil {
		t.Fatal(err)
	}
	embedded := cat(t, info, "usr/share/metainfo/radanor-sparrow.metainfo.xml")
	if !bytes.Equal(embedded, approved) {
		t.Error("the AppImage's AppStream metadata is not byte-identical to packaging/appstream/radanor-sparrow.metainfo.xml")
	}
}
