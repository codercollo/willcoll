package main

import "testing"

func TestCheckDevEnv(t *testing.T) {
	cases := []struct {
		env     string
		wantErr bool
	}{
		{env: "", wantErr: true},
		{env: "development", wantErr: false},
		{env: "staging", wantErr: true},
		{env: "production", wantErr: true},
	}
	for _, c := range cases {
		err := checkDevEnv(c.env)
		if (err != nil) != c.wantErr {
			t.Errorf("checkDevEnv(%q): got err=%v, want error=%v", c.env, err, c.wantErr)
		}
	}
}
