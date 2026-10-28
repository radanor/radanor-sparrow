package winres

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/branding"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found above the test's working directory")
		}
		dir = parent
	}
}

func approvedIcon(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "public", "favicon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sampleOptions(t *testing.T, architecture string) Options {
	t.Helper()
	return Options{
		Icon:             approvedIcon(t),
		Architecture:     architecture,
		Company:          "Radanor",
		Product:          "Radanor Sparrow",
		Description:      "Radanor Sparrow Community Edition",
		Version:          "0.1.0-dev.0+gcc81e6633f55",
		Copyright:        "© 2026 Radanor Test Holder",
		Numeric:          [4]uint16{0, 1, 0, 0},
		OriginalFilename: "RadanorSparrow.exe",
	}
}

// The copyright holder is the one version string that must be exact or absent.
// A file's Properties dialog showing an empty or guessed LegalCopyright would
// read as a claim about who holds the rights, so a caller with no verified
// holder has to produce no string at all rather than a placeholder.
func TestVersionStringsCarryTheHolderAndOmitAnEmptyOne(t *testing.T) {
	options := sampleOptions(t, "amd64")
	if options.Copyright == "" {
		t.Fatal("the sample options carry no copyright holder, so this test would prove nothing")
	}
	read := func() map[string]string {
		values := map[string]string{}
		for _, entry := range options.versionStrings() {
			values[entry.Key] = entry.Value
		}
		return values
	}

	values := read()
	if got := values["LegalCopyright"]; got != options.Copyright {
		t.Errorf("LegalCopyright = %q, want %q", got, options.Copyright)
	}
	if len(values) != 8 {
		t.Errorf("the version carries %d strings %v, want the seven standard ones plus LegalCopyright", len(values), values)
	}

	options.Copyright = ""
	values = read()
	if got, present := values["LegalCopyright"]; present {
		t.Errorf("an empty copyright still wrote LegalCopyright %q", got)
	}
	if len(values) != 7 {
		t.Errorf("the version without a copyright carries %d strings %v, want 7", len(values), values)
	}
}

