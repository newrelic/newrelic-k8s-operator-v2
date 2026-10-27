package main

import "testing"

func TestResolveDefaultAccountID(t *testing.T) {
	tests := []struct {
		name    string
		flagVal string
		env     map[string]string
		want    int
		wantErr bool
	}{
		{name: "neither set disables the default", want: 0},
		{name: "flag only", flagVal: "123", want: 123},
		{name: "env only", env: map[string]string{"NEW_RELIC_ACCOUNT_ID": "456"}, want: 456},
		{name: "flag wins over env", flagVal: "123", env: map[string]string{"NEW_RELIC_ACCOUNT_ID": "456"}, want: 123},
		{name: "flag trimmed", flagVal: "  123\n", want: 123},
		{name: "env trimmed", env: map[string]string{"NEW_RELIC_ACCOUNT_ID": " 456 \n"}, want: 456},
		{
			name:    "whitespace-only flag falls back to env",
			flagVal: "   ",
			env:     map[string]string{"NEW_RELIC_ACCOUNT_ID": "456"},
			want:    456,
		},
		{name: "zero flag disables", flagVal: "0", want: 0},
		{name: "zero env disables", env: map[string]string{"NEW_RELIC_ACCOUNT_ID": "0"}, want: 0},
		{name: "non-numeric flag", flagVal: "abc", wantErr: true},
		{name: "non-numeric env", env: map[string]string{"NEW_RELIC_ACCOUNT_ID": "abc"}, wantErr: true},
		{name: "decimal flag", flagVal: "12.5", wantErr: true},
		{name: "negative flag", flagVal: "-1", wantErr: true},
		{name: "negative env", env: map[string]string{"NEW_RELIC_ACCOUNT_ID": "-7"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }

			got, err := resolveDefaultAccountID(tt.flagVal, getenv)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
