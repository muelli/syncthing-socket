package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"io"

	"github.com/coreos/go-systemd/v22/journal"
)

const LevelTrace = slog.Level(-8)

// TextHandler is the custom handler for console output.
type TextHandler struct {
	level slog.Level
}

func (h *TextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *TextHandler) Handle(ctx context.Context, r slog.Record) error {
	var buf bytes.Buffer

	buf.WriteString(r.Time.Format("2006-01-02 15:04:05.000"))
	buf.WriteString(" ")

	levelStr := r.Level.String()
	if r.Level <= LevelTrace {
		levelStr = "TRACE"
	}
	buf.WriteString("[")
	buf.WriteString(levelStr)
	buf.WriteString("] ")

	buf.WriteString(r.Message)

	r.Attrs(func(a slog.Attr) bool {
		buf.WriteString(fmt.Sprintf(" %s=%v", a.Key, a.Value.Any()))
		return true
	})

	buf.WriteString("\n")
	_, err := os.Stderr.WriteString(buf.String())
	return err
}

func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *TextHandler) WithGroup(name string) slog.Handler       { return h }

// JournaldHandler writes structured logs to the systemd journal.
type JournaldHandler struct {
	level slog.Level
}

func (h *JournaldHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *JournaldHandler) Handle(ctx context.Context, r slog.Record) error {
	var pri journal.Priority
	switch {
	case r.Level <= LevelTrace:
		pri = journal.PriDebug
	case r.Level < slog.LevelInfo:
		pri = journal.PriDebug
	case r.Level < slog.LevelWarn:
		pri = journal.PriInfo
	case r.Level < slog.LevelError:
		pri = journal.PriWarning
	default:
		pri = journal.PriErr
	}

	vars := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool {
		key := strings.ToUpper(a.Key)
		// Basic sanitization: journald keys must be uppercase alphanumeric and underscores
		key = strings.Map(func(r rune) rune {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return '_'
		}, key)
		vars[key] = fmt.Sprintf("%v", a.Value.Any())
		return true
	})

	// Add the level as a string for clarity
	if r.Level <= LevelTrace {
		vars["LOG_LEVEL"] = "TRACE"
	} else {
		vars["LOG_LEVEL"] = r.Level.String()
	}

	return journal.Send(r.Message, pri, vars)
}

func (h *JournaldHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *JournaldHandler) WithGroup(name string) slog.Handler       { return h }

func setupLogging(levelStr, formatStr string) {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "trace":
		level = LevelTrace
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	if formatStr == "auto" {
		if os.Getenv("INVOCATION_ID") != "" || os.Getenv("JOURNAL_STREAM") != "" {
			formatStr = "journald"
		} else {
			formatStr = "text"
		}
	}

	var handler slog.Handler
	if strings.ToLower(formatStr) == "json" {
		opts := &slog.HandlerOptions{
			Level: level,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.LevelKey {
					l := a.Value.Any().(slog.Level)
					if l <= LevelTrace {
						return slog.String(slog.LevelKey, "TRACE")
					}
				}
				return a
			},
		}
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else if strings.ToLower(formatStr) == "journald" && journal.Enabled() {
		handler = &JournaldHandler{level: level}
	} else {
		handler = &TextHandler{level: level}
	}

	slog.SetDefault(slog.New(handler))
}

// CopyWithTrace performs io.Copy but logs all transferred bytes at TRACE level.
func CopyWithTrace(dst io.Writer, src io.Reader, direction string) (written int64, err error) {
	buf := make([]byte, 32*1024)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			slog.Log(context.Background(), LevelTrace, "IO read", "direction", direction, "bytes", nr)
			if slog.Default().Handler().Enabled(context.Background(), LevelTrace) {
				slog.Log(context.Background(), LevelTrace, "IO data", "direction", direction, "hex", fmt.Sprintf("%x", buf[:nr]))
			}
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = io.ErrShortWrite
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}