// The approved artwork is the only icon source; the parser must read exactly
// the three images it holds, uncompressed.
func TestParseICOAcceptsTheApprovedArtwork(t *testing.T) {
	images, err := ParseICO(approvedIcon(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 {
		t.Fatalf("the approved icon holds %d images, want 3", len(images))
	}
	for index, want := range []struct {
		width, height uint16
		size          int
	}{
		{16, 16, 1128},
		{32, 32, 4264},
		{48, 48, 9640},
	} {
		got := images[index]
		if got.Width != want.width || got.Height != want.height {
			t.Errorf("image %d is %dx%d, want %dx%d", index, got.Width, got.Height, want.width, want.height)
		}
		if len(got.Data) != want.size {
			t.Errorf("image %d is %d bytes, want %d", index, len(got.Data), want.size)
		}
		if got.Planes != 1 || got.BitCount != 32 {
			t.Errorf("image %d declares %d planes and %d bits, want 1 and 32", index, got.Planes, got.BitCount)
		}
		if !isDeviceIndependentBitmap(got.Data) {
			t.Errorf("image %d is not a device-independent bitmap", index)
		}
	}
}

// A malformed or unsupported icon must fail loudly rather than being copied
// through, because the resource is verified against the approved bytes.
func TestParseICORejectsInputItCannotRepresent(t *testing.T) {
	approved := approvedIcon(t)
	for name, data := range map[string][]byte{
		"empty":               nil,
		"truncated header":    approved[:4],
		"truncated directory": approved[:20],
		"wrong reserved":      append(append([]byte{}, 7, 0, 1, 0), approved[4:]...),
		"wrong type":          append(append([]byte{}, 0, 0, 2, 0), approved[4:]...),
		"no images":           []byte{0, 0, 1, 0, 0, 0},
		"impossible count":    []byte{0, 0, 1, 0, 200, 0},
	} {
		if _, err := ParseICO(data); err == nil {
			t.Errorf("ParseICO accepted %s", name)
		}
	}

	// A PNG-compressed entry is valid ICO but not something this package will
	// copy through silently.
	png := make([]byte, 6+16+40)
	binary.LittleEndian.PutUint16(png[2:4], 1)
	binary.LittleEndian.PutUint16(png[4:6], 1)
	png[6], png[7] = 16, 16
	binary.LittleEndian.PutUint16(png[6+4:6+6], 1)
	binary.LittleEndian.PutUint16(png[6+6:6+8], 32)
	binary.LittleEndian.PutUint32(png[6+8:6+12], 40)
	binary.LittleEndian.PutUint32(png[6+12:6+16], 22)
	copy(png[22:], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	if _, err := ParseICO(png); err == nil {
		t.Error("ParseICO accepted a PNG-compressed icon entry")
	}
}

// The object has to match what the linker's loader accepts, so its header,
// section names, section characteristics and relocation types are pinned
// exactly. That loader rejects a resource section whose characteristics are
// anything but initialized data plus read, and it only understands an
// image-relative 32-bit relocation.
func TestResourceObjectMatchesTheLinkerContract(t *testing.T) {
	for _, want := range []struct {
		architecture string
		machine      uint16
		relocation   uint16
	}{
		{"amd64", 0x8664, 0x0003}, // IMAGE_REL_AMD64_ADDR32NB
		{"arm64", 0xAA64, 0x0002}, // IMAGE_REL_ARM64_ADDR32NB
	} {
		object, err := Build(sampleOptions(t, want.architecture))
		if err != nil {
			t.Fatalf("%s: %v", want.architecture, err)
		}
		path := filepath.Join(t.TempDir(), "resource.syso")
		if err := os.WriteFile(path, object, 0o644); err != nil {
			t.Fatal(err)
		}
		file, err := pe.Open(path)
		if err != nil {
			t.Fatalf("%s: the object is not a readable COFF file: %v", want.architecture, err)
		}
		defer file.Close()

		if got := binary.LittleEndian.Uint16(object[0:2]); got != want.machine {
			t.Errorf("%s: machine = %#x, want %#x", want.architecture, got, want.machine)
		}
		if binary.LittleEndian.Uint32(object[4:8]) != 0 {
			t.Errorf("%s: the object carries a timestamp, so it is not reproducible", want.architecture)
		}
		if len(file.Sections) != 2 {
			t.Fatalf("%s: the object has %d sections, want exactly 2", want.architecture, len(file.Sections))
		}
		for index, name := range []string{".rsrc$01", ".rsrc$02"} {
			section := file.Sections[index]
			if strings.TrimRight(section.Name, "\x00") != name {
				t.Errorf("%s: section %d is %q, want %q", want.architecture, index, section.Name, name)
			}
			// Ordered exactly: the linker adds the first section's length to
			// every relocated value, so the directory must come first.
			if got := section.Characteristics; got != 0x40000040 {
				t.Errorf("%s: %s characteristics = %#x, want 0x40000040", want.architecture, name, got)
			}
		}
		directory := file.Sections[0]
		if directory.NumberOfRelocations == 0 {
			t.Fatalf("%s: the resource directory declares no relocations, so its addresses could never be fixed up", want.architecture)
		}
		for index, relocation := range directory.Relocs {
			if relocation.Type != want.relocation {
				t.Errorf("%s: relocation %d has type %#x, want %#x", want.architecture, index, relocation.Type, want.relocation)
			}
		}
		// Every relocation must point at the section carrying the data, and at
		// a four-byte-aligned field inside the directory.
		data, err := directory.Data()
		if err != nil {
			t.Fatal(err)
		}
		for index, relocation := range directory.Relocs {
			if relocation.VirtualAddress%4 != 0 || int(relocation.VirtualAddress)+4 > len(data) {
				t.Errorf("%s: relocation %d is at %d, which is not a four-byte field inside the directory", want.architecture, index, relocation.VirtualAddress)
			}
			if symbol := file.COFFSymbols[relocation.SymbolTableIndex]; strings.TrimRight(string(symbol.Name[:]), "\x00") != ".rsrc$02" {
				t.Errorf("%s: relocation %d points at symbol %q, want .rsrc$02", want.architecture, index, symbol.Name)
			}
		}
	}
}

// The addend written into each directory field must be the offset of that
// resource inside the data section, because the linker adds the section address
// and the directory's length to it.
func TestResourceDirectoryAddendsAddressTheDataSection(t *testing.T) {
	options := sampleOptions(t, "amd64")
	images, err := ParseICO(options.Icon)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint16, len(images))
	resources := []resource{}
	for index, image := range images {
		ids[index] = uint16(index + 1)
		resources = append(resources, resource{kind: resourceTypeIcon, name: uint32(ids[index]), data: image.Data})
	}
	resources = append(resources, resource{kind: resourceTypeGroupIcon, name: 1, data: groupIcon(images, ids)})
	resources = append(resources, resource{kind: resourceTypeVersion, name: 1, data: buildVersionInfo(options.Numeric, options.versionStrings())})

	directory, data, fixups := buildResources(resources)
	if len(fixups) != len(resources) {
		t.Fatalf("got %d fixups for %d resources, want one per resource", len(fixups), len(resources))
	}
	if len(fixups) != 5 {
		t.Fatalf("got %d fixups, want 5 (three icons, one group icon, one version)", len(fixups))
	}
	seen := map[int]bool{}
	for _, item := range fixups {
		if item.offset%4 != 0 {
			t.Errorf("a fixup is at %d, which is not four-byte aligned", item.offset)
		}
		if item.addend%4 != 0 {
			t.Errorf("a fixup addend is %d, which is not four-byte aligned", item.addend)
		}
		if item.addend >= len(data) {
			t.Errorf("a fixup addend %d is past the end of the %d-byte data section", item.addend, len(data))
		}
		if stored := binary.LittleEndian.Uint32(directory[item.offset : item.offset+4]); int(stored) != item.addend {
			t.Errorf("the directory field at %d holds %d, want the addend %d", item.offset, stored, item.addend)
		}
		if stored := binary.LittleEndian.Uint32(directory[item.offset+4 : item.offset+8]); stored == 0 {
			t.Errorf("the data entry at %d declares a zero length", item.offset)
		}
		seen[item.addend] = true
	}
	if len(seen) != len(fixups) {
		t.Errorf("two resources share a data offset: %v", seen)
	}
	// The icon images must be present in the data section exactly as supplied.
	for _, image := range images {
		if !bytes.Contains(data, image.Data) {
			t.Error("the data section does not contain an approved icon image byte for byte")
		}
	}
}

// The version resource is re-parsed here independently of the builder, so a
// padding or length mistake cannot make both sides agree by construction.
func TestVersionInfoIsWellFormed(t *testing.T) {
	entries := []VersionString{
		{Key: "CompanyName", Value: "Radanor"},
		{Key: "FileVersion", Value: "0.1.0-dev.0+gcc81e6633f55"},
		{Key: "ProductName", Value: "Radanor Sparrow"},
	}
	blob := buildVersionInfo([4]uint16{1, 2, 3, 4}, entries)

	root := parseVersionNode(t, blob, 0)
	if root.key != "VS_VERSION_INFO" {
		t.Fatalf("root key = %q", root.key)
	}
	if root.length != len(blob) {
		t.Errorf("the root wLength is %d but the resource is %d bytes", root.length, len(blob))
	}
	if root.valueLength != fixedFileInfoSize || root.valueType != 0 {
		t.Errorf("the root value is %d bytes of type %d, want %d bytes of binary type", root.valueLength, root.valueType, fixedFileInfoSize)
	}
	if len(root.value) != fixedFileInfoSize {
		t.Fatalf("the root value is %d bytes, want %d", len(root.value), fixedFileInfoSize)
	}
	fixed := root.value
	if got := binary.LittleEndian.Uint32(fixed[0:4]); got != 0xFEEF04BD {
		t.Errorf("dwSignature = %#x, want 0xFEEF04BD", got)
	}
	if got := binary.LittleEndian.Uint32(fixed[8:12]); got != 1<<16|2 {
		t.Errorf("dwFileVersionMS = %#x, want the major and minor fields 1.2", got)
	}
	if got := binary.LittleEndian.Uint32(fixed[12:16]); got != 3<<16|4 {
		t.Errorf("dwFileVersionLS = %#x, want the build and revision fields 3.4", got)
	}
	if root.valueType != 0 && root.value != nil {
		t.Error("a text value should not be treated as the binary fixed file info")
	}

	children := root.children
	if len(children) != 2 || children[0].key != "StringFileInfo" || children[1].key != "VarFileInfo" {
		t.Fatalf("root children = %v, want StringFileInfo then VarFileInfo", keysOf(children))
	}
	if children[0].offset != root.childrenOffset || children[1].offset != children[0].offset+children[0].length {
		t.Error("the root's children are not contiguous, or do not start where its value ends")
	}
	if last := children[1]; last.offset+last.length != root.length {
		t.Errorf("the last child ends at %d but the root ends at %d", last.offset+last.length, root.length)
	}

	table := children[0].children
	if len(table) != 1 {
		t.Fatalf("StringFileInfo has %d tables, want 1", len(table))
	}
	if table[0].key != "040904B0" {
		t.Errorf("the string table key is %q, want the 0x0409/0x04B0 language pair", table[0].key)
	}
	if len(table[0].children) != len(entries) {
		t.Fatalf("the string table has %d entries, want %d", len(table[0].children), len(entries))
	}
	for index, entry := range entries {
		node := table[0].children[index]
		if node.key != entry.Key {
			t.Errorf("entry %d is %q, want %q", index, node.key, entry.Key)
		}
		if node.valueType != 1 {
			t.Errorf("entry %q is not a text value", entry.Key)
		}
		if got := decodeUTF16(node.rawValue); got != entry.Value {
			t.Errorf("entry %q = %q, want %q", entry.Key, got, entry.Value)
		}
		// A text value's length counts characters including the terminator.
		if node.valueLength != len([]rune(entry.Value))+1 {
			t.Errorf("entry %q declares wValueLength %d, want %d characters", entry.Key, node.valueLength, len([]rune(entry.Value))+1)
		}
	}
	translation := children[1].children
	if len(translation) != 1 || translation[0].key != "Translation" {
		t.Fatalf("VarFileInfo children = %v, want one Translation", keysOf(translation))
	}
	if translation[0].valueLength != 4 {
		t.Errorf("Translation wValueLength = %d, want 4", translation[0].valueLength)
	}
	if got := binary.LittleEndian.Uint32(translation[0].rawValue); got != uint32(versionLanguage)|uint32(versionCodePage)<<16 {
		t.Errorf("Translation = %#x, want language %#x with code page %#x", got, versionLanguage, versionCodePage)
	}
}

func keysOf(nodes []parsedVersionNode) []string {
	out := make([]string, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node.key)
	}
	return out
}

type parsedVersionNode struct {
	key            string
	length         int
	valueLength    int
	valueType      uint16
	offset         int
	childrenOffset int
	rawValue       []byte
	value          []byte
	children       []parsedVersionNode
}

// parseVersionNode walks a VS_VERSION_INFO structure the way a resource reader
// does: a six-byte header, a null-terminated UTF-16 key, a value, then any
// children, each field padded to a four-byte boundary.
func parseVersionNode(t *testing.T, buffer []byte, offset int) parsedVersionNode {
	t.Helper()
	if offset+6 > len(buffer) {
		t.Fatalf("node at %d has no header", offset)
	}
	node := parsedVersionNode{
		length:      int(binary.LittleEndian.Uint16(buffer[offset : offset+2])),
		valueLength: int(binary.LittleEndian.Uint16(buffer[offset+2 : offset+4])),
		valueType:   binary.LittleEndian.Uint16(buffer[offset+4 : offset+6]),
		offset:      offset,
	}
	if node.length == 0 || offset+node.length > len(buffer) {
		t.Fatalf("node at %d declares length %d, past the end of a %d-byte buffer", offset, node.length, len(buffer))
	}
	end := offset + node.length

	cursor := offset + 6
	for {
		if cursor+2 > end {
			t.Fatalf("node at %d has an unterminated key", offset)
		}
		if binary.LittleEndian.Uint16(buffer[cursor:cursor+2]) == 0 {
			break
		}
		cursor += 2
	}
	for index := offset + 6; index < cursor; index += 2 {
		node.key += string(rune(binary.LittleEndian.Uint16(buffer[index : index+2])))
	}
	cursor += 2
	for (cursor-offset)%4 != 0 {
		cursor++
	}
	if node.valueType == 0 && node.valueLength > 0 {
		if cursor+node.valueLength > end {
			t.Fatalf("node %q declares a %d-byte value that does not fit", node.key, node.valueLength)
		}
		node.rawValue = buffer[cursor : cursor+node.valueLength]
		node.value = node.rawValue
		cursor += node.valueLength
		for (cursor-offset)%4 != 0 {
			cursor++
		}
	} else if node.valueType == 1 && node.valueLength > 0 {
		size := node.valueLength * 2
		if cursor+size > end {
			t.Fatalf("node %q declares a %d-character value that does not fit", node.key, node.valueLength)
		}
		node.rawValue = buffer[cursor : cursor+size]
		cursor += size
		for (cursor-offset)%4 != 0 {
			cursor++
		}
	}
	node.childrenOffset = cursor

	// Only containers have children; a string or a var has a value instead.
	if node.valueLength == 0 || node.key == "VS_VERSION_INFO" {
		for cursor+6 <= end {
			child := parseVersionNode(t, buffer, cursor)
			node.children = append(node.children, child)
			cursor += child.length
		}
	}
	return node
}

func decodeUTF16(raw []byte) string {
	var builder strings.Builder
	for index := 0; index+1 < len(raw); index += 2 {
		value := binary.LittleEndian.Uint16(raw[index : index+2])
		if value == 0 {
			break
		}
		builder.WriteRune(rune(value))
	}
	return builder.String()
}

func TestBuildRejectsIncompleteInput(t *testing.T) {
	for name, mutate := range map[string]func(*Options){
		"no version":          func(o *Options) { o.Version = "" },
		"unsupported arch":    func(o *Options) { o.Architecture = "riscv64" },
		"no icon":             func(o *Options) { o.Icon = nil },
		"icon is not an icon": func(o *Options) { o.Icon = []byte("not an icon file at all") },
	} {
		options := sampleOptions(t, "amd64")
		mutate(&options)
		if _, err := Build(options); err == nil {
			t.Errorf("Build accepted %s", name)
		}
	}
	if _, _, ok := machineFor("riscv64"); ok {
		t.Error("machineFor accepted an architecture the Windows build does not target")
	}
}

// Check the PE/COFF flag directly, independently of ReadResources, so a writer
// and reader sharing the same interpretation cannot conceal an invalid tree.
func TestResourceDirectoryUsesPESubdirectoryFlags(t *testing.T) {
	directory, _, _ := buildResources([]resource{{kind: resourceTypeVersion, name: 1, data: []byte{1, 2, 3, 4}}})
	offset := 0
	for depth := 0; depth < 3; depth++ {
		child := binary.LittleEndian.Uint32(directory[offset+20 : offset+24])
		wantDirectory := depth < 2
		if (child&0x80000000 != 0) != wantDirectory {
			t.Fatalf("depth %d: subdirectory bit=%t, want %t", depth, child&0x80000000 != 0, wantDirectory)
		}
		offset = int(child & 0x7fffffff)
	}
}

// The linked executable is the only thing that proves the Go linker accepted
// the object and laid the sections out the way the resource directory expects.
// Whether it does is not something the writer and reader can settle between
// themselves, so this builds a real Windows executable with the object beside
// it.
//
// The probe is a throwaway module in a temporary directory: the distribution
// package is not touched, and the object is not placed in the source tree. The
// artifact test below is what proves the real executable links.
func TestLinkedExecutableCarriesTheResources(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("the go toolchain is not on PATH")
	}
	root := repoRoot(t)
	// A version nothing else could have produced, so a stale object somewhere
	// on disk cannot make this pass: the linked executable has to report what
	// this test put beside it.
	options := sampleOptions(t, "amd64")
	options.Version = "9.9.9-dev.1+g0badc0ffee"
	options.Numeric = [4]uint16{9, 9, 9, 1}
	object, err := Build(options)
	if err != nil {
		t.Fatal(err)
	}

	temporary := t.TempDir()
	// The probe module declares the same language version as the repository, so
	// the probe cannot ask for a different toolchain than the checkout uses.
	errors := []error{
		os.WriteFile(filepath.Join(temporary, "go.mod"), []byte("module linkprobe\n\ngo "+languageVersion(t, root)+"\n"), 0o644),
		os.WriteFile(filepath.Join(temporary, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644),
		os.WriteFile(filepath.Join(temporary, "zz_resource_windows_amd64.syso"), object, 0o644),
	}
	for _, err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	executable := filepath.Join(temporary, "RadanorSparrow.exe")
	command := exec.Command(goTool, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-H=windowsgui", "-o", executable, ".")
	command.Dir = temporary
	command.Env = append(os.Environ(),
		"GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local",
		"CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("the linker rejected the resource object: %v\n%s", err, output)
	}

	entries, err := ReadResources(executable)
	if err != nil {
		t.Fatal(err)
	}
	assertApprovedResources(t, entries, options)
}

// languageVersion reads the `go` directive from the repository's go.mod.
//
// The file is read as lines and split on whitespace rather than matched with a
// line-anchored regular expression. A Windows checkout can materialise go.mod
// with CRLF line endings, and `$` does not match between `\r` and `\n`, so a
// regexp such as `(?m)^go (\S+)$` finds nothing there and the probe module is
// written without a language version. Splitting on whitespace makes the parse
// independent of the line ending the checkout happened to use, which is the
// property the Windows job needs; the file is not otherwise parsed because only
// this one directive is needed.
func languageVersion(t *testing.T, root string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(source), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1]
		}
	}
	t.Fatal("go.mod has no go directive")
	return ""
}

