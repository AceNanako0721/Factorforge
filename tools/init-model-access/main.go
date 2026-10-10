// Initialize only the empty model section in the canonical private config.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"

	"github.com/pelletier/go-toml/v2"
)

func main() {
	if err := initialize(); err != nil {
		fmt.Fprintln(os.Stderr, "MODEL_CONFIG_INIT_FAILED: inspect private config and existing model process")
		os.Exit(1)
	}
}

func initialize() error {
	const marker = "# BEGIN FACTORFORGE MODEL ACCESS"
	template, err := os.ReadFile("config/config.example.toml")
	if err != nil {
		return err
	}
	offset := bytes.Index(template, []byte(marker))
	if offset < 0 {
		return fmt.Errorf("template")
	}
	fileName, err := filepath.Abs("config/config.toml")
	if err != nil {
		return err
	}
	for p := fileName; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("private path")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	info, err := os.Lstat("config/config.toml")
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private file")
	}
	lockName := filepath.Join(filepath.Dir(fileName), ".model-access.lock")
	lock, err := os.OpenFile(lockName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(lockName)
	ownerBytes := make([]byte, 16)
	if _, err = rand.Read(ownerBytes); err != nil {
		lock.Close()
		return err
	}
	host, _ := os.Hostname()
	metadata, _ := json.Marshal(map[string]any{"owner": hex.EncodeToString(ownerBytes), "pid": os.Getpid(), "hostname": host})
	_, err = lock.Write(metadata)
	if err == nil {
		err = lock.Sync()
	}
	lock.Close()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile("config/config.toml")
	if err != nil {
		return err
	}
	var parsedOld map[string]any
	if err = toml.Unmarshal(raw, &parsedOld); err != nil {
		return err
	}
	var next []byte
	if bytes.Contains(raw, []byte(marker)) {
		const endMarker = "# END FACTORFORGE MODEL ACCESS"
		start, end := bytes.Index(raw, []byte(marker)), bytes.Index(raw, []byte(endMarker))
		if bytes.Count(raw, []byte(marker)) != 1 || bytes.Count(raw, []byte(endMarker)) != 1 || end < start || len(bytes.TrimSpace(raw[end+len(endMarker):])) != 0 {
			return fmt.Errorf("model block")
		}
		model, ok := parsedOld["model_access"].(map[string]any)
		if !ok {
			return fmt.Errorf("model section")
		}
		// Upgrade by inserting only missing path fields. Preserve every original
		// byte, including tokens, comments and limits, rather than rewriting TOML.
		var additions []byte
		for _, key := range []string{"claude_cli_path", "antigravity_cli_path"} {
			if value, exists := model[key]; exists {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("model path")
				}
			} else {
				additions = append(additions, []byte(key+" = \"\"\n")...)
			}
		}
		if len(additions) == 0 {
			fmt.Println("Model access block already current; nothing changed")
			return nil
		}
		next = append(append(append([]byte{}, raw[:end]...), additions...), raw[end:]...)
	} else {
		if _, exists := parsedOld["model_access"]; exists {
			return fmt.Errorf("existing section")
		}
		next = append(append(append([]byte{}, raw...), '\n'), template[offset:]...)
	}
	// Validate complete TOML and unchanged non-model configuration before replace.
	var parsed map[string]any
	if err = toml.Unmarshal(next, &parsed); err != nil {
		return err
	}
	delete(parsedOld, "model_access")
	delete(parsed, "model_access")
	if !reflect.DeepEqual(parsedOld, parsed) {
		return fmt.Errorf("outside model changed")
	}
	file, err := os.CreateTemp(filepath.Dir(fileName), ".model-init-*.tmp")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err != nil {
		return err
	}
	if _, err = file.Write(next); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	current, err := os.ReadFile(fileName)
	if err != nil || !bytes.Equal(current, raw) {
		return fmt.Errorf("config changed")
	}
	if err = os.Rename(file.Name(), fileName); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		directory, e := os.Open(filepath.Dir(fileName))
		if e != nil {
			return e
		}
		defer directory.Close()
		if e = directory.Sync(); e != nil {
			return e
		}
	}
	fmt.Println("Appended empty model access block; no accounts or services started")
	return nil
}
