//go:build !linux && !windows

package coreapp

import (
	"github.com/radanor/radanor-shield/internal/monitoring"
)

func nativeSources() ([]monitoring.Source, error) { return nil, monitoring.ErrUnsupported }
