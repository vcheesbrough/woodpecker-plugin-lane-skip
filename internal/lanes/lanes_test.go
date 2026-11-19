package lanes

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cfg, err := Parse(DefaultPath, []byte(`
lanes:
  web:
    context: ./Dockerfile.web
    inputs: [e2e/, ./deploy/grafana]
    steps: [e2e-web]
  unit:
    inputs: [crates]
    steps: [cargo-test, cargo-test.release]
`))
	if err != nil {
		t.Fatal(err)
	}
	web := cfg.Lanes["web"]
	if web.Name != "web" || web.Context != "Dockerfile.web" || !reflect.DeepEqual(web.Inputs, []string{"e2e", "deploy/grafana"}) {
		t.Fatalf("web = %+v", web)
	}
	if lane, ok := cfg.LaneOf("cargo-test.release"); !ok || lane.Name != "unit" || lane.Context != "" {
		t.Fatalf("LaneOf(cargo-test.release) = %+v, %v", lane, ok)
	}
	if _, ok := cfg.LaneOf("build-web"); ok {
		t.Fatal("an undeclared step has a lane")
	}
	if got := cfg.Steps(); !reflect.DeepEqual(got, []string{"cargo-test", "cargo-test.release", "e2e-web"}) {
		t.Fatalf("Steps = %v", got)
	}
	if got := cfg.Names(); !reflect.DeepEqual(got, []string{"unit", "web"}) {
		t.Fatalf("Names = %v", got)
	}
}

func TestParseRefuses(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"no lanes":         {`lanes: {}`, "no lanes"},
		"empty file":       {``, "EOF"},
		"unknown key":      {"lanes:\n  web:\n    input: [e2e]\n    steps: [s]\n", "field input not found"},
		"unknown top key":  {"lane:\n  web: {}\n", "field lane not found"},
		"no steps":         {"lanes:\n  web:\n    inputs: [e2e]\n", "lane web: no steps"},
		"duplicate step":   {"lanes:\n  a:\n    steps: [s]\n  b:\n    steps: [s]\n", "step s is in lanes a and b"},
		"bad step name":    {"lanes:\n  a:\n    steps: [\"x/y\"]\n", `step "x/y"`},
		"dotdot step name": {"lanes:\n  a:\n    steps: [\"x..y\"]\n", `step "x..y"`},
		"lock step name":   {"lanes:\n  a:\n    steps: [x.lock]\n", `step "x.lock"`},
		"bad lane name":    {"lanes:\n  \"a b\":\n    steps: [s]\n", `lane "a b"`},
		"absolute input":   {"lanes:\n  a:\n    inputs: [/etc]\n    steps: [s]\n", "relative to the repository root"},
		"escaping input":   {"lanes:\n  a:\n    inputs: [../x]\n    steps: [s]\n", "inside the repository"},
		"dot input":        {"lanes:\n  a:\n    inputs: [./]\n    steps: [s]\n", "must name a file"},
		"escaping context": {"lanes:\n  a:\n    context: ../Dockerfile\n    steps: [s]\n", "context"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("lanes.yml", []byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
