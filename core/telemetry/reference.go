package telemetry

import "sync"

// LoggerRef forwards to one replaceable logger shared by an installation.
// Its zero value discards logs.
type LoggerRef struct {
	mu     sync.RWMutex
	logger Logger
}

func NewLoggerRef(logger Logger) *LoggerRef {
	r := &LoggerRef{}
	r.Set(logger)
	return r
}

func (r *LoggerRef) Set(logger Logger) {
	if r == nil {
		return
	}
	if other, ok := logger.(*LoggerRef); ok && other == r {
		return
	}
	r.mu.Lock()
	r.logger = logger
	r.mu.Unlock()
}

func (r *LoggerRef) current() Logger {
	if r == nil {
		return NopLogger()
	}
	r.mu.RLock()
	logger := r.logger
	r.mu.RUnlock()
	if logger == nil {
		return NopLogger()
	}
	return logger
}

func (r *LoggerRef) Debugf(format string, args ...any) { r.current().Debugf(format, args...) }

func (r *LoggerRef) Infof(format string, args ...any) { r.current().Infof(format, args...) }

func (r *LoggerRef) Warnf(format string, args ...any) { r.current().Warnf(format, args...) }

func (r *LoggerRef) Errorf(format string, args ...any) { r.current().Errorf(format, args...) }

func (r *LoggerRef) Importantf(format string, args ...any) { r.current().Importantf(format, args...) }
