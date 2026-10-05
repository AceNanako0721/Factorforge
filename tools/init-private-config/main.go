// Prepare ignored private assets without overwriting existing files.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	for _, pair := range [][2]string{{"config/config.example.toml", "config/config.toml"}, {"prompts/prompts.example.json", "prompts/prompts.local.json"}} {
		if err := copyOnce(pair[0], pair[1]); err != nil {
			fmt.Fprintln(os.Stderr, "PRIVATE_ASSET_INITIALIZATION_FAILED")
			os.Exit(1)
		}
	}
}
func copyOnce(template, destination string) error {
	source, err := os.Open(template)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		fmt.Println("Kept existing " + filepath.ToSlash(destination))
		return nil
	}
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err = io.Copy(target, source); err != nil {
		return err
	}
	fmt.Println("Created " + filepath.ToSlash(destination))
	return nil
}
