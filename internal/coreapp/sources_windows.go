//go:build windows

package coreapp

import (
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/sources/filewatch"
	"github.com/radanor/radanor-shield/internal/sources/windowseventlog"
)

func nativeSources() ([]monitoring.Source, error) {
	sources := []monitoring.Source{windowseventlog.New()}
	files, err := filewatch.Default()
	if err == nil {
		sources = append(sources, files)
	}
	return sources, err
}
