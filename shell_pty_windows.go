//go:build windows

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"sync"
)

type windowsShell struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	
	outReader *io.PipeReader
	outWriter *io.PipeWriter
	outMutex  sync.Mutex
}

func (s *windowsShell) Read(p []byte) (n int, err error) {
	return s.outReader.Read(p)
}

func (s *windowsShell) Write(p []byte) (n int, err error) {
	var translatedStdin bytes.Buffer
	var echo bytes.Buffer

	for _, b := range p {
		switch b {
		case '\r', '\n':
			// cmd.exe needs \r\n, and we echo \r\n to the client
			translatedStdin.WriteString("\r\n")
			echo.WriteString("\r\n")
		case '\x7f', '\b':
			// Backspace visually erases the character on the client and sends \b to cmd
			translatedStdin.WriteByte('\b')
			echo.WriteString("\b \b")
		case '\x03': // Ctrl+C
			s.cmd.Process.Kill()
			return len(p), nil
		case '\x04': // Ctrl+D (EOF)
			s.stdin.Close()
			return len(p), nil
		default:
			translatedStdin.WriteByte(b)
			echo.WriteByte(b)
		}
	}

	if translatedStdin.Len() > 0 {
		s.stdin.Write(translatedStdin.Bytes())
	}

	if echo.Len() > 0 {
		s.outMutex.Lock()
		s.outWriter.Write(echo.Bytes())
		s.outMutex.Unlock()
	}

	return len(p), nil
}

func (s *windowsShell) Close() error {
	s.stdin.Close()
	s.stdout.Close()
	s.outWriter.Close()
	if s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}

func (s *windowsShell) Wait() error {
	return s.cmd.Wait()
}

func (s *windowsShell) Resize(rows, cols uint16) error {
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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	shell := &windowsShell{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		outReader: pr,
		outWriter: pw,
	}

	copyFunc := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				shell.outMutex.Lock()
				pw.Write(buf[:n])
				shell.outMutex.Unlock()
			}
			if err != nil {
				break
			}
		}
	}

	go copyFunc(stdout)
	go copyFunc(stderr)

	return shell, nil
}
