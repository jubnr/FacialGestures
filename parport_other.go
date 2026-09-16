// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Distributed under the GNU General Public License v3.

//go:build !linux

// Stub parallel-port implementation for non-Linux builds (macOS / Windows test
// binaries). There is no parallel port, so opening always fails and the program
// falls back to keyboard responses and disabled triggers.
package main

import "fmt"

type parPort struct{}

func openParPort(device string) (*parPort, error) {
	return nil, fmt.Errorf("parallel port not supported on this OS")
}

func (p *parPort) SetData(value byte) error { return nil }
func (p *parPort) Status() (byte, error)    { return 0, nil }
func (p *parPort) Close()                   {}