// A Windows checkout can hand the test a CRLF go.mod while the repository's
// file is LF. The probe module must learn the same language version either way,
// otherwise the linked-executable test fails on Windows for a reason that has
// nothing to do with the resource object it is meant to check.
func TestLanguageVersionReadsEitherLineEnding(t *testing.T) {
	for name, content := range map[string]string{
		"LF":   "module linkprobe\n\ngo 1.27.0\n",
		"CRLF": "module linkprobe\r\n\r\ngo 1.27.0\r\n",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := languageVersion(t, dir); got != "1.27.0" {
			t.Errorf("%s go.mod: go directive = %q, want 1.27.0", name, got)
		}
	}
}

// When the packaging job supplies the built distribution, the assertions run
// against the artifact that would actually be shipped. The version is checked
// against the version rule rather than against a number this test would have to
// hardcode to one commit, so the check still means something on the next one.
func TestBuiltDistributionCarriesTheResources(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built distribution executable")
	}
	// The same variable names the Linux artifact in the Linux validation run, and
	// that executable has no PE resources to check. An artifact that claims to be
	// a Windows executable is still checked, so a corrupt one cannot slip past
	// as a skip.
	file, err := pe.Open(binary)
	if err != nil {
		if strings.EqualFold(filepath.Ext(binary), ".exe") {
			t.Fatalf("%s is named as a Windows executable but is not a PE image: %v", binary, err)
		}
		t.Skipf("%s is not a PE image, so it carries no Windows resource tree", binary)
	}
	_ = file.Close()

	entries, err := ReadResources(binary)
	if err != nil {
		t.Fatal(err)
	}
	assertDistributionResources(t, entries)
}

