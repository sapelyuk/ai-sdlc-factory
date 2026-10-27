package config

import "testing"

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "DOCKER_HOST", "RUNTIME", "DATABASE_URL", "SANDBOX_NETWORK",
		"SANDBOX_IMAGE", "SANDBOX_DISK_LIMIT_BYTES",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadRequiresDockerHost(t *testing.T) {
	clearConfigEnv(t)
	if _, err := Load(); err == nil {
		t.Fatal("Load() should require DOCKER_HOST")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DOCKER_HOST", "tcp://docker-socket-proxy:2375")
	t.Setenv("DATABASE_URL", "postgres://user:pass@postgres:5432/db")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8060 || cfg.Runtime != "runc" {
		t.Fatalf("unexpected defaults: port=%d runtime=%s", cfg.Port, cfg.Runtime)
	}
	if cfg.DiskLimitBytes != 2*1024*1024*1024 {
		t.Fatalf("disk limit = %d", cfg.DiskLimitBytes)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DOCKER_HOST", "tcp://proxy:2375")
	t.Setenv("DATABASE_URL", "postgres://user:pass@postgres:5432/db")
	t.Setenv("PORT", "8061")
	t.Setenv("RUNTIME", "runsc")
	t.Setenv("SANDBOX_DISK_LIMIT_BYTES", "1073741824")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8061 || cfg.Runtime != "runsc" || cfg.DiskLimitBytes != 1073741824 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid port", key: "PORT", value: "not-a-port"},
		{name: "out of range port", key: "PORT", value: "65536"},
		{name: "invalid runtime", key: "RUNTIME", value: "kata"},
		{name: "invalid disk limit", key: "SANDBOX_DISK_LIMIT_BYTES", value: "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("DOCKER_HOST", "tcp://proxy:2375")
			t.Setenv("DATABASE_URL", "postgres://user:pass@postgres:5432/db")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() should reject invalid configuration")
			}
		})
	}
}
