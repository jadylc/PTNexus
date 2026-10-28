package config

import "testing"

func TestParseAndValidateFlareSolverrConfig(t *testing.T) {
	cases := []struct {
		name    string
		raw     any
		wantErr bool
		check   func(t *testing.T, cfg FlareSolverrConfig)
	}{
		{
			name: "nil 回退默认",
			raw:  nil,
			check: func(t *testing.T, cfg FlareSolverrConfig) {
				if cfg.Enabled {
					t.Fatalf("expected disabled by default")
				}
				if cfg.TimeoutSeconds != 90 {
					t.Fatalf("expected default timeout 90, got %d", cfg.TimeoutSeconds)
				}
			},
		},
		{
			name: "启用且补全协议",
			raw: map[string]any{
				"enabled":         true,
				"api_url":         "127.0.0.1:8112",
				"timeout_seconds": float64(30),
			},
			check: func(t *testing.T, cfg FlareSolverrConfig) {
				if !cfg.Enabled {
					t.Fatalf("expected enabled")
				}
				if cfg.APIURL != "http://127.0.0.1:8112" {
					t.Fatalf("expected scheme auto-completed, got %s", cfg.APIURL)
				}
				if cfg.TimeoutSeconds != 30 {
					t.Fatalf("expected timeout 30, got %d", cfg.TimeoutSeconds)
				}
				if cfg.MaxTimeoutMillis != 30000 {
					t.Fatalf("expected max timeout derived, got %d", cfg.MaxTimeoutMillis)
				}
			},
		},
		{
			name: "兼容旧字段名",
			raw: map[string]any{
				"enabled": true,
				"apiUrl":  "http://127.0.0.1:8112/",
				"apiKey":  "secret",
				"url":     "",
			},
			check: func(t *testing.T, cfg FlareSolverrConfig) {
				if cfg.APIURL != "http://127.0.0.1:8112" {
					t.Fatalf("expected trailing slash trimmed, got %s", cfg.APIURL)
				}
				if cfg.APIKey != "secret" {
					t.Fatalf("expected api key preserved, got %s", cfg.APIKey)
				}
			},
		},
		{
			name:    "启用但缺少地址",
			raw:     map[string]any{"enabled": true, "api_url": ""},
			wantErr: true,
		},
		{
			name:    "非法协议",
			raw:     map[string]any{"enabled": true, "api_url": "ftp://127.0.0.1:8112"},
			wantErr: true,
		},
		{
			name:    "缺少主机名",
			raw:     map[string]any{"enabled": true, "api_url": "http://"},
			wantErr: true,
		},
		{
			name:    "超时超限",
			raw:     map[string]any{"enabled": true, "api_url": "http://127.0.0.1:8112", "timeout_seconds": 9999},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ParseFlareSolverrConfig(tc.raw)
			err := ValidateFlareSolverrConfig(cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

func TestFlareSolverrConfigToMap(t *testing.T) {
	cfg := FlareSolverrConfig{
		Enabled:          true,
		APIURL:           "http://127.0.0.1:8112",
		TimeoutSeconds:   120,
		MaxTimeoutMillis: 0,
	}
	normalized := NormalizeFlareSolverrConfig(cfg)
	if normalized.MaxTimeoutMillis != 120000 {
		t.Fatalf("expected max timeout 120000, got %d", normalized.MaxTimeoutMillis)
	}
	mapped := normalized.ToMap()
	if mapped["timeout_seconds"] != 120 {
		t.Fatalf("expected map timeout 120, got %v", mapped["timeout_seconds"])
	}
	if _, ok := mapped["extra_headers"].(map[string]string); !ok {
		t.Fatalf("expected extra_headers map in output")
	}
}
