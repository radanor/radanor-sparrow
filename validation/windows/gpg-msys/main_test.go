//go:build windows

package main

import "testing"

func TestOnlyAbsoluteWindowsPathsAreTranslated(t *testing.T) {
	for input, want := range map[string]string{
		`C:\Users\Test Name\keys`: "/c/Users/Test Name/keys",
		`D:/Temp/artifact.asc`:    "/d/Temp/artifact.asc",
		`\\server\share\keys`:     "//server/share/keys",
		"/tmp/keys":               "/tmp/keys",
		"--export-secret-keys":    "--export-secret-keys",
		"0123456789ABCDEF":        "0123456789ABCDEF",
		"C:relative":              "C:relative",
	} {
		if got := posixPath(input); got != want {
			t.Errorf("posixPath(%q)=%q, want %q", input, got, want)
		}
	}
}
