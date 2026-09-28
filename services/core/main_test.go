package main

import "testing"

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantCmd   command
		wantSteps int
		wantErr   bool
	}{
		{name: "nessun argomento", args: nil, wantCmd: cmdServe},
		{name: "serve esplicito", args: []string{"serve"}, wantCmd: cmdServe},
		{name: "migrate senza sottocomando", args: []string{"migrate"}, wantCmd: cmdMigrateUp},
		{name: "migrate up", args: []string{"migrate", "up"}, wantCmd: cmdMigrateUp},
		{name: "migrate down senza N", args: []string{"migrate", "down"}, wantCmd: cmdMigrateDown, wantSteps: 1},
		{name: "migrate down con N", args: []string{"migrate", "down", "3"}, wantCmd: cmdMigrateDown, wantSteps: 3},
		{name: "migrate down N non valido", args: []string{"migrate", "down", "abc"}, wantErr: true},
		{name: "migrate down N negativo", args: []string{"migrate", "down", "-1"}, wantErr: true},
		{name: "migrate sottocomando sconosciuto", args: []string{"migrate", "sideways"}, wantErr: true},
		{name: "comando sconosciuto", args: []string{"bogus"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, steps, err := parseCommand(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("attendevo un errore")
				}
				return
			}
			if err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			if cmd != tc.wantCmd {
				t.Fatalf("cmd = %v, voluto %v", cmd, tc.wantCmd)
			}
			if tc.wantCmd == cmdMigrateDown && steps != tc.wantSteps {
				t.Fatalf("steps = %d, voluto %d", steps, tc.wantSteps)
			}
		})
	}
}
