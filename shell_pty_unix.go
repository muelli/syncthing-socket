//go:build !windows

package main

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixShell struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func (s *unixShell) Read(p []byte) (n int, err error) { return s.ptmx.Read(p) }
func (s *unixShell) Write(p []byte) (n int, err error) { return s.ptmx.Write(p) }
func (s *unixShell) Close() error { return s.ptmx.Close() }
func (s *unixShell) Wait() error { return s.cmd.Wait() }
func (s *unixShell) Resize(rows, cols uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

func spawnShell() (ShellSession, error) {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	cmd := exec.Command(shellPath)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixShell{ptmx: ptmx, cmd: cmd}, nil
}
