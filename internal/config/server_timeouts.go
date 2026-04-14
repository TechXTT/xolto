package config

import "time"

const (
	defaultServerReadTimeout       = 15 * time.Second
	defaultServerWriteTimeout      = 30 * time.Second
	defaultServerIdleTimeout       = 60 * time.Second
	defaultServerReadHeaderTimeout = 5 * time.Second
)

type HTTPTimeouts struct {
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
}

func DefaultHTTPTimeouts() HTTPTimeouts {
	return HTTPTimeouts{
		ReadTimeout:       defaultServerReadTimeout,
		WriteTimeout:      defaultServerWriteTimeout,
		IdleTimeout:       defaultServerIdleTimeout,
		ReadHeaderTimeout: defaultServerReadHeaderTimeout,
	}
}

func (t HTTPTimeouts) withDefaults() HTTPTimeouts {
	defaults := DefaultHTTPTimeouts()
	if t.ReadTimeout <= 0 {
		t.ReadTimeout = defaults.ReadTimeout
	}
	if t.WriteTimeout <= 0 {
		t.WriteTimeout = defaults.WriteTimeout
	}
	if t.IdleTimeout <= 0 {
		t.IdleTimeout = defaults.IdleTimeout
	}
	if t.ReadHeaderTimeout <= 0 {
		t.ReadHeaderTimeout = defaults.ReadHeaderTimeout
	}
	return t
}
