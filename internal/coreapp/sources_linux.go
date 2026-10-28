//go:build linux

package coreapp

import (
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/sources/filewatch"
)

func nativeSources() ([]monitoring.Source, error) {
	source, err := filewatch.Default()
	if err != nil {
		return nil, err
	}
	return []monitoring.Source{source}, nil
}
