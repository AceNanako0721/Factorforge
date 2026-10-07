package repoguard

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func plainMarkdown(text string) string {
	text = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`).ReplaceAllString(text, "$1")
	return strings.NewReplacer("**", "", "`", "").Replace(text)
}
func MarkdownUnits(text string) []string {
	lines := strings.Split(text, "\n")
	units := []string{}
	for index := 0; index < len(lines); {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			index++
			continue
		}
		if strings.HasPrefix(line, "| ") {
			if !regexp.MustCompile(`^\|[\s|:\-]+$`).MatchString(line) {
				for _, cell := range strings.Split(strings.Trim(line, "|"), "|") {
					units = append(units, plainMarkdown(strings.TrimSpace(cell)))
				}
			}
			index++
			continue
		}
		if match := regexp.MustCompile(`^#{1,4} (.+)$`).FindStringSubmatch(line); match != nil {
			units = append(units, plainMarkdown(match[1]))
			index++
			continue
		}
		chunk := []string{line}
		index++
		for index < len(lines) && strings.TrimSpace(lines[index]) != "" && !strings.HasPrefix(lines[index], "#") && !strings.HasPrefix(lines[index], "| ") {
			chunk = append(chunk, lines[index])
			index++
		}
		units = append(units, plainMarkdown(strings.Join(chunk, " ")))
	}
	return units
}
func CheckWord(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	archive, err := zip.OpenReader(strings.TrimSuffix(path, filepath.Ext(path)) + ".docx")
	if err != nil {
		return fmt.Errorf("paired Word document missing: %s", filepath.Base(path))
	}
	defer archive.Close()
	var reader io.ReadCloser
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			reader, err = file.Open()
			break
		}
	}
	if reader == nil || err != nil {
		return fmt.Errorf("invalid Word container")
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	units := []string{}
	inP, inText, bodyDepth := false, false, 0
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid Word XML")
		}
		switch x := token.(type) {
		case xml.StartElement:
			if x.Name.Local == "body" {
				bodyDepth++
			}
			if bodyDepth > 0 && x.Name.Local == "p" {
				inP = true
				text.Reset()
			}
			if inP && x.Name.Local == "t" {
				inText = true
			}
		case xml.CharData:
			if inP && inText {
				text.Write([]byte(x))
			}
		case xml.EndElement:
			if x.Name.Local == "t" {
				inText = false
			}
			if x.Name.Local == "p" && inP {
				if text.Len() > 0 {
					units = append(units, text.String())
				}
				inP = false
			}
			if x.Name.Local == "body" {
				bodyDepth--
			}
		}
	}
	if !equalJSON(units, MarkdownUnits(string(data))) {
		return fmt.Errorf("Word/Markdown body mismatch: %s", filepath.Base(path))
	}
	return nil
}
func CheckLegacyWords(root, dir string) error {
	if _, err := os.Stat(filepath.Join(root, dir, "README.md")); err != nil {
		return fmt.Errorf("legacy document index missing")
	}
	for _, prefix := range []string{"01_交易系统层", "02_策略化框架层", "03_SOXLUSDT_JEV应用实例"} {
		for _, suffix := range []string{"式样书.md", "设计书.md"} {
			if err := CheckWord(filepath.Join(root, dir, prefix+suffix)); err != nil {
				return err
			}
		}
	}
	return CheckWord(filepath.Join(root, dir, "开发规划书.md"))
}
