//go:build windows

package main

import (
	"io"
	"os"
	"os/exec"
)

type windowsShell struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (s *windowsShell) Read(p []byte) (n int, err error) { return s.stdout.Read(p) }
func (s *windowsShell) Write(p []byte) (n int, err error) { return s.stdin.Write(p) }
func (s *windowsShell) Close() error {
	s.stdin.Close()
	s.stdout.Close()
	return nil
}
func (s *windowsShell) Wait() error { return s.cmd.Wait() }
func (s *windowsShell) Resize(rows, cols uint16) error {
	// Not supported on basic Windows pipes
	return nil
}

func spawnShell() (ShellSession, error) {
	shellPath := os.Getenv("COMSPEC")
	if shellPath == "" {
		shellPath = "cmd.exe"
	}
	cmd := exec.Command(shellPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // Merge stderr and stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &windowsShell{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}
