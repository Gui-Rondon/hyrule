package mongodb

import (
	"testing"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
)

func TestURI(t *testing.T) {
	cases := []struct {
		name     string
		env      string
		cfg      config.Config
		wantURI  string
		wantName string
	}{
		{
			name:     "env wins",
			env:      "mongodb://env:27017/fromenv",
			cfg:      config.Config{"mongo": map[string]any{"uri": "mongodb://cfg/x"}},
			wantURI:  "mongodb://env:27017/fromenv",
			wantName: "fromenv",
		},
		{
			name:     "uri from config",
			cfg:      config.Config{"mongo": map[string]any{"uri": "mongodb://u:p@h1,h2/tags?replicaSet=rs"}},
			wantURI:  "mongodb://u:p@h1,h2/tags?replicaSet=rs",
			wantName: "tags",
		},
		{
			name:     "uri without database uses default",
			cfg:      config.Config{"mongo": map[string]any{"uri": "mongodb://h1/"}},
			wantURI:  "mongodb://h1/",
			wantName: "tags",
		},
		{
			name: "host and base64 password",
			cfg: config.Config{"mongo": map[string]any{
				"host": "localhost", "port": float64(27017), "dbname": "tags",
				"username": "app", "password": "c2VuaGEK", // "senha\n"
			}},
			wantURI:  "mongodb://app:senha@localhost:27017/tags",
			wantName: "tags",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MONGO_CONNECTION_STRING", tc.env)
			uri, name, err := URI(tc.cfg, "tags")
			if err != nil {
				t.Fatal(err)
			}
			if uri != tc.wantURI || name != tc.wantName {
				t.Errorf("got %q %q, want %q %q", uri, name, tc.wantURI, tc.wantName)
			}
		})
	}
}
