package agent

import (
	"fmt"
	"os"

	"github.com/SeanoChang/keel/internal/workspace"
)

type Agent struct {
	Name string
	Dir  string
}

func New(name, dir string) (*Agent, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("agent dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("agent dir %q is not a directory", dir)
	}
	return &Agent{Name: name, Dir: dir}, nil
}

func (a *Agent) HasGoals() bool {
	return workspace.HasGoals(a.Dir)
}
