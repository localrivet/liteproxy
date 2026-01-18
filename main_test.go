package main

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback string
		want     string
	}{
		{
			name:     "returns env value when set",
			key:      "TEST_VAR",
			envValue: "custom",
			fallback: "default",
			want:     "custom",
		},
		{
			name:     "returns fallback when not set",
			key:      "TEST_VAR_UNSET",
			envValue: "",
			fallback: "default",
			want:     "default",
		},
		{
			name:     "returns empty string env over fallback",
			key:      "TEST_VAR_EMPTY",
			envValue: "",
			fallback: "default",
			want:     "default", // empty string means not set
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}
			got := getEnv(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("getEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback int
		want     int
	}{
		{
			name:     "parses valid int",
			key:      "TEST_INT",
			envValue: "8080",
			fallback: 80,
			want:     8080,
		},
		{
			name:     "returns fallback for invalid int",
			key:      "TEST_INT_INVALID",
			envValue: "not-a-number",
			fallback: 80,
			want:     80,
		},
		{
			name:     "returns fallback when not set",
			key:      "TEST_INT_UNSET",
			envValue: "",
			fallback: 443,
			want:     443,
		},
		{
			name:     "parses zero",
			key:      "TEST_INT_ZERO",
			envValue: "0",
			fallback: 80,
			want:     0,
		},
		{
			name:     "parses negative",
			key:      "TEST_INT_NEG",
			envValue: "-1",
			fallback: 80,
			want:     -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}
			got := getEnvInt(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		fallback bool
		want     bool
	}{
		{
			name:     "true string",
			key:      "TEST_BOOL",
			envValue: "true",
			fallback: false,
			want:     true,
		},
		{
			name:     "1 string",
			key:      "TEST_BOOL",
			envValue: "1",
			fallback: false,
			want:     true,
		},
		{
			name:     "yes string",
			key:      "TEST_BOOL",
			envValue: "yes",
			fallback: false,
			want:     true,
		},
		{
			name:     "false string",
			key:      "TEST_BOOL",
			envValue: "false",
			fallback: true,
			want:     false,
		},
		{
			name:     "0 string",
			key:      "TEST_BOOL",
			envValue: "0",
			fallback: true,
			want:     false,
		},
		{
			name:     "random string is false",
			key:      "TEST_BOOL",
			envValue: "random",
			fallback: true,
			want:     false,
		},
		{
			name:     "returns fallback when not set",
			key:      "TEST_BOOL_UNSET",
			envValue: "",
			fallback: true,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			} else {
				os.Unsetenv(tt.key)
			}
			got := getEnvBool(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvBool() = %v, want %v", got, tt.want)
			}
		})
	}
}
