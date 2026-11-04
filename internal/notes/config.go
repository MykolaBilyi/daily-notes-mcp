package notes

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config describes where and how daily notes are stored.
type Config struct {
	Folder   string `json:"folder"`
	Format   string `json:"format"`
	Template string `json:"template"`
}

// LoadConfig reads an Obsidian daily-notes.json file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read notes config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse notes config %s: %w", path, err)
	}
	return cfg, nil
}
