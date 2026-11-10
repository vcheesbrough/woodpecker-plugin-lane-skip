// Package lanes reads the consumer's lane configuration (.woodpecker/lanes.yml
// by default):
//
//	lanes:
//	  web:
//	    context: Dockerfile.web      # optional: the Dockerfile whose build context the lane covers
//	    inputs: [e2e, deploy/grafana] # files or directories read from outside that context
//	    steps: [e2e-web]              # the test steps that consult this lane
package lanes

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultPath is where the config lives unless the `config` setting says
// otherwise.
const DefaultPath = ".woodpecker/lanes.yml"

// Lane is a set of files and the steps that pass or fail on them.
type Lane struct {
	Name    string   `yaml:"-"`
	Context string   `yaml:"context"`
	Inputs  []string `yaml:"inputs"`
	Steps   []string `yaml:"steps"`
}

// Config is a parsed lanes.yml.
type Config struct {
	Path  string
	Lanes map[string]*Lane
	steps map[string]*Lane
}

// A name becomes a ref component (refs/ci/green/<step>/<key>) and a file name
// (.lane/<step>.key), so it is held to what is safe in both.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validName(name string) bool {
	return nameRE.MatchString(name) && !strings.Contains(name, "..") && !strings.HasSuffix(name, ".lock")
}

// Parse reads lanes.yml content. Unknown keys are refused, so a typo cannot
// silently drop an input from a key.
func Parse(configPath string, data []byte) (*Config, error) {
	var raw struct {
		Lanes map[string]*Lane `yaml:"lanes"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}
	if len(raw.Lanes) == 0 {
		return nil, fmt.Errorf("%s: no lanes", configPath)
	}
	cfg := &Config{Path: configPath, Lanes: raw.Lanes, steps: map[string]*Lane{}}
	var problems []string
	for _, name := range cfg.Names() {
		lane := cfg.Lanes[name]
		if lane == nil {
			lane = &Lane{}
			cfg.Lanes[name] = lane
		}
		lane.Name = name
		if !validName(name) {
			problems = append(problems, fmt.Sprintf("lane %q: names are letters, digits, '.', '_' and '-'", name))
		}
		if lane.Context != "" {
			clean, err := cleanPath(lane.Context)
			if err != nil {
				problems = append(problems, fmt.Sprintf("lane %s: context: %v", name, err))
			}
			lane.Context = clean
		}
		for i, in := range lane.Inputs {
			clean, err := cleanPath(in)
			if err != nil {
				problems = append(problems, fmt.Sprintf("lane %s: input %q: %v", name, in, err))
			}
			lane.Inputs[i] = clean
		}
		if len(lane.Steps) == 0 {
			problems = append(problems, fmt.Sprintf("lane %s: no steps", name))
		}
		for _, step := range lane.Steps {
			if !validName(step) {
				problems = append(problems, fmt.Sprintf("lane %s: step %q: names are letters, digits, '.', '_' and '-'", name, step))
				continue
			}
			if other, dup := cfg.steps[step]; dup {
				problems = append(problems, fmt.Sprintf("step %s is in lanes %s and %s; a step consults one lane", step, other.Name, name))
				continue
			}
			cfg.steps[step] = lane
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s: %s", configPath, strings.Join(problems, "; "))
	}
	return cfg, nil
}

// cleanPath makes a repository-relative path canonical: `./e2e/` is `e2e`.
func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "/") {
		return p, errors.New("must be relative to the repository root")
	}
	clean := path.Clean(p)
	if clean == "." || clean == "" {
		return p, errors.New("must name a file or directory")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return p, errors.New("must stay inside the repository")
	}
	return clean, nil
}

// Names returns the lane names in byte order.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Lanes))
	for name := range c.Lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LaneOf returns the lane a step consults.
func (c *Config) LaneOf(step string) (*Lane, bool) {
	lane, ok := c.steps[step]
	return lane, ok
}

// Steps returns every declared step in byte order.
func (c *Config) Steps() []string {
	steps := make([]string, 0, len(c.steps))
	for step := range c.steps {
		steps = append(steps, step)
	}
	sort.Strings(steps)
	return steps
}

// ValidStepName reports whether a step name from a setting can name a marker.
func ValidStepName(step string) bool { return validName(step) }
