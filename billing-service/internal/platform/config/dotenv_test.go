package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFile_DoesNotOverwriteProcessEnv(t *testing.T) {
	t.Setenv("BILLING_DOTENV_PRESET", "from-process")
	t.Setenv("BILLING_DOTENV_NEW", "")
	require.NoError(t, os.Unsetenv("BILLING_DOTENV_NEW"))

	path := filepath.Join(t.TempDir(), ".env.test")
	content := "# comment\r\n" +
		"export BILLING_DOTENV_PRESET=from-file\r\n" +
		"BILLING_DOTENV_NEW=\"quoted value\"\r\n" +
		"BILLING_DOTENV_DUP=first\r\n" +
		"BILLING_DOTENV_DUP=second\r\n" +
		"BILLING_DOTENV_SINGLE='single'\r\n" +
		"NO_EQUALS_LINE\r\n" +
		"\r\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	require.NoError(t, loadFile(path))

	assert.Equal(t, "from-process", os.Getenv("BILLING_DOTENV_PRESET"))
	assert.Equal(t, "quoted value", os.Getenv("BILLING_DOTENV_NEW"))
	assert.Equal(t, "second", os.Getenv("BILLING_DOTENV_DUP"), "last wins inside one file")
	assert.Equal(t, "single", os.Getenv("BILLING_DOTENV_SINGLE"))
}

func TestParseEnvLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantKey string
		wantVal string
		wantOK  bool
	}{
		{name: "plain", line: "KEY=value", wantKey: "KEY", wantVal: "value", wantOK: true},
		{name: "spaces", line: "  KEY = value  ", wantKey: "KEY", wantVal: "value", wantOK: true},
		{name: "export", line: "export KEY=value", wantKey: "KEY", wantVal: "value", wantOK: true},
		{name: "double quotes", line: `KEY="a b"`, wantKey: "KEY", wantVal: "a b", wantOK: true},
		{name: "single quotes", line: "KEY='a b'", wantKey: "KEY", wantVal: "a b", wantOK: true},
		{name: "inline hash is value", line: "KEY=a#b", wantKey: "KEY", wantVal: "a#b", wantOK: true},
		{name: "comment", line: "# KEY=value", wantOK: false},
		{name: "empty", line: "   ", wantOK: false},
		{name: "no equals", line: "KEY", wantOK: false},
		{name: "empty key", line: "=value", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, val, ok := parseEnvLine(tc.line)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantKey, key)
				assert.Equal(t, tc.wantVal, val)
			}
		})
	}
}
