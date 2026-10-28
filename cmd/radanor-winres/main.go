// Command radanor-winres writes the Windows resource object for the
// distribution executable: the approved Radanor icon and the product's version
// information, as the `.syso` file the Go linker folds into the executable.
//
// It is the same shape as the licensing gate — a thin command over an internal
// package — so the rule lives in one tested place rather than in the build
// scripts. It is pure Go and needs no Resource Compiler, so the Windows
// resource is produced identically on the Linux and Windows build paths.
//
// The output path is where the linker expects it: a `.syso` file in the main
// package's directory whose name ends in `_windows` (optionally `_windows_arch`)
// is linked only into a Windows build, so the Linux build is unaffected.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/winres"
)

func main() {
	out := flag.String("out", "", "path of the COFF resource object to write")
	iconPath := flag.String("ico", filepath.Join("web", "public", "favicon.ico"), "approved .ico file, copied byte for byte")
	architecture := flag.String("arch", "amd64", "target architecture: amd64 or arm64")
	version := flag.String("version", "", "product version for the resource; defaults to the version this build carries")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "radanor-winres: -out is required")
		os.Exit(2)
	}
	if *version == "" {
		*version = branding.Semver()
	}

	icon, err := winres.ReadIconFile(*iconPath)
	if err != nil {
		fail(err)
	}
	images, err := winres.ParseICO(icon)
	if err != nil {
		fail(fmt.Errorf("%s: %w", *iconPath, err))
	}

	// The numeric fields are derived from the version this build actually
	// carries, not from the version the source default holds, so the resource's
	// numbers and its version strings describe the same build.
	object, err := winres.Build(winres.Options{
		Icon:             icon,
		Architecture:     *architecture,
		Company:          branding.Company,
		Product:          branding.Product,
		Description:      branding.DisplayName,
		Version:          *version,
		Numeric:          branding.NumericVersionOf(*version),
		OriginalFilename: branding.WindowsExecutable,
		// The holder the licence names, so a file's Properties dialog and the
		// LICENSE cannot disagree. An empty value would omit the field.
		Copyright: branding.CopyrightNotice,
	})
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, object, 0o644); err != nil {
		fail(err)
	}

	sizes := make([]int, 0, len(images))
	for _, image := range images {
		sizes = append(sizes, len(image.Data))
	}
	fmt.Printf("Windows resources: %d icon images %v, version %s (%s build), %d bytes -> %s\n",
		len(images), sizes, *version, branding.Variant, len(object), *out)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "radanor-winres: %v\n", err)
	os.Exit(1)
}