// assertDistributionResources checks an executable the build scripts produced:
// the artwork is exact, while the version is checked against the rule and
// against the identity strings the source states, so the same assertions hold
// for any commit and for either build variant.
func assertDistributionResources(t *testing.T, entries []Entry) {
	t.Helper()
	images, err := ParseICO(approvedIcon(t))
	if err != nil {
		t.Fatal(err)
	}
	assertIconResources(t, entries, images)

	version := resourcesOfType(entries, resourceTypeVersion)
	if len(version) != 1 {
		t.Fatalf("the executable carries %d RT_VERSION resources, want 1", len(version))
	}
	root := parseVersionNode(t, version[1], 0)
	values := readVersionStrings(t, root)

	// The distribution was built from this checkout by the documented script,
	// so the version it reports is the version the rule derives here. Comparing
	// the two is what makes this an assertion about the artifact instead of a
	// snapshot of one commit.
	expected := distributionVersion(t)
	if got := values["FileVersion"]; got != expected {
		t.Errorf("FileVersion = %q, but the version rule reports %q for this checkout", got, expected)
	}
	if values["ProductVersion"] != values["FileVersion"] {
		t.Errorf("ProductVersion = %q and FileVersion = %q, but both come from one version", values["ProductVersion"], values["FileVersion"])
	}
	for _, want := range []struct{ key, value string }{
		{"CompanyName", branding.Company},
		{"ProductName", branding.Product},
		{"FileDescription", branding.DisplayName},
		{"OriginalFilename", branding.WindowsExecutable},
	} {
		if got := values[want.key]; got != want.value {
			t.Errorf("version string %s = %q, want %q", want.key, got, want.value)
		}
	}
	// A development distribution must be distinguishable from a release one in
	// the artifact itself, not only in the build log: wherever the rule reports
	// a pre-release identifier, the resource has to report one too.
	if strings.Contains(expected, "-dev") && !strings.Contains(values["FileVersion"], "-dev") {
		t.Errorf("the version rule reports the development version %q, but the resource reports %q", expected, values["FileVersion"])
	}
	// VS_FIXEDFILEINFO cannot hold a pre-release suffix, so the numeric fields
	// are the numeric part of the same version rather than a second number.
	numeric := branding.NumericVersionOf(expected)
	if got, want := binary.LittleEndian.Uint32(root.value[8:12]), uint32(numeric[0])<<16|uint32(numeric[1]); got != want {
		t.Errorf("dwFileVersionMS = %#x, want %#x from %v", got, want, numeric)
	}
	if got, want := binary.LittleEndian.Uint32(root.value[12:16]), uint32(numeric[2])<<16|uint32(numeric[3]); got != want {
		t.Errorf("dwFileVersionLS = %#x, want %#x from %v", got, want, numeric)
	}
}

