//go:build !linux && !windows

package filewatch

import (
	"context"
	"github.com/radanor/radanor-shield/internal/monitoring"
)

func (s *Source) Run(context.Context, []byte, monitoring.EmitFunc, monitoring.Reporter) error {
	return monitoring.ErrUnsupported
}
