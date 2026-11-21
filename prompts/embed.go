// Package prompts embeds the versioned prompt files (<name>.<version>.md).
package prompts

import (
	"embed"
	"fmt"
)

//go:embed *.md
var files embed.FS

// Load returns the prompt template for name and version, e.g. Load("seat", "v1").
func Load(name, version string) (string, error) {
	b, err := files.ReadFile(fmt.Sprintf("%s.%s.md", name, version))
	if err != nil {
		return "", fmt.Errorf("prompt %s.%s: %w", name, version, err)
	}
	return string(b), nil
}