// distributionVersion asks the version rule what this checkout builds as,
// instead of pinning the assertion to one commit's identifier.
func distributionVersion(t *testing.T) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("the go toolchain is not on PATH")
	}
	command := exec.Command(goTool, "run", "./cmd/radanor-version")
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local")
	out, err := command.Output()
	if err != nil {
		t.Fatalf("the version rule could not be run: %v", err)
	}
	if version := strings.TrimSpace(string(out)); version != "" {
		return version
	}
	t.Fatal("the version rule printed nothing")
	return ""
}

// resourcesOfType collects the resources filed under one type.
func resourcesOfType(entries []Entry, kind uint32) map[uint32][]byte {
	out := map[uint32][]byte{}
	for _, entry := range entries {
		if entry.Type == kind {
			out[entry.Name] = entry.Data
		}
	}
	return out
}

// assertIconResources checks the icon half of a built executable's resource
// tree: every image of the approved artwork, byte for byte, and a group icon
// that names them.
func assertIconResources(t *testing.T, entries []Entry, images []IconImage) {
	t.Helper()
	icons, group := resourcesOfType(entries, resourceTypeIcon), resourcesOfType(entries, resourceTypeGroupIcon)
	if len(icons) != len(images) {
		t.Errorf("the executable carries %d RT_ICON resources, want %d", len(icons), len(images))
	}
	for index, image := range images {
		got := icons[uint32(index+1)]
		if got == nil {
			t.Errorf("RT_ICON %d is missing", index+1)
			continue
		}
		if !bytes.Equal(got, image.Data) {
			t.Errorf("RT_ICON %d (%d bytes) is not the approved image byte for byte (%d bytes)", index+1, len(got), len(image.Data))
		}
	}
	if len(group) != 1 {
		t.Fatalf("the executable carries %d RT_GROUP_ICON resources, want 1", len(group))
	}
	directory := group[1]
	if len(directory) != 6+14*len(images) {
		t.Fatalf("the group icon is %d bytes, want %d", len(directory), 6+14*len(images))
	}
	if reserved, kind, count := binary.LittleEndian.Uint16(directory[0:2]), binary.LittleEndian.Uint16(directory[2:4]), binary.LittleEndian.Uint16(directory[4:6]); reserved != 0 || kind != 1 || int(count) != len(images) {
		t.Errorf("the group icon header is (%d, %d, %d), want (0, 1, %d)", reserved, kind, count, len(images))
	}
	for index, image := range images {
		at := 6 + index*14
		if id := binary.LittleEndian.Uint16(directory[at+12 : at+14]); int(id) != index+1 {
			t.Errorf("group entry %d names RT_ICON %d, want %d", index, id, index+1)
		}
		if size := binary.LittleEndian.Uint32(directory[at+8 : at+12]); int(size) != len(image.Data) {
			t.Errorf("group entry %d declares %d bytes, want %d", index, size, len(image.Data))
		}
	}
}

