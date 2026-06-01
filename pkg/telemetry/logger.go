package telemetry

import (
	"io"
	"os"
	"sync"

	"github.com/chainreactors/logs"
)

type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Importantf(format string, args ...any)
}

type LogConfig struct {
	Debug  bool
	Quiet  bool
	Output io.Writer
	Color  bool
}

type logsLogger struct {
	base *logs.Logger
}

func NewLogger(cfg LogConfig) Logger {
	level := logs.WarnLevel
	if cfg.Debug {
		level = logs.DebugLevel
	} else if cfg.Quiet {
		level = logs.ErrorLevel
	}
	base := logs.NewLogger(level)
	if cfg.Output != nil {
		base.SetOutput(cfg.Output)
	} else {
		base.SetOutput(os.Stderr)
	}
	base.SetFormatter(map[logs.Level]string{
		logs.DebugLevel:     "[debug] %s\n",
		logs.InfoLevel:      "[info] %s\n",
		logs.WarnLevel:      "[warn] %s\n",
		logs.ErrorLevel:     "[error] %s\n",
		logs.ImportantLevel: "[info] %s\n",
	})
	base.SetColor(cfg.Color)
	return logsLogger{base: base}
}

func GlobalLogger(cfg LogConfig) Logger {
	logger := NewLogger(cfg)
	if adapter, ok := logger.(logsLogger); ok {
		logs.Log = adapter.base
	}
	return logger
}

func GlobalLogs() *logs.Logger {
	if logs.Log == nil {
		logs.Log = logs.NewLogger(logs.WarnLevel)
		logs.Log.SetOutput(os.Stderr)
	}
	return logs.Log
}

var enableDebugOnce sync.Once

func EnableLogsDebug() *logs.Logger {
	logger := GlobalLogs()
	enableDebugOnce.Do(func() {
		logger.SetLevel(logs.DebugLevel)
		logger.SetQuiet(false)
	})
	return logger
}

func SuppressGlobalNonErrors() func() {
	logger := GlobalLogs()
	oldLevel := logger.Level
	oldQuiet := logger.Quiet
	logger.SetLevel(logs.ErrorLevel)
	logger.SetQuiet(false)
	return func() {
		logger.SetLevel(oldLevel)
		logger.SetQuiet(oldQuiet)
	}
}

func ActivateDebug(logger Logger) func() {
	oldGlobal := logs.Log
	target := oldGlobal
	if adapter, ok := logger.(logsLogger); ok && adapter.base != nil {
		target = adapter.base
	}
	if target == nil {
		target = GlobalLogs()
	}
	if oldGlobal == nil {
		oldGlobal = target
	}

	oldLevel := target.Level
	oldQuiet := target.Quiet
	target.SetLevel(logs.DebugLevel)
	target.SetQuiet(false)
	logs.Log = target

	return func() {
		target.SetLevel(oldLevel)
		target.SetQuiet(oldQuiet)
		logs.Log = oldGlobal
	}
}

func NopLogger() Logger {
	return nopLogger{}
}

func ErrorOnlyLogger(logger Logger) Logger {
	if logger == nil {
		return NopLogger()
	}
	return errorOnlyLogger{base: logger}
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any)     {}
func (nopLogger) Infof(string, ...any)      {}
func (nopLogger) Warnf(string, ...any)      {}
func (nopLogger) Errorf(string, ...any)     {}
func (nopLogger) Importantf(string, ...any) {}

type errorOnlyLogger struct {
	base Logger
}

func (errorOnlyLogger) Debugf(string, ...any) {}
func (errorOnlyLogger) Infof(string, ...any)  {}
func (errorOnlyLogger) Warnf(string, ...any)  {}
func (l errorOnlyLogger) Errorf(format string, args ...any) {
	l.base.Errorf(format, args...)
}
func (errorOnlyLogger) Importantf(string, ...any) {}

func (l logsLogger) Debugf(format string, args ...any) {
	l.base.Debugf(format, args...)
}

func (l logsLogger) Infof(format string, args ...any) {
	l.base.Infof(format, args...)
}

func (l logsLogger) Warnf(format string, args ...any) {
	l.base.Warnf(format, args...)
}

func (l logsLogger) Errorf(format string, args ...any) {
	l.base.Errorf(format, args...)
}

func (l logsLogger) Importantf(format string, args ...any) {
	l.base.Importantf(format, args...)
}
