// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Distributed under the GNU General Public License v3.

//go:build linux

// Copied verbatim from examples/MindSentencesExtension/parport_linux.go (the
// convention that was validated on the NeuroSpin MEG). Original notes:
//
// Self-contained Linux parallel-port access for MindSentences, replicating the
// pyparallel behaviour used by the original mind_sentences.py / meg_forp_buttons.py:
//
//   - triggers : write the data register (PPWDATA), like pyparallel's setData().
//   - buttons  : read the status register (PPRSTATUS), like pyparallel's PPRSTATUS.
//
// This deliberately does NOT use goxpyriment/triggers.ParallelPort, whose ppdev
// ioctl command numbers are wrong (PPCLAIM/PPWDATA/PPRSTATUS), which makes the
// port fail to open on the MEG. The constants below are taken verbatim from
// <linux/ppdev.h> and verified against the kernel header.
package main

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"unsafe"
)

// ppdev ioctl numbers from <linux/ppdev.h> (PP_IOCTL = 'p' = 0x70):
//
//	_IO(type,nr)      = (type<<8)|nr
//	_IOW(type,nr,u8)  = (1<<30)|(1<<16)|(type<<8)|nr
//	_IOR(type,nr,u8)  = (2<<30)|(1<<16)|(type<<8)|nr
const (
	ppClaim   uintptr = 0x0000708B // PPCLAIM   = _IO('p', 0x8b)      — claim exclusive access
	ppRelease uintptr = 0x0000708C // PPRELEASE = _IO('p', 0x8c)      — release
	ppWData   uintptr = 0x40017086 // PPWDATA   = _IOW('p', 0x86, u8) — write data register
	ppRStatus uintptr = 0x80017081 // PPRSTATUS = _IOR('p', 0x81, u8) — read status register
	ppDataDir uintptr = 0x40047090 // PPDATADIR = _IOW('p', 0x90, int) — set data-line direction
)

// parPort is an open, claimed /dev/parportN device.
type parPort struct {
	f *os.File
}

// openParPort opens and claims a parallel port (e.g. "/dev/parport1").
func openParPort(device string) (*parPort, error) {
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", device, err)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ppClaim, 0); errno != 0 {
		f.Close()
		return nil, fmt.Errorf("PPCLAIM %s: %w", device, errno)
	}
	p := &parPort{f: f}
	// Force the 8 data lines to OUTPUT (forward) mode. After PPCLAIM the port may
	// come up in reverse/input mode (bidirectional ports), in which case writes
	// don't drive the pins and STI101 sees no trigger. 0 = forward (output).
	if err := p.setDataDir(false); err != nil {
		log.Printf("WARNING: %s PPDATADIR forward failed: %v (triggers may not drive the pins)", device, err)
	}
	_ = p.SetData(0) // start with all data lines LOW
	return p, nil
}

// setDataDir sets the data-register direction. reverse=false → forward (output,
// drives the pins, needed for triggers); reverse=true → input (tri-state).
func (p *parPort) setDataDir(reverse bool) error {
	var v int32
	if reverse {
		v = 1
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, p.f.Fd(), ppDataDir,
		uintptr(unsafe.Pointer(&v))); errno != 0 {
		return fmt.Errorf("PPDATADIR: %w", errno)
	}
	return nil
}

// SetData writes a byte to the data register (the 8 trigger lines). Like
// pyparallel's pp.setData(value).
func (p *parPort) SetData(value byte) error {
	if p.f == nil {
		return fmt.Errorf("parport: not open")
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, p.f.Fd(), ppWData,
		uintptr(unsafe.Pointer(&value))); errno != 0 {
		return fmt.Errorf("PPWDATA: %w", errno)
	}
	return nil
}

// Status reads the status register (the FORP button lines). Like pyparallel's
// pp.PPRSTATUS().
func (p *parPort) Status() (byte, error) {
	if p.f == nil {
		return 0, fmt.Errorf("parport: not open")
	}
	var v byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, p.f.Fd(), ppRStatus,
		uintptr(unsafe.Pointer(&v))); errno != 0 {
		return 0, fmt.Errorf("PPRSTATUS: %w", errno)
	}
	return v, nil
}

// Close lowers all data lines, releases the port and closes the device.
func (p *parPort) Close() {
	if p.f == nil {
		return
	}
	_ = p.SetData(0)
	syscall.Syscall(syscall.SYS_IOCTL, p.f.Fd(), ppRelease, 0)
	_ = p.f.Close()
	p.f = nil
}