// assertApprovedResources checks a built executable's resource tree against the
// approved artwork and the expected version, reading both from their real
// source rather than from the builder's own intermediate values.
func assertApprovedResources(t *testing.T, entries []Entry, options Options) {
	t.Helper()
	images, err := ParseICO(approvedIcon(t))
	if err != nil {
		t.Fatal(err)
	}
	assertIconResources(t, entries, images)
	version := resourcesOfType(entries, resourceTypeVersion)
	if len(version) != 1 {
		t.Fatalf("the executable carries %d RT_VERSION resources, want 1", len(version))
	}
	root := parseVersionNode(t, version[1], 0)
	if root.key != "VS_VERSION_INFO" {
		t.Fatalf("the version resource root is %q", root.key)
	}
	if got := binary.LittleEndian.Uint32(root.value[8:12]); got != uint32(options.Numeric[0])<<16|uint32(options.Numeric[1]) {
		t.Errorf("the executable's numeric version is %#x, want %#x from %v", got, uint32(options.Numeric[0])<<16|uint32(options.Numeric[1]), options.Numeric)
	}
	values := readVersionStrings(t, root)
	for _, want := range []struct{ key, value string }{
		{"CompanyName", options.Company},
		{"ProductName", options.Product},
		{"FileDescription", options.Description},
		{"OriginalFilename", options.OriginalFilename},
		{"FileVersion", options.Version},
		{"ProductVersion", options.Version},
	} {
		if got := values[want.key]; got != want.value {
			t.Errorf("version string %s = %q, want %q", want.key, got, want.value)
		}
	}
	// LegalCopyright must be exactly what the caller supplied, and must be
	// absent when the caller supplied nothing, so an empty option can never
	// become a blank or wrong attribution in a user-visible field.
	if got, present := values["LegalCopyright"]; options.Copyright == "" {
		if present {
			t.Errorf("the version resource asserts LegalCopyright %q although the caller supplied none", got)
		}
	} else if got != options.Copyright {
		t.Errorf("the version resource asserts LegalCopyright %q, want %q", got, options.Copyright)
	}
	// A development build must say so where a user can see it.
	if !strings.Contains(values["ProductVersion"], "dev") {
		t.Errorf("ProductVersion = %q does not identify this as a development build", values["ProductVersion"])
	}
}

