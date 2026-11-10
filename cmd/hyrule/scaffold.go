package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// renderTemplateDir walks the embedded template tree rooted at base,
// rendering each file as a text/template with data and writing it under
// outDir at the same relative path, with the .tmpl suffix stripped.
func renderTemplateDir(templates fs.FS, base, outDir string, data any) error {
	return fs.WalkDir(templates, base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(outDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		raw, err := fs.ReadFile(templates, path)
		if err != nil {
			return err
		}
		tmpl, err := template.New(d.Name()).Parse(string(raw))
		if err != nil {
			return err
		}

		dest = strings.TrimSuffix(dest, ".tmpl")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, err := os.Create(dest)
		if err != nil {
			return err
		}
		defer f.Close()
		return tmpl.Execute(f, data)
	})
}