func readVersionStrings(t *testing.T, root parsedVersionNode) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, child := range root.children {
		if child.key != "StringFileInfo" {
			continue
		}
		for _, table := range child.children {
			for _, entry := range table.children {
				out[entry.key] = decodeUTF16(entry.rawValue)
			}
		}
	}
	return out
}

// A resource blob must start four-byte aligned, because every length and
// padding rule in the version resource is measured from the structure's start.
func TestResourceDataKeepsStructuresAligned(t *testing.T) {
	options := sampleOptions(t, "amd64")
	images, err := ParseICO(options.Icon)
	if err != nil {
		t.Fatal(err)
	}
	var resources []resource
	for index, image := range images {
		resources = append(resources, resource{kind: resourceTypeIcon, name: uint32(index + 1), data: image.Data})
	}
	resources = append(resources, resource{kind: resourceTypeVersion, name: 1, data: buildVersionInfo(options.Numeric, options.versionStrings())})
	_, data, fixups := buildResources(resources)
	for _, item := range fixups {
		if item.addend%4 != 0 {
			t.Errorf("a resource starts at offset %d in the data section, which is not four-byte aligned", item.addend)
		}
	}
	if len(data)%4 != 0 {
		t.Errorf("the data section is %d bytes, which is not a multiple of four", len(data))
	}
}
